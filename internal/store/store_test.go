package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSlugForms(t *testing.T) {
	t.Parallel()
	for _, remote := range []string{
		"https://github.com/owner/repo.git",
		"ssh://git@github.com/owner/repo.git",
		"git@github.com:owner/repo.git",
		"github-narayana:owner/repo.git",
	} {
		got, ok := Slug(remote)
		if !ok || got != "owner__repo" {
			t.Fatalf("Slug(%q) = %q, %v", remote, got, ok)
		}
	}
}

func TestParseWriteRoundTripPreservesCommentsAndOrder(t *testing.T) {
	input := "# first\nALPHA=one\n\n# second\nBETA=two\nOTHER=line\n"
	doc := Parse([]byte(input))
	if got := string(doc.Bytes()); got != input {
		t.Fatalf("round trip = %q, want %q", got, input)
	}
	entries := doc.Entries()
	if len(entries) != 3 || entries[0].Key != "ALPHA" || entries[0].Comment != "first" || entries[1].Key != "BETA" || entries[1].Comment != "second" || entries[2].Key != "OTHER" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

func TestSetExistingKeepsComment(t *testing.T) {
	doc := Parse([]byte("# existing\nTOKEN=old\nOTHER=yes\n"))
	doc.Set("TOKEN", "new", nil)
	want := "# existing\nTOKEN=new\nOTHER=yes\n"
	if got := string(doc.Bytes()); got != want {
		t.Fatalf("Set = %q, want %q", got, want)
	}
}

func TestUnsetRemovesKeyAndDirectComments(t *testing.T) {
	doc := Parse([]byte("KEEP=yes\n# remove one\n# remove two\nREMOVE=no\nAFTER=yes\n"))
	if !doc.Unset("REMOVE") {
		t.Fatal("Unset did not find key")
	}
	want := "KEEP=yes\nAFTER=yes\n"
	if got := string(doc.Bytes()); got != want {
		t.Fatalf("Unset = %q, want %q", got, want)
	}
}

func TestStoreMutations(t *testing.T) {
	home := t.TempDir()
	s := NewForProject(home, "project")
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	configureGit(t, home)
	comment := "token"
	if err := s.Set("TOKEN", "first", &comment); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("TOKEN", "second", nil); err != nil {
		t.Fatal(err)
	}
	entries, err := s.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Value != "second" || entries[0].Comment != "token" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
	if err := s.Unset("TOKEN"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "project", ".env")); err != nil {
		t.Fatal(err)
	}
}

func configureGit(t *testing.T, home string) {
	t.Helper()
	for _, args := range [][]string{{"config", "user.name", "test"}, {"config", "user.email", "test@example.invalid"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = home
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
}
