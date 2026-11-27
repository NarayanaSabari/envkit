package vault

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const project = "software-factory"

func history(t *testing.T, home string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", home, "log", "--format=%s").Output()
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

func TestSetThenGet(t *testing.T) {
	home := t.TempDir()
	v, err := Open(home, project)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	before := len(history(t, home))
	if before == 1 && history(t, home)[0] == "" {
		before = 0
	}
	if err := v.Set("FAKE_TRACKER_TOKEN", "fake-tracker-key", "The key of the source fake-tickets."); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, ok, err := v.Get("FAKE_TRACKER_TOKEN")
	if err != nil || !ok || got != "fake-tracker-key" {
		t.Fatalf("FAKE_TRACKER_TOKEN read back wrong: ok=%v err=%v", ok, err)
	}
	if _, ok, err := v.Get("FAKE_CODE_TOKEN"); err != nil || ok {
		t.Fatalf("FAKE_CODE_TOKEN: ok=%v err=%v, want unset", ok, err)
	}
	data, err := os.ReadFile(filepath.Join(home, project, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# The key of the source fake-tickets.") {
		t.Fatal("the file does not hold the comment")
	}
	info, err := os.Stat(filepath.Join(home, project, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v, want 0600", info.Mode().Perm())
	}
	log := history(t, home)
	if len(log)-before != 1 {
		t.Fatalf("history grew by %d commits, want 1", len(log)-before)
	}
	if !strings.Contains(log[0], "FAKE_TRACKER_TOKEN") || strings.Contains(log[0], "fake-tracker-key") {
		t.Fatal("the new commit must name the key and not hold the value")
	}
}

func TestRefused(t *testing.T) {
	home := t.TempDir()
	v, err := Open(home, project)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, c := range []struct{ key, value string }{
		{"1BAD-KEY", "fake-secret-one"},
		{"FAKE_KEY", "fake-secret\ntwo"},
		{"FAKE_KEY", "fake-secret\rthree"},
		{"FAKE_KEY", "fake-secret\x00four"},
	} {
		err := v.Set(c.key, c.value, "x")
		if err == nil {
			t.Fatalf("set %q: no error", c.key)
		}
		if strings.Contains(err.Error(), "fake-secret") {
			t.Fatalf("set %q: the error holds the value", c.key)
		}
	}
	if _, ok, _ := v.Get("FAKE_KEY"); ok {
		t.Fatal("a refused value was written")
	}
	for _, p := range []string{"", ".", "..", ".git", "a/b"} {
		if _, err := Open(home, p); err == nil {
			t.Fatalf("project %q opened", p)
		}
	}
}

func TestEmptyHomeIsEnvkitHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ENVKIT_HOME", home)
	v, err := Open("", project)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := v.Set("FAKE_CODE_TOKEN", "fake-code-key", "c"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, project, ".env")); err != nil {
		t.Fatal("the key is not in ENVKIT_HOME")
	}
}
