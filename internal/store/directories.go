package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

const directoriesFile = ".directories.json"

// ProjectDirectories returns visited source roots, not vault paths. Legacy
// projects have none. Reading metadata never runs Git or creates a project.
func (s *Store) ProjectDirectories(name string) ([]string, error) {
	if err := validateProject(name); err != nil {
		return nil, err
	}
	var directories []string
	err := s.withHomeLock(func() error {
		project := NewForProject(s.Home, name)
		if err := project.existingProject(); err != nil {
			return err
		}
		var err error
		directories, err = project.readDirectories()
		return err
	})
	return directories, err
}

func (s *Store) readDirectories() ([]string, error) {
	path := filepath.Join(s.ProjectDir(), directoriesFile)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("envkit: directories metadata must be regular: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var directories []string
	if err := json.Unmarshal(data, &directories); err != nil {
		return nil, fmt.Errorf("envkit: invalid directories metadata: %w", err)
	}
	return directories, nil
}

// Associations stay local/untracked; only .env participates in audit commits.
func (s *Store) recordDirectory() error {
	if s.directory == "" {
		return nil
	}
	directories, err := s.readDirectories()
	if err != nil {
		return err
	}
	path := filepath.Join(s.ProjectDir(), directoriesFile)
	if slices.Contains(directories, s.directory) {
		return os.Chmod(path, 0o600)
	}
	data, err := json.Marshal(append(directories, s.directory))
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(s.ProjectDir(), ".envkit-directories-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
