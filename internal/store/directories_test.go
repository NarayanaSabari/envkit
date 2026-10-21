package store

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNewRecordsOnlyVisitedSourceRootUnderLock(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "envkit.project", "shared")
	nested := filepath.Join(root, "nested", "deep")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "vault")
	t.Setenv("ENVKIT_HOME", home)
	t.Setenv("ENVKIT_PROJECT", "")
	s, err := New(nested)
	if err != nil {
		t.Fatal(err)
	}
	if s.Project != "shared" {
		t.Fatalf("project = %q", s.Project)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatal("New modified disk")
	}
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	configureGit(t, home)
	if _, err := s.Read(); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("KEY", "synthetic", nil); err != nil {
		t.Fatal(err)
	}
	rootHandle, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := rootHandle.Ensure(); err != nil {
		t.Fatal(err)
	}
	if directories, err := s.ProjectDirectories("shared"); err != nil || !reflect.DeepEqual(directories, []string{gitOutput(root, "rev-parse", "--show-toplevel")}) {
		t.Fatalf("root resolution/dedup: %v %v", directories, err)
	}
	if got := runGit(t, home, "ls-files"); got != "shared/.env\n" {
		t.Fatalf("metadata tracked: %q", got)
	}
	info, err := os.Stat(filepath.Join(s.ProjectDir(), directoriesFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("metadata permissions: %v %v", info, err)
	}
}

func TestNewRecordsAbsoluteNonGitDirectory(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("ENVKIT_HOME", filepath.Join(t.TempDir(), "vault"))
	t.Setenv("ENVKIT_PROJECT", "explicit")
	// Relative input must produce an absolute association and project name.
	t.Chdir(cwd)
	s, err := New(".")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	if directories, err := s.ProjectDirectories("explicit"); err != nil || !reflect.DeepEqual(directories, []string{cwd}) {
		t.Fatalf("non-Git source: %v %v", directories, err)
	}
}

func TestNewRecordsMultipleVisitedWorktreesAndRename(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	configureGit(t, root)
	runGit(t, root, "config", "envkit.project", "shared")
	runGit(t, root, "commit", "--allow-empty", "-qm", "synthetic fixture")
	worktree := filepath.Join(t.TempDir(), "worktree")
	unvisited := filepath.Join(t.TempDir(), "unvisited")
	runGit(t, root, "worktree", "add", "-qb", "visited", worktree)
	runGit(t, root, "worktree", "add", "-qb", "unvisited", unvisited)
	home := filepath.Join(t.TempDir(), "vault")
	t.Setenv("ENVKIT_HOME", home)
	t.Setenv("ENVKIT_PROJECT", "")
	for _, cwd := range []string{root, worktree, root, worktree} {
		s, err := New(cwd)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Ensure(); err != nil {
			t.Fatal(err)
		}
	}
	configureGit(t, home)
	s := NewForProject(home, "shared")
	want := []string{gitOutput(root, "rev-parse", "--show-toplevel"), gitOutput(worktree, "rev-parse", "--show-toplevel")}
	if directories, err := s.ProjectDirectories("shared"); err != nil || !reflect.DeepEqual(directories, want) {
		t.Fatalf("worktree associations: %v %v", directories, err)
	}
	if err := s.RenameProject("shared", "renamed"); err != nil {
		t.Fatal(err)
	}
	if directories, err := s.ProjectDirectories("renamed"); err != nil || !reflect.DeepEqual(directories, want) {
		t.Fatalf("renamed associations: %v %v", directories, err)
	}
}

func TestProjectDirectoriesLegacyNoGitNoCreationAndExplicitName(t *testing.T) {
	s := testStore(t, "legacy")
	// NewForProject must never associate the vault or process cwd.
	if _, err := os.Stat(filepath.Join(s.ProjectDir(), directoriesFile)); !os.IsNotExist(err) {
		t.Fatal("invented association")
	}
	receiver := NewForProject(s.Home, "../invalid-unrelated-selection")
	t.Setenv("PATH", t.TempDir()) // Directory display must not invoke Git.
	if directories, err := receiver.ProjectDirectories("legacy"); err != nil || len(directories) != 0 {
		t.Fatalf("legacy: %v %v", directories, err)
	}
	if _, err := receiver.ProjectDirectories("missing"); !os.IsNotExist(err) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Home, "missing")); !os.IsNotExist(err) {
		t.Fatal("metadata read created missing project")
	}
	plain := NewForProject(t.TempDir(), "plain")
	if err := os.Mkdir(plain.ProjectDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, plain.Path(), "")
	if _, err := plain.ProjectDirectories("plain"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(plain.Home, ".git")); !os.IsNotExist(err) {
		t.Fatal("metadata read initialized Git")
	}
}

func TestDirectoryMetadataFailuresPreserveData(t *testing.T) {
	for _, kind := range []string{"invalid-json", "symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t, "project")
			s.directory = "/visited/root"
			path := filepath.Join(s.ProjectDir(), directoriesFile)
			outside := filepath.Join(t.TempDir(), "outside")
			writeFile(t, outside, "keep")
			switch kind {
			case "invalid-json":
				writeFile(t, path, "broken metadata")
			case "symlink":
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			for _, call := range []func() error{s.Ensure, func() error { _, err := s.ProjectDirectories("project"); return err }, func() error { return s.DeleteProject("project") }} {
				if err := call(); err == nil {
					t.Fatal("unsafe metadata accepted")
				}
			}
			if readFile(t, outside) != "keep" {
				t.Fatal("outside metadata modified")
			}
			if kind == "invalid-json" && readFile(t, path) != "broken metadata" {
				t.Fatal("invalid metadata overwritten")
			}
			if _, err := os.Stat(s.Path()); err != nil {
				t.Fatal("metadata failure removed env", err)
			}
		})
	}
}
