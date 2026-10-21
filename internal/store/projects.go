package store

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func (s *Store) existingProject() error {
	info, err := os.Lstat(s.ProjectDir())
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("envkit: project must be a directory, not a symlink: %s", s.ProjectDir())
	}
	info, err = os.Lstat(s.Path())
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("envkit: env file must be regular, not a symlink: %s", s.Path())
	}
	return nil
}

// RenameProject moves an explicit project, including local directory metadata.
// It never changes the handle's selected Project.
func (s *Store) RenameProject(oldName, newName string) error {
	if err := validateProject(oldName); err != nil {
		return err
	}
	if err := validateProject(newName); err != nil {
		return err
	}
	return s.withVaultLock(func() error {
		old := NewForProject(s.Home, oldName)
		next := NewForProject(s.Home, newName)
		if err := old.existingProject(); err != nil {
			return err
		}
		if _, err := os.Lstat(next.ProjectDir()); err == nil {
			return fmt.Errorf("envkit: destination project already exists: %s", newName)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if tracked, err := s.trackedPath(":(literal)" + newName); err != nil {
			return err
		} else if tracked {
			return fmt.Errorf("envkit: destination project already tracked: %s", newName)
		}
		// Git rejects an old path that was never audited or indexed.
		paths := []string{next.pathspec()}
		tracked, err := s.trackedPath(old.pathspec())
		if err != nil {
			return err
		}
		if tracked {
			paths = append(paths, old.pathspec())
		}
		if err := filepath.WalkDir(old.ProjectDir(), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("envkit: symlink not allowed: %s", path)
			}
			return nil
		}); err != nil {
			return err
		}
		return s.withIndexRollback(func() error {
			// Separate dirty content from the move so Git can follow an exact rename.
			if tracked {
				if err := old.commit(fmt.Sprintf("%s: snapshot before rename", oldName)); err != nil {
					return err
				}
			}
			if err := os.Rename(old.ProjectDir(), next.ProjectDir()); err != nil {
				return err
			}
			if err := s.commitPaths(fmt.Sprintf("%s: rename to %s", oldName, newName), paths...); err != nil {
				return errors.Join(err, os.Rename(next.ProjectDir(), old.ProjectDir()))
			}
			return nil
		})
	})
}

func (s *Store) trackedPath(path string) (bool, error) {
	out, err := s.git("ls-files", "--", path)
	if err != nil {
		return false, err
	}
	if len(out) != 0 {
		return true, nil
	}
	if _, err := s.git("rev-parse", "--verify", "--quiet", "HEAD"); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return false, nil // Unborn repository has no tracked paths in HEAD.
		}
		return false, err
	}
	out, err = s.git("ls-tree", "-r", "--name-only", "HEAD", "--", path)
	return len(out) != 0, err
}

// DeleteProject requires caller confirmation. Latest on-disk .env is audited
// before deletion; a failed deletion may leave that snapshot in audit history.
func (s *Store) DeleteProject(name string) error {
	if err := validateProject(name); err != nil {
		return err
	}
	return s.withVaultLock(func() error {
		project := NewForProject(s.Home, name)
		if err := project.existingProject(); err != nil {
			return err
		}
		entries, err := os.ReadDir(project.ProjectDir())
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if (entry.Name() != ".env" && entry.Name() != directoriesFile) || !entry.Type().IsRegular() {
				return fmt.Errorf("envkit: refusing deletion: unexpected file or recovery edits: %s", entry.Name())
			}
		}
		if _, err := project.readDirectories(); err != nil {
			return err
		}
		// Keep recoverable data inside private Git metadata until audit succeeds.
		backup, err := os.MkdirTemp(filepath.Join(s.Home, ".git"), "envkit-delete-")
		if err != nil {
			return err
		}
		defer os.Remove(backup) // Only removes an empty recovery container.
		saved := filepath.Join(backup, "project")
		if err := s.withIndexRollback(func() error {
			if err := project.commit(fmt.Sprintf("%s: snapshot before deletion", name)); err != nil {
				return err
			}
			if err := os.Rename(project.ProjectDir(), saved); err != nil {
				return err
			}
			if err := s.commitPaths(fmt.Sprintf("%s: delete project", name), project.pathspec()); err != nil {
				return errors.Join(err, os.Rename(saved, project.ProjectDir()))
			}
			return nil
		}); err != nil {
			return err
		}
		for _, entry := range entries {
			if err := os.Remove(filepath.Join(saved, entry.Name())); err != nil {
				return fmt.Errorf("envkit: deletion audited; recovery data at %s: %w", saved, err)
			}
		}
		return os.Remove(saved)
	})
}

// Restore the exact original index on failure, including unrelated staging.
// Vault flock serializes envkit clients, not external Git writers.
func (s *Store) withIndexRollback(fn func() error) error {
	path := filepath.Join(s.Home, ".git", "index")
	if err := rejectSymlink(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return err
	}
	if err := fn(); err != nil {
		var restore error
		if missing {
			restore = os.Remove(path)
			if errors.Is(restore, os.ErrNotExist) {
				restore = nil
			}
		} else {
			var file *os.File
			file, restore = os.CreateTemp(filepath.Join(s.Home, ".git"), "envkit-index-*")
			if restore == nil {
				defer os.Remove(file.Name())
				_, restore = file.Write(data)
				restore = errors.Join(restore, file.Close())
				if restore == nil {
					restore = os.Rename(file.Name(), path)
				}
			}
		}
		return errors.Join(err, restore)
	}
	return nil
}
