package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/NarayanaSabari/envkit/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

func TestViewFitsTerminalAndKeepsDetailFullWidth(t *testing.T) {
	s := store.NewForProject(t.TempDir(), "narayana__envkit")
	for _, entry := range []struct {
		key, value, comment string
	}{
		{"OPENAI_API_KEY", "openai-secret", "OpenAI API key"},
		{"DATABASE_URL", "postgres://example", "Primary database"},
		{"LONG_CONFIGURATION_KEY_NAME", "value", "A deliberately long comment for truncation"},
	} {
		comment := entry.comment
		if err := s.Set(entry.key, entry.value, &comment); err != nil {
			t.Fatal(err)
		}
	}

	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 120, Height: 40}} {
		t.Run(fmt.Sprintf("terminal-%dx%d", size.Width, size.Height), func(t *testing.T) {
			model := New(s)
			updated, _ := model.Update(size)
			view := updated.(Model).View()
			lines := strings.Split(view, "\n")
			if len(lines) != size.Height {
				t.Fatalf("line count = %d, want %d\n%s", len(lines), size.Height, view)
			}
			for i, line := range lines {
				if got := lipgloss.Width(line); got > size.Width {
					t.Fatalf("line %d width = %d, want <= %d\n%s", i, got, size.Width, line)
				}
			}
			var header string
			for _, line := range lines {
				if strings.Contains(line, "KEY") && strings.Contains(line, "VALUE") {
					header = line
					break
				}
			}
			if header == "" || !strings.Contains(header, "COMMENT") || !strings.Contains(header, "CHANGED") {
				t.Fatalf("table header is not on one line\n%s", view)
			}
			for i, line := range lines {
				if strings.Contains(line, "Detail") {
					if i == 0 || !strings.HasPrefix(lines[i-1], "╭") {
						t.Fatalf("Detail top border does not start at column 0: %q", lines[i-1])
					}
					return
				}
			}
			t.Fatalf("Detail title not found\n%s", view)
		})
	}
}
