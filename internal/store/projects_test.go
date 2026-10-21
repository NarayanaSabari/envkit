package store

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func stageUnrelated(t *testing.T, s *Store) string {
	t.Helper()
	writeFile(t, filepath.Join(s.Home, "unrelated"), "staged content\n")
	runGit(t, s.Home, "add", "--", "unrelated")
	writeFile(t, filepath.Join(s.Home, "unrelated"), "unstaged content\n")
	return runGit(t, s.Home, "diff", "--cached", "--", "unrelated")
}

func assertUnrelatedStaging(t *testing.T, s *Store, want string) {
	t.Helper()
	if got := runGit(t, s.Home, "diff", "--cached", "--", "unrelated"); got != want {
		t.Fatalf("unrelated staging changed: %q, want %q", got, want)
	}
	if got := readFile(t, filepath.Join(s.Home, "unrelated")); got != "unstaged content\n" {
		t.Fatal("unrelated working file changed")
	}
	if got := runGit(t, s.Home, "show", "--format=", "--name-only", "HEAD"); strings.Contains(got, "unrelated") {
		t.Fatalf("unrelated staging committed: %s", got)
	}
}

func TestLifecycleExplicitTargetLeavesSelectedProjectUntouched(t *testing.T) {
	for _, operation := range []string{"rename", "delete"} {
		for _, selected := range []string{"selected", "../invalid-selection"} {
			t.Run(operation+"/"+selected, func(t *testing.T) {
				s := testStore(t, "selected")
				if err := s.Set("KEY", "keep-selected", nil); err != nil {
					t.Fatal(err)
				}
				target := NewForProject(s.Home, "target")
				if err := target.Set("KEY", "target-value", nil); err != nil {
					t.Fatal(err)
				}
				receiver := NewForProject(s.Home, selected)
				var err error
				if operation == "rename" {
					err = receiver.RenameProject("target", "renamed")
				} else {
					err = receiver.DeleteProject("target")
				}
				if err != nil {
					t.Fatal(err)
				}
				if receiver.Project != selected || readFile(t, s.Path()) != "KEY=keep-selected\n" {
					t.Fatal("explicit operation changed selected project")
				}
			})
		}
	}
}

func TestRenameProjectExplicitTargetAndHistory(t *testing.T) {
	for _, active := range []bool{true, false} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			s := testStore(t, "old[*]")
			if err := s.Set("KEY", "synthetic-old", nil); err != nil {
				t.Fatal(err)
			}
			if err := s.Set("KEY", "synthetic-current", nil); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(s.ProjectDir(), directoriesFile), `["/visited/root"]`)
			writeFile(t, filepath.Join(s.ProjectDir(), ".envkit-edit-recovery"), "keep recovery")
			stage := stageUnrelated(t, s)
			receiver := s
			if !active {
				receiver = NewForProject(s.Home, "unloaded")
			}
			selected := receiver.Project
			before := runGit(t, s.Home, "rev-list", "--count", "HEAD")
			if err := receiver.RenameProject(s.Project, "new?name"); err != nil {
				t.Fatal(err)
			}
			if receiver.Project != selected {
				t.Fatal("rename changed receiver selection")
			}
			next := NewForProject(s.Home, "new?name")
			if got := readFile(t, next.Path()); got != "KEY=synthetic-current\n" {
				t.Fatalf("renamed value = %q", got)
			}
			if got := readFile(t, filepath.Join(next.ProjectDir(), ".envkit-edit-recovery")); got != "keep recovery" {
				t.Fatal("rename lost recovery file")
			}
			if directories, err := receiver.ProjectDirectories(next.Project); err != nil || !reflect.DeepEqual(directories, []string{"/visited/root"}) {
				t.Fatalf("renamed metadata: %v %v", directories, err)
			}
			if _, err := os.Stat(s.ProjectDir()); !os.IsNotExist(err) {
				t.Fatalf("old source remains: %v", err)
			}
			if got := runGit(t, s.Home, "rev-list", "--count", "HEAD"); before != "2\n" || got != "3\n" {
				t.Fatalf("rename must use one commit: before %q after %q", before, got)
			}
			for _, history := range []func() (string, error){func() (string, error) { return next.Log("") }, func() (string, error) { return next.Log("KEY") }, func() (string, error) { return next.KeyHistory("KEY", "%s") }} {
				text, err := history()
				if err != nil || !strings.Contains(text, "old[*]: set KEY") {
					t.Fatalf("history does not follow rename: %q %v", text, err)
				}
			}
			if history, err := next.KeyHistories("%s"); err != nil || len(history["KEY"]) != 2 {
				t.Fatalf("batched history does not follow rename: %v %v", history, err)
			}
			if projects, err := receiver.Projects(); err != nil || !reflect.DeepEqual(projects, []string{"new?name"}) {
				t.Fatalf("projects recreated selected source: %v %v", projects, err)
			}
			assertUnrelatedStaging(t, s, stage)
		})
	}
}

func TestRenameProjectNeverAuditedAndStagedDeletion(t *testing.T) {
	for _, kind := range []string{"empty", "nonempty", "staged-deletion"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t, "old")
			if kind != "empty" {
				writeFile(t, s.Path(), "KEY=synthetic\n")
			}
			if kind == "staged-deletion" {
				if err := s.Commit("initial"); err != nil {
					t.Fatal(err)
				}
				runGit(t, s.Home, "rm", "--cached", "--", "old/.env")
			}
			before := readFile(t, s.Path())
			if err := s.RenameProject("old", "new"); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, filepath.Join(s.Home, "new", ".env")); got != before {
				t.Fatalf("value changed: %q", got)
			}
			if got := runGit(t, s.Home, "status", "--porcelain", "--", "old", "new"); got != "" {
				t.Fatalf("rename not audited: %s", got)
			}
		})
	}
}

func TestDeleteProjectAuditsLatestUncommittedAndDoesNotResurrect(t *testing.T) {
	for _, active := range []bool{true, false} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			s := testStore(t, "target[*]")
			if err := s.Set("KEY", "synthetic-old", nil); err != nil {
				t.Fatal(err)
			}
			writeFile(t, s.Path(), "KEY=synthetic-uncommitted\n")
			writeFile(t, filepath.Join(s.ProjectDir(), directoriesFile), `["/source"]`)
			stage := stageUnrelated(t, s)
			receiver := s
			if !active {
				receiver = NewForProject(s.Home, "unloaded")
			}
			selected := receiver.Project
			if err := receiver.DeleteProject(s.Project); err != nil {
				t.Fatal(err)
			}
			if receiver.Project != selected {
				t.Fatal("delete changed receiver selection")
			}
			if got := runGit(t, s.Home, "show", "HEAD^:target[*]/.env"); got != "KEY=synthetic-uncommitted\n" {
				t.Fatalf("latest data not audited before delete: %q", got)
			}
			if got := runGit(t, s.Home, "ls-tree", "-r", "--name-only", "HEAD"); got != "" {
				t.Fatalf("deletion not audited: %q", got)
			}
			for range 2 {
				if projects, err := receiver.Projects(); err != nil || len(projects) != 0 {
					t.Fatalf("last deletion resurrected project: %v %v", projects, err)
				}
			}
			if _, err := receiver.ProjectDirectories(s.Project); err == nil {
				t.Fatal("missing project's metadata accepted")
			}
			if _, err := os.Stat(s.ProjectDir()); !os.IsNotExist(err) {
				t.Fatalf("deleted project recreated: %v", err)
			}
			assertUnrelatedStaging(t, s, stage)
		})
	}
}

func TestProjectListingAndCancelEquivalentDoNotCreateProjects(t *testing.T) {
	s := testStore(t, "exists")
	if err := s.Set("KEY", "synthetic", nil); err != nil {
		t.Fatal(err)
	}
	index := readFile(t, filepath.Join(s.Home, ".git", "index"))
	head := runGit(t, s.Home, "rev-parse", "HEAD")
	for _, receiver := range []*Store{s, NewForProject(s.Home, "missing")} {
		if projects, err := receiver.Projects(); err != nil || !reflect.DeepEqual(projects, []string{"exists"}) {
			t.Fatalf("projects: %v %v", projects, err)
		}
		if dirs, err := receiver.ProjectDirectories("exists"); err != nil || len(dirs) != 0 {
			t.Fatalf("legacy metadata: %v %v", dirs, err)
		}
	}
	if readFile(t, s.Path()) != "KEY=synthetic\n" || readFile(t, filepath.Join(s.Home, ".git", "index")) != index || runGit(t, s.Home, "rev-parse", "HEAD") != head {
		t.Fatal("read-only/cancel-equivalent calls changed project or audit state")
	}
	if _, err := os.Stat(filepath.Join(s.Home, "missing")); !os.IsNotExist(err) {
		t.Fatal("listing created missing project")
	}
}

func TestLifecycleRejectsInvalidMissingAndCollidingNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../escape", "a/b", `a\b`, ".GIT", "a\x00b"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			s := NewForProject(filepath.Join(t.TempDir(), "vault"), "valid")
			for _, call := range []func() error{func() error { return s.RenameProject(name, "new") }, func() error { return s.RenameProject("old", name) }, func() error { return s.DeleteProject(name) }, func() error { _, err := s.ProjectDirectories(name); return err }} {
				if err := call(); err == nil || !strings.Contains(err.Error(), "invalid project") {
					t.Fatalf("accepted invalid name: %v", err)
				}
			}
			if _, err := os.Stat(s.Home); !os.IsNotExist(err) {
				t.Fatal("invalid arguments touched disk")
			}
		})
	}
	s := testStore(t, "old")
	if err := s.Set("KEY", "keep", nil); err != nil {
		t.Fatal(err)
	}
	other := NewForProject(s.Home, "other")
	if err := other.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() error{func() error { return s.RenameProject("missing", "new") }, func() error { return s.DeleteProject("missing") }, func() error { return s.RenameProject("old", "other") }, func() error { return s.RenameProject("old", "old") }} {
		if err := call(); err == nil {
			t.Fatal("accepted missing/colliding source/destination")
		}
	}
	if got := readFile(t, s.Path()); got != "KEY=keep\n" {
		t.Fatalf("rejected operation changed source: %q", got)
	}
	if _, err := os.Stat(filepath.Join(s.Home, "missing")); !os.IsNotExist(err) {
		t.Fatal("missing source created")
	}
}

func TestRenameProjectRejectsTrackedDestinationWithoutDirectory(t *testing.T) {
	for _, stagedDelete := range []bool{false, true} {
		t.Run(fmt.Sprint(stagedDelete), func(t *testing.T) {
			s := testStore(t, "old")
			if err := s.Set("KEY", "old", nil); err != nil {
				t.Fatal(err)
			}
			next := NewForProject(s.Home, "new")
			if err := next.Set("KEY", "new", nil); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(next.Path()); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(next.ProjectDir()); err != nil {
				t.Fatal(err)
			}
			if stagedDelete {
				runGit(t, s.Home, "add", "-u", "--", "new")
			}
			index := readFile(t, filepath.Join(s.Home, ".git", "index"))
			if err := s.RenameProject("old", "new"); err == nil || !strings.Contains(err.Error(), "already tracked") {
				t.Fatalf("tracked destination accepted: %v", err)
			}
			if got := readFile(t, filepath.Join(s.Home, ".git", "index")); got != index {
				t.Fatal("collision changed index")
			}
		})
	}
}

func TestLifecycleRejectsSymlinksAndDeleteRecoveryFiles(t *testing.T) {
	for _, kind := range []string{"project-link", "env-link", "metadata-link", "nested-link", "recovery", "extra-directory"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t, "old")
			outside := t.TempDir()
			writeFile(t, filepath.Join(outside, ".env"), "KEY=outside\n")
			switch kind {
			case "project-link":
				if err := os.Remove(s.Path()); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(s.ProjectDir()); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, s.ProjectDir()); err != nil {
					t.Fatal(err)
				}
			case "env-link":
				if err := os.Remove(s.Path()); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(outside, ".env"), s.Path()); err != nil {
					t.Fatal(err)
				}
			case "metadata-link":
				if err := os.Symlink(filepath.Join(outside, ".env"), filepath.Join(s.ProjectDir(), directoriesFile)); err != nil {
					t.Fatal(err)
				}
			case "nested-link":
				if err := os.Symlink(outside, filepath.Join(s.ProjectDir(), "nested")); err != nil {
					t.Fatal(err)
				}
			case "recovery":
				writeFile(t, filepath.Join(s.ProjectDir(), ".envkit-edit-recovery"), "keep")
			case "extra-directory":
				if err := os.Mkdir(filepath.Join(s.ProjectDir(), "extra"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.DeleteProject("old"); err == nil {
				t.Fatal("unsafe deletion accepted")
			}
			if strings.Contains(kind, "link") {
				if err := s.RenameProject("old", "new"); err == nil {
					t.Fatal("symlink rename accepted")
				}
			}
			if got := readFile(t, filepath.Join(outside, ".env")); got != "KEY=outside\n" {
				t.Fatal("outside data changed")
			}
			if _, err := os.Lstat(s.ProjectDir()); err != nil {
				t.Fatal("source removed", err)
			}
		})
	}
}

func TestRenameProjectRejectsDestinationSymlink(t *testing.T) {
	s := testStore(t, "old")
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, ".env"), "KEY=keep-outside\n")
	if err := os.Symlink(outside, filepath.Join(s.Home, "new")); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameProject("old", "new"); err == nil {
		t.Fatal("destination symlink accepted")
	}
	if readFile(t, filepath.Join(outside, ".env")) != "KEY=keep-outside\n" {
		t.Fatal("destination symlink target changed")
	}
	if _, err := os.Stat(s.Path()); err != nil {
		t.Fatal("source removed", err)
	}
}

func TestLifecycleGitFailureRestoresInitiallyAbsentIndex(t *testing.T) {
	for _, operation := range []string{"rename", "delete"} {
		t.Run(operation, func(t *testing.T) {
			s := testStore(t, "old")
			writeFile(t, s.Path(), "KEY=never-audited\n")
			hook := filepath.Join(s.Home, ".git", "hooks", "pre-commit")
			writeFile(t, hook, "#!/bin/sh\nexit 1\n")
			if err := os.Chmod(hook, 0o700); err != nil {
				t.Fatal(err)
			}
			var err error
			if operation == "rename" {
				err = s.RenameProject("old", "new")
			} else {
				err = s.DeleteProject("old")
			}
			if err == nil {
				t.Fatal("failed initial commit accepted")
			}
			if got := readFile(t, s.Path()); got != "KEY=never-audited\n" {
				t.Fatalf("source lost: %q", got)
			}
			if _, err := os.Stat(filepath.Join(s.Home, ".git", "index")); !os.IsNotExist(err) {
				t.Fatalf("failed transaction left index: %v", err)
			}
		})
	}
}

func TestLifecycleGitFailureRestoresDirectoryAndIndex(t *testing.T) {
	for _, operation := range []string{"rename", "delete-snapshot", "delete-audit"} {
		t.Run(operation, func(t *testing.T) {
			s := testStore(t, "old")
			if err := s.Set("KEY", "original", nil); err != nil {
				t.Fatal(err)
			}
			writeFile(t, s.Path(), "KEY=latest-uncommitted\n")
			writeFile(t, filepath.Join(s.ProjectDir(), directoriesFile), `["/source"]`)
			stageUnrelated(t, s)
			index := readFile(t, filepath.Join(s.Home, ".git", "index"))
			head := runGit(t, s.Home, "rev-parse", "HEAD")
			hook := "#!/bin/sh\nexit 1\n"
			if operation == "delete-audit" {
				hook = "#!/bin/sh\nif test -e 'old/.env'; then exit 0; fi\nexit 1\n"
			}
			hookPath := filepath.Join(s.Home, ".git", "hooks", "pre-commit")
			writeFile(t, hookPath, hook)
			if err := os.Chmod(hookPath, 0o700); err != nil {
				t.Fatal(err)
			}
			var err error
			if operation == "rename" {
				err = s.RenameProject("old", "new")
			} else {
				err = s.DeleteProject("old")
			}
			if err == nil {
				t.Fatal("Git failure ignored")
			}
			if got := readFile(t, s.Path()); got != "KEY=latest-uncommitted\n" {
				t.Fatalf("source lost: %q", got)
			}
			if got := readFile(t, filepath.Join(s.ProjectDir(), directoriesFile)); got != `["/source"]` {
				t.Fatal("metadata lost")
			}
			if got := readFile(t, filepath.Join(s.Home, ".git", "index")); got != index {
				t.Fatal("original index not restored")
			}
			if _, err := os.Stat(filepath.Join(s.Home, "new")); !os.IsNotExist(err) {
				t.Fatal("failed rename left destination")
			}
			if operation == "delete-audit" {
				if got := runGit(t, s.Home, "show", "HEAD:old/.env"); got != "KEY=latest-uncommitted\n" {
					t.Fatal("latest snapshot missing")
				}
			} else if runGit(t, s.Home, "rev-parse", "HEAD") != head {
				t.Fatal("failed commit changed HEAD")
			}
			if backups, err := filepath.Glob(filepath.Join(s.Home, ".git", "envkit-delete-*")); err != nil || len(backups) != 0 {
				t.Fatalf("recovery containers leaked: %v %v", backups, err)
			}
		})
	}
}
