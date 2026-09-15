package tui

import (
	"os"
	"testing"

	"github.com/NarayanaSabari/envkit/internal/store"
	tea "github.com/charmbracelet/bubbletea"
)

func TestModelRendersAndMovesCursor(t *testing.T) {
	s := store.NewForProject(t.TempDir(), "project")
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Path(), []byte("ONE=one\nTWO=two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	model := New(s)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model = updated.(Model)
	if got := model.View(); got == "" {
		t.Fatal("View returned empty output")
	}
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	model = updated.(Model)
	if model.keyCursor != 1 {
		t.Fatalf("key cursor = %d, want 1", model.keyCursor)
	}
}
