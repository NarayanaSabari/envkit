package store

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T, project string) *Store {
	t.Helper()
	s := NewForProject(t.TempDir(), project)
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	configureGit(t, s.Home)
	return s
}

func runGit(t *testing.T, home string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = home
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProjectValidationAtEveryStoreEntry(t *testing.T) {
	for _, project := range []string{"", ".", "..", "../escaped", "a/b", `a\b`, ".git", ".GIT", "a\x00b"} {
		t.Run(fmt.Sprintf("%q", project), func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "vault")
			s := NewForProject(home, project)
			calls := map[string]func() error{
				"Ensure":       s.Ensure,
				"Read":         func() error { _, err := s.Read(); return err },
				"Get":          func() error { _, _, err := s.Get("KEY"); return err },
				"Entries":      func() error { _, err := s.Entries(); return err },
				"Set":          func() error { return s.Set("KEY", "value", nil) },
				"Unset":        func() error { return s.Unset("KEY") },
				"Comment":      func() error { return s.Comment("KEY", "text") },
				"Commit":       func() error { return s.Commit("bad") },
				"Edit":         func() error { return s.Edit([]string{"false"}) },
				"Log":          func() error { _, err := s.Log(""); return err },
				"KeyHistory":   func() error { _, err := s.KeyHistory("KEY", "%s"); return err },
				"KeyHistories": func() error { _, err := s.KeyHistories("%s"); return err },
				"Diff":         func() error { _, err := s.Diff(); return err },
				"Projects":     func() error { _, err := s.Projects(); return err },
			}
			for name, call := range calls {
				if err := call(); err == nil || !strings.Contains(err.Error(), "invalid project") {
					t.Errorf("%s: %v", name, err)
				}
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatalf("invalid project touched vault: %v", err)
			}
		})
	}
}

func TestResolveProjectRejectsTraversalOverrides(t *testing.T) {
	cwd := t.TempDir()
	runGit(t, cwd, "init", "-q")
	t.Setenv("ENVKIT_HOME", filepath.Join(t.TempDir(), "vault"))
	t.Setenv("ENVKIT_PROJECT", "../escaped")
	if _, err := New(cwd); err == nil {
		t.Fatal("accepted ENVKIT_PROJECT traversal")
	}
	t.Setenv("ENVKIT_PROJECT", "")
	runGit(t, cwd, "config", "envkit.project", "../escaped")
	if _, err := New(cwd); err == nil {
		t.Fatal("accepted Git config traversal")
	}
	runGit(t, cwd, "config", "envkit.project", "valid-project")
	if s, err := New(cwd); err != nil || s.Project != "valid-project" {
		t.Fatalf("valid project = %#v, %v", s, err)
	}
}

func TestStoreRejectsProjectAndEnvSymlinks(t *testing.T) {
	for _, targetInside := range []bool{false, true} {
		for _, kind := range []string{"project", "env"} {
			t.Run(fmt.Sprintf("%s/inside=%v", kind, targetInside), func(t *testing.T) {
				home := t.TempDir()
				target := t.TempDir()
				if targetInside {
					target = filepath.Join(home, "target")
					if err := os.Mkdir(target, 0o700); err != nil {
						t.Fatal(err)
					}
				}
				outside := filepath.Join(target, ".env")
				writeFile(t, outside, "SECRET=untouched\n")
				s := NewForProject(home, "project")
				link, destination := s.ProjectDir(), target
				if kind == "env" {
					if err := os.Mkdir(s.ProjectDir(), 0o700); err != nil {
						t.Fatal(err)
					}
					link, destination = s.Path(), outside
				}
				if err := os.Symlink(destination, link); err != nil {
					t.Fatal(err)
				}
				for _, call := range []func() error{s.Ensure, func() error { _, err := s.Read(); return err }, func() error { return s.Set("SECRET", "changed", nil) }, func() error { return s.Edit([]string{"false"}) }} {
					if err := call(); err == nil || !strings.Contains(err.Error(), "symlink") {
						t.Fatalf("symlink accepted: %v", err)
					}
				}
				if got := readFile(t, outside); got != "SECRET=untouched\n" {
					t.Fatalf("outside changed: %q", got)
				}
			})
		}
	}
	for _, path := range []string{".envkit.lock", ".git"} {
		t.Run(path, func(t *testing.T) {
			home := t.TempDir()
			if err := os.Symlink(t.TempDir(), filepath.Join(home, path)); err != nil {
				t.Fatal(err)
			}
			if err := NewForProject(home, "project").Ensure(); err == nil {
				t.Fatal("accepted metadata symlink")
			}
		})
	}
}

func TestStoreRejectsNonRegularEnv(t *testing.T) {
	s := NewForProject(t.TempDir(), "project")
	if err := os.MkdirAll(s.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(s.Path(), "keep"), "untouched")
	if err := s.Ensure(); err == nil || !strings.Contains(err.Error(), "must be regular") {
		t.Fatalf("directory accepted: %v", err)
	}
	if got := readFile(t, filepath.Join(s.Path(), "keep")); got != "untouched" {
		t.Fatal("non-regular env changed")
	}
}

func TestSetRejectsControlCharactersWithoutChangingFile(t *testing.T) {
	s := testStore(t, "project")
	if err := s.Set("KEY", "original", nil); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, s.Path())
	head := runGit(t, s.Home, "rev-parse", "HEAD")
	for _, value := range []string{"first\nINJECTED=second", "first\n# SECRET", "a\rINJECTED=b", "a\r\nb", "a\x00b"} {
		if err := s.Set("KEY", value, nil); err == nil {
			t.Errorf("accepted value %q", value)
		}
		if got := readFile(t, s.Path()); got != before {
			t.Fatalf("file changed: %q", got)
		}
	}
	if got := runGit(t, s.Home, "rev-parse", "HEAD"); got != head {
		t.Fatal("invalid input committed")
	}
	if err := s.Set("EMPTY", "", nil); err != nil {
		t.Fatal(err)
	}
	if value, found, err := s.Get("EMPTY"); err != nil || !found || value != "" {
		t.Fatalf("empty value: %q %v %v", value, found, err)
	}
}

func TestMultilineCommentsCannotInjectAssignments(t *testing.T) {
	s := testStore(t, "project")
	comment := "first\nINJECTED=second\r\n# third\rlast"
	if err := s.Set("KEY", "secret", &comment); err != nil {
		t.Fatal(err)
	}
	want := "# first\n# INJECTED=second\n# # third\n# last\nKEY=secret\n"
	if got := readFile(t, s.Path()); got != want {
		t.Fatalf("comments = %q", got)
	}
	if entries, err := s.Entries(); err != nil || len(entries) != 1 || entries[0].Key != "KEY" {
		t.Fatalf("entries: %#v %v", entries, err)
	}
	if err := s.Comment("KEY", "new\nOTHER=injected"); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, s.Path()); got != "# new\n# OTHER=injected\nKEY=secret\n" {
		t.Fatalf("comment replacement = %q", got)
	}
	before := readFile(t, s.Path())
	if err := s.Comment("KEY", "bad\x00comment"); err == nil {
		t.Fatal("accepted NUL comment")
	}
	if got := readFile(t, s.Path()); got != before {
		t.Fatal("invalid comment changed file")
	}
}

func TestSharedLoadRejectsDuplicateKeysWithoutDestroyingFile(t *testing.T) {
	s := testStore(t, "project")
	input := "# user content\nKEY=first\nunknown text\nKEY=last\n"
	writeFile(t, s.Path(), input)
	for name, call := range map[string]func() error{
		"Read":    func() error { _, err := s.Read(); return err },
		"Get":     func() error { _, _, err := s.Get("KEY"); return err },
		"Entries": func() error { _, err := s.Entries(); return err },
		"Set":     func() error { return s.Set("KEY", "new", nil) },
		"Unset":   func() error { return s.Unset("KEY") },
		"Comment": func() error { return s.Comment("KEY", "new") },
		"Commit":  func() error { return s.Commit("bad") },
		"Edit":    func() error { return s.Edit([]string{"false"}) },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "duplicate key: KEY") {
			t.Errorf("%s: %v", name, err)
		}
		if got := readFile(t, s.Path()); got != input {
			t.Fatalf("%s destroyed file: %q", name, got)
		}
	}
}

func TestSharedLoadPreservesUnknownLinesAndRejectsInvalidValues(t *testing.T) {
	s := testStore(t, "project")
	writeFile(t, s.Path(), "unknown text\nKEY=old\n")
	if err := s.Set("KEY", "new", nil); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, s.Path()); got != "unknown text\nKEY=new\n" {
		t.Fatalf("unknown lines lost: %q", got)
	}
	for _, input := range []string{"KEY=value\r\n", "KEY=val\x00ue\n", "unknown\x00text\n"} {
		writeFile(t, s.Path(), input)
		if _, err := s.Read(); err == nil {
			t.Fatalf("accepted %q", input)
		}
		if got := readFile(t, s.Path()); got != input {
			t.Fatal("invalid file modified")
		}
	}
}

func TestWritesAtomicallyReplacePrivateFileAndCleanFailedTemp(t *testing.T) {
	s := testStore(t, "project")
	if err := s.Set("KEY", "old", nil); err != nil {
		t.Fatal(err)
	}
	old, err := os.Open(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if err := os.Chmod(s.Path(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("KEY", "new", nil); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 100)
	n, err := old.Read(data)
	if err != nil || string(data[:n]) != "KEY=old\n" {
		t.Fatalf("old inode overwritten: %q %v", data[:n], err)
	}
	for path, want := range map[string]os.FileMode{s.Home: 0o700, s.ProjectDir(): 0o700, s.Path(): 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("permissions %s: %v %v", path, info, err)
		}
	}
	if err := os.Remove(s.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(s.Path(), "keep"), "untouched")
	if err := s.write(Parse([]byte("KEY=failure\n"))); err == nil {
		t.Fatal("rename unexpectedly succeeded")
	}
	if got := readFile(t, filepath.Join(s.Path(), "keep")); got != "untouched" {
		t.Fatal("failed write destroyed target")
	}
	files, err := filepath.Glob(filepath.Join(s.ProjectDir(), ".envkit-edit-*"))
	if err != nil || len(files) != 0 {
		t.Fatalf("leaked temporary files: %v %v", files, err)
	}
}

func TestHistoryTracksAssignmentRotationsNotCommentsOrOtherKeys(t *testing.T) {
	s := testStore(t, "project")
	for _, value := range []string{"first", "second"} {
		if err := s.Set("KEY", value, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Comment("KEY", "KEY in comment"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("KEY_SUFFIX", "KEY=elsewhere", nil); err != nil {
		t.Fatal(err)
	}
	other := NewForProject(s.Home, "other")
	if err := other.Set("KEY", "other project", nil); err != nil {
		t.Fatal(err)
	}
	history, err := s.KeyHistory("KEY", "%s")
	if err != nil || strings.Count(history, "project: set KEY\n") != 2 || strings.Count(history, "\n") != 2 {
		t.Fatalf("history = %q, %v", history, err)
	}
	log, err := s.Log("KEY")
	if err != nil || !strings.Contains(log, "+KEY=second") || !strings.Contains(log, "-KEY=first") || strings.Contains(log, "comment KEY") || strings.Contains(log, "set KEY_SUFFIX") {
		t.Fatalf("log = %s, %v", log, err)
	}
	if err := s.Unset("KEY"); err != nil {
		t.Fatal(err)
	}
	history, err = s.KeyHistory("KEY", "%s")
	if err != nil || strings.Count(history, "\n") != 3 {
		t.Fatalf("unset history = %q, %v", history, err)
	}
	for _, key := range []string{".*", "KEY=", "-bad", "KEY\nOTHER"} {
		if _, err := s.Log(key); err == nil {
			t.Errorf("Log accepted %q", key)
		}
		if _, err := s.KeyHistory(key, "%s"); err == nil {
			t.Errorf("KeyHistory accepted %q", key)
		}
	}
	if _, err := s.KeyHistory("", "%s"); err == nil {
		t.Fatal("KeyHistory accepted empty key")
	}
}

func TestCommitOnlySelectedEnvPreservesUnrelatedStaging(t *testing.T) {
	for _, project := range []string{"project", "odd[*]"} {
		t.Run(project, func(t *testing.T) {
			s := testStore(t, project)
			unrelated := filepath.Join(s.Home, "unrelated")
			writeFile(t, unrelated, "staged snapshot\n")
			runGit(t, s.Home, "add", "unrelated")
			writeFile(t, unrelated, "unstaged work\n")
			if err := s.Set("KEY", "first", nil); err != nil {
				t.Fatal(err)
			}
			if got := runGit(t, s.Home, "diff-tree", "--root", "--no-commit-id", "--name-only", "-r", "HEAD"); got != project+"/.env\n" {
				t.Fatalf("commit paths = %q", got)
			}
			if got := runGit(t, s.Home, "show", ":unrelated"); got != "staged snapshot\n" {
				t.Fatalf("staged content = %q", got)
			}
			if got := readFile(t, unrelated); got != "unstaged work\n" {
				t.Fatal("worktree changed")
			}
			staged := runGit(t, s.Home, "diff", "--cached", "--binary")
			head := runGit(t, s.Home, "rev-parse", "HEAD")
			if err := s.Set("KEY", "first", nil); err != nil {
				t.Fatal(err)
			}
			if err := s.Unset("ABSENT"); err != nil {
				t.Fatal(err)
			}
			if err := s.Commit("no-op"); err != nil {
				t.Fatal(err)
			}
			if got := runGit(t, s.Home, "rev-parse", "HEAD"); got != head {
				t.Fatal("no-op created commit")
			}
			if err := s.Set("KEY", "second", nil); err != nil {
				t.Fatal(err)
			}
			if got := runGit(t, s.Home, "diff", "--cached", "--binary"); got != staged {
				t.Fatalf("unrelated staging changed\n%s\nwant\n%s", got, staged)
			}
			if tracked := runGit(t, s.Home, "ls-tree", "-r", "--name-only", "HEAD"); strings.Contains(tracked, ".envkit") {
				t.Fatalf("lock/temp committed: %s", tracked)
			}
		})
	}
}

func TestCommitPreservesStagedModificationAndDeletion(t *testing.T) {
	s := testStore(t, "project")
	if err := s.Set("KEY", "first", nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"modified", "deleted"} {
		writeFile(t, filepath.Join(s.Home, name), "baseline\n")
	}
	runGit(t, s.Home, "add", "modified", "deleted")
	runGit(t, s.Home, "commit", "-q", "-m", "unrelated baseline")
	writeFile(t, filepath.Join(s.Home, "modified"), "staged\n")
	runGit(t, s.Home, "add", "modified")
	writeFile(t, filepath.Join(s.Home, "modified"), "unstaged\n")
	runGit(t, s.Home, "rm", "-q", "deleted")
	before := runGit(t, s.Home, "diff", "--cached", "--binary")
	if err := s.Set("KEY", "second", nil); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, s.Home, "diff", "--cached", "--binary"); got != before {
		t.Fatalf("staging changed: %s, want %s", got, before)
	}
	if got := runGit(t, s.Home, "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD"); got != "project/.env\n" {
		t.Fatalf("commit paths = %q", got)
	}
	if got := readFile(t, filepath.Join(s.Home, "modified")); got != "unstaged\n" {
		t.Fatal("unstaged modification lost")
	}
}

func TestEditValidatesBeforeInstallAndPreservesRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		success      bool
	}{
		{"valid-rename", `printf 'unknown text\nKEY=edited\n' > "$1.new"; mv "$1.new" "$1"`, true},
		{"no-op", `:`, true},
		{"duplicate", `printf 'KEY=first\nKEY=last\n' > "$1"; chmod 644 "$1"`, false},
		{"NUL", `printf 'KEY=bad\000value\n' > "$1"`, false},
		{"CR", `printf 'KEY=bad\r\n' > "$1"`, false},
		{"failed-editor", `printf 'KEY=unfinished\n' > "$1"; exit 1`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t, "project")
			if err := s.Set("KEY", "original", nil); err != nil {
				t.Fatal(err)
			}
			before := readFile(t, s.Path())
			head := runGit(t, s.Home, "rev-parse", "HEAD")
			err := s.Edit([]string{"sh", "-c", tc.script, "editor"})
			if tc.success {
				if err != nil {
					t.Fatal(err)
				}
				if tc.name == "valid-rename" {
					if got := readFile(t, s.Path()); got != "unknown text\nKEY=edited\n" {
						t.Fatalf("edit = %q", got)
					}
					if got := runGit(t, s.Home, "log", "-1", "--format=%s"); got != "project: edit\n" {
						t.Fatalf("edit audit = %q", got)
					}
				} else if got := runGit(t, s.Home, "rev-parse", "HEAD"); got != head {
					t.Fatal("no-op edit committed")
				}
				files, _ := filepath.Glob(filepath.Join(s.ProjectDir(), ".envkit-edit-*"))
				if len(files) != 0 {
					t.Fatalf("successful edit leaked files: %v", files)
				}
			} else {
				files, _ := filepath.Glob(filepath.Join(s.ProjectDir(), ".envkit-edit-*"))
				if err == nil || len(files) != 1 || !strings.Contains(err.Error(), files[0]) {
					t.Fatalf("missing recoverable failure: %v %v", err, files)
				}
				info, statErr := os.Stat(files[0])
				if statErr != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("recovery permissions: %v %v", info, statErr)
				}
				if got := readFile(t, s.Path()); got != before {
					t.Fatal("invalid edit replaced original")
				}
				if got := runGit(t, s.Home, "rev-parse", "HEAD"); got != head {
					t.Fatal("invalid edit committed")
				}
			}
			info, err := os.Stat(s.Path())
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("edited permissions: %v %v", info, err)
			}
		})
	}
	s := testStore(t, "project")
	if err := s.Edit(nil); err == nil {
		t.Fatal("accepted empty editor")
	}
	if err := s.Edit([]string{"sh", "-c", ":", "editor"}); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, s.Home, "rev-list", "--count", "HEAD"); got != "1\n" {
		t.Fatalf("empty document commits = %q", got)
	}
}

// Each helper is a separate OS process, so these tests exercise flock, not Go mutexes.
func TestStoreProcessHelper(t *testing.T) {
	if os.Getenv("ENVKIT_STORE_HELPER") != "1" {
		return
	}
	s := NewForProject(os.Getenv("ENVKIT_TEST_HOME"), os.Getenv("ENVKIT_TEST_PROJECT"))
	if script := os.Getenv("ENVKIT_TEST_EDITOR"); script != "" {
		if err := s.Edit([]string{"sh", "-c", script, "editor"}); err != nil {
			t.Fatal(err)
		}
		return
	}
	id := os.Getenv("ENVKIT_TEST_ID")
	for i := range 3 {
		if err := s.Set("KEY_"+id+"_"+strconv.Itoa(i), "value", nil); err != nil {
			t.Fatal(err)
		}
	}
}

func storeProcess(t *testing.T, home, project, id, script string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestStoreProcessHelper$")
	cmd.Env = append(os.Environ(), "ENVKIT_STORE_HELPER=1", "ENVKIT_TEST_HOME="+home, "ENVKIT_TEST_PROJECT="+project, "ENVKIT_TEST_ID="+id, "ENVKIT_TEST_EDITOR="+script,
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	out := new(bytes.Buffer)
	cmd.Stdout, cmd.Stderr = out, out
	return cmd, out
}

func TestConcurrentProcessesKeepAllKeysAndAuditCommits(t *testing.T) {
	home := t.TempDir()
	type process struct {
		cmd    *exec.Cmd
		output *bytes.Buffer
	}
	var processes []process
	for i := range 8 {
		cmd, out := storeProcess(t, home, fmt.Sprintf("project%d", i%2), strconv.Itoa(i), "")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		processes = append(processes, process{cmd, out})
	}
	for _, p := range processes {
		if err := p.cmd.Wait(); err != nil {
			t.Fatalf("concurrent writer: %v\n%s", err, p.output)
		}
	}
	for i := range 2 {
		s := NewForProject(home, fmt.Sprintf("project%d", i))
		entries, err := s.Entries()
		if err != nil || len(entries) != 12 {
			t.Fatalf("project %d lost keys: %d %v", i, len(entries), err)
		}
	}
	if got := runGit(t, home, "rev-list", "--count", "HEAD"); got != "24\n" {
		t.Fatalf("audit commits = %q", got)
	}
	if got := runGit(t, home, "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("index dirty: %s", got)
	}
	if got := runGit(t, home, "ls-tree", "-r", "--name-only", "HEAD"); got != "project0/.env\nproject1/.env\n" {
		t.Fatalf("unexpected tracked files: %q", got)
	}
}

func TestEditHoldsVaultLockAcrossEditorAndCommit(t *testing.T) {
	s := testStore(t, "project")
	marker, release := filepath.Join(t.TempDir(), "editing"), filepath.Join(t.TempDir(), "release")
	script := fmt.Sprintf(`touch %q; while [ ! -f %q ]; do sleep 0.02; done; printf 'EDITED=value\n' >> "$1"`, marker, release)
	edit, editOut := storeProcess(t, s.Home, "project", "edit", script)
	if err := edit.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = edit.Process.Kill() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("editor never acquired lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	writer, writerOut := storeProcess(t, s.Home, "other", "writer", "")
	if err := writer.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Process.Kill() }()
	done := make(chan error, 1)
	go func() { done <- writer.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("writer bypassed editor lock: %v %s", err, writerOut)
	case <-time.After(200 * time.Millisecond):
	}
	writeFile(t, release, "release")
	if err := edit.Wait(); err != nil {
		t.Fatalf("editor: %v %s", err, editOut)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("writer: %v %s", err, writerOut)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("writer deadlocked after edit")
	}
	if value, found, err := s.Get("EDITED"); err != nil || !found || value != "value" {
		t.Fatalf("edit lost: %q %v %v", value, found, err)
	}
	if got := runGit(t, s.Home, "rev-list", "--count", "HEAD"); got != "4\n" {
		t.Fatalf("editor/writer audit count = %q", got)
	}
}
