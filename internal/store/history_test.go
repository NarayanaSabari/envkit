package store

import (
	"os"
	"strings"
	"testing"
)

func TestKeyHistoriesMatchesIndividualHistory(t *testing.T) {
	s := testStore(t, "odd[*]")
	for _, value := range []string{"first-secret", "rotated-secret"} {
		if err := s.Set("KEY", value, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Comment("KEY", "KEY=comment, not an assignment change"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("KEY_SUFFIX", "KEY=not-a-change-to-KEY", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Unset("KEY"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("KEY", "recreated-secret", nil); err != nil {
		t.Fatal(err)
	}
	// A shared commit must appear once per changed key, even for replacements.
	if err := os.WriteFile(s.Path(), []byte("KEY=shared-secret\nKEY_SUFFIX=shared-secret\nEMPTY=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit("+PHANTOM=metadata is not a patch"); err != nil {
		t.Fatal(err)
	}
	other := NewForProject(s.Home, "other")
	if err := other.Set("KEY", "other-project-secret", nil); err != nil {
		t.Fatal(err)
	}
	histories, err := s.KeyHistories("%h  %ar  %s")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"KEY", "KEY_SUFFIX", "EMPTY"} {
		individual, err := s.KeyHistory(key, "%h  %ar  %s")
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(histories[key], "\n") + "\n"; got != individual {
			t.Errorf("history for %s = %q, want %q", key, got, individual)
		}
	}
	if len(histories) != 3 || len(histories["KEY"]) != 5 {
		t.Fatalf("unexpected keys or duplicate commits: %v", histories)
	}
	for _, history := range histories {
		for _, line := range history {
			if strings.Contains(line, "secret") {
				t.Fatalf("history exposed assignment value: %q", line)
			}
		}
	}
}

func TestKeyHistoriesEmptyAndErrors(t *testing.T) {
	s := testStore(t, "project")
	if err := s.Commit("empty document"); err != nil {
		t.Fatal(err)
	}
	histories, err := s.KeyHistories("%s")
	if err != nil || len(histories) != 0 {
		t.Fatalf("empty history = %v, %v", histories, err)
	}
	if _, err := NewForProject(s.Home, "../escape").KeyHistories("%s"); err == nil {
		t.Fatal("history accepted project traversal")
	}
	if _, err := testStore(t, "uncommitted").KeyHistories("%s"); err == nil {
		t.Fatal("history swallowed Git failure in empty repository")
	}
}
