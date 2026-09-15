package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/NarayanaSabari/envkit/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

func TestViewFitsTerminalAndUsesLazyGitStylePanes(t *testing.T) {
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

	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 170, Height: 50}} {
		t.Run(fmt.Sprintf("terminal-%dx%d", size.Width, size.Height), func(t *testing.T) {
			model := New(s)
			updated, _ := model.Update(size)
			model = updated.(Model)
			view := model.View()
			lines := strings.Split(ansi.Strip(view), "\n")
			if len(lines) != size.Height {
				t.Fatalf("line count = %d, want %d\n%s", len(lines), size.Height, view)
			}
			for i, line := range lines {
				if got := lipgloss.Width(line); got > size.Width {
					t.Fatalf("line %d width = %d, want <= %d\n%s", i, got, size.Width, view)
				}
			}
			if !strings.HasPrefix(lines[len(lines)-1], "tab") {
				t.Fatalf("key bar must be last and start with tab: %q", lines[len(lines)-1])
			}

			leftWidth := model.projectsWidth(size.Width)
			if !strings.HasPrefix(lines[0], "╭─ Projects ") {
				t.Fatalf("Projects title is not inline in its top border: %q", lines[0])
			}
			if got := string([]rune(lines[0])[leftWidth:]); !strings.HasPrefix(got, "╭─ Keys: narayana__envkit (3) ") {
				t.Fatalf("Keys title is not inline in its top border: %q", got)
			}

			headerIndex := -1
			for i, line := range lines {
				if strings.Contains(line, "KEY") && strings.Contains(line, "VALUE") {
					headerIndex = i
					break
				}
			}
			if headerIndex == -1 {
				t.Fatalf("table header not found\n%s", view)
			}
			header := string([]rune(lines[headerIndex])[leftWidth+1 : size.Width-1])
			if !strings.Contains(header, "KEY") || !strings.Contains(header, "VALUE") || !strings.Contains(header, "COMMENT") || !strings.Contains(header, "UPDATED") || strings.Contains(header, "│") {
				t.Fatalf("table header must contain plain columns without pipes: %q", header)
			}
			rule := string([]rune(lines[headerIndex+1])[leftWidth+1 : size.Width-1])
			if rule != strings.Repeat("─", size.Width-leftWidth-2) {
				t.Fatalf("table header rule must span the Keys inner width: %q", rule)
			}

			detailTop := -1
			for i, line := range lines {
				if strings.Contains(line, "╭─ Detail ") {
					detailTop = i
					break
				}
			}
			if detailTop == -1 {
				t.Fatalf("Detail title is not inline in its top border\n%s", view)
			}
			detailBottom := detailTop
			for detailBottom < len(lines) && !strings.Contains(string([]rune(lines[detailBottom])[leftWidth:]), "╰") {
				detailBottom++
			}
			if detailBottom == len(lines) {
				t.Fatalf("Detail pane has no bottom border\n%s", view)
			}
			if detailHeight := detailBottom - detailTop + 1; detailHeight > size.Height*2/5 {
				t.Fatalf("Detail height = %d, want <= 40%% of %d", detailHeight, size.Height)
			}
		})
	}
}

func TestKeyTableKeepsKeysAndUpdatedTimesIntact(t *testing.T) {
	s := store.NewForProject(t.TempDir(), "project")
	entries := []struct {
		key, value, comment string
		when                time.Time
	}{
		{"SHORT", "one", "", time.Now().Add(-12 * time.Minute)},
		{"MEDIUM_KEY12", "two", "fourteen chars", time.Now().Add(-13 * time.Minute)},
		{"LONGEST_KEY_NAME", "three", "thirty-three characters in comment!", time.Now().Add(-14 * time.Minute)},
	}
	for _, entry := range entries {
		date := entry.when.Format(time.RFC3339)
		t.Setenv("GIT_AUTHOR_DATE", date)
		t.Setenv("GIT_COMMITTER_DATE", date)
		comment := entry.comment
		if err := s.Set(entry.key, entry.value, &comment); err != nil {
			t.Fatal(err)
		}
	}

	updatedByKey := make(map[string]string, len(entries))
	for _, entry := range entries {
		history, err := s.KeyHistory(entry.key, "%ar")
		if err != nil {
			t.Fatal(err)
		}
		updatedByKey[entry.key] = strings.TrimSpace(strings.Split(history, "\n")[0])
	}

	for _, size := range []tea.WindowSizeMsg{
		{Width: 80, Height: 24}, {Width: 120, Height: 30},
		{Width: 170, Height: 50}, {Width: 200, Height: 60},
	} {
		t.Run(fmt.Sprintf("terminal-%dx%d", size.Width, size.Height), func(t *testing.T) {
			model := New(s)
			updated, _ := model.Update(size)
			model = updated.(Model)
			lines := strings.Split(ansi.Strip(model.View()), "\n")
			for i, line := range lines {
				if got := lipgloss.Width(line); got > size.Width {
					t.Fatalf("line %d width = %d, want <= %d\n%s", i, got, size.Width, model.View())
				}
			}
			for _, entry := range entries {
				var row string
				for _, line := range lines {
					if strings.Contains(line, entry.key) {
						row = line
						break
					}
				}
				if row == "" {
					t.Fatalf("row for %q not found\n%s", entry.key, model.View())
				}
				if !strings.Contains(row, entry.key) {
					t.Fatalf("key %q is truncated in row %q", entry.key, row)
				}
				if !strings.Contains(row, updatedByKey[entry.key]) {
					t.Fatalf("updated time %q is truncated in row %q", updatedByKey[entry.key], row)
				}
			}
		})
	}
}

func TestRevealExpandsValueColumnAndShowsDetailValue(t *testing.T) {
	s := store.NewForProject(t.TempDir(), "project")
	comment := "Selected secret comment"
	const value = "velvet_fb_1234567890abcdef"
	if err := s.Set("VELVET_TOKEN", value, &comment); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("NEXT_TOKEN", "other-secret", nil); err != nil {
		t.Fatal(err)
	}

	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 30}, {Width: 170, Height: 50}} {
		t.Run(fmt.Sprintf("terminal-%dx%d", size.Width, size.Height), func(t *testing.T) {
			model := New(s)
			updated, _ := model.Update(size)
			model = updated.(Model)
			updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
			model = updated.(Model)

			view := ansi.Strip(model.View())
			if !strings.Contains(view, value) {
				t.Fatalf("revealed value is truncated or missing\n%s", view)
			}
			detail := ansi.Strip(model.detail(model.contentWidth(size.Width-model.projectsWidth(size.Width)), size.Height))
			if !strings.Contains(detail, value) {
				t.Fatalf("Detail does not contain the complete revealed value\n%s", detail)
			}

			updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyTab})
			model = updated.(Model)
			updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
			model = updated.(Model)
			if view := ansi.Strip(model.View()); strings.Contains(view, value) {
				t.Fatalf("revealed value remains visible after changing rows\n%s", view)
			}
		})
	}
}

func TestHeaderRuleContainsVisibleCharacters(t *testing.T) {
	s := store.NewForProject(t.TempDir(), "project")
	if err := s.Set("TOKEN", "secret", nil); err != nil {
		t.Fatal(err)
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 80, Height: 24}, {Width: 170, Height: 50}} {
		t.Run(fmt.Sprintf("terminal-%dx%d", size.Width, size.Height), func(t *testing.T) {
			model := New(s)
			updated, _ := model.Update(size)
			model = updated.(Model)
			view := model.View()
			if !strings.Contains(view, "─") {
				t.Fatalf("View output does not literally contain a header rule\n%s", view)
			}
			lines := strings.Split(ansi.Strip(view), "\n")
			headerIndex := -1
			for i, line := range lines {
				if strings.Contains(line, "KEY") && strings.Contains(line, "VALUE") {
					headerIndex = i
					break
				}
			}
			if headerIndex == -1 || headerIndex+1 >= len(lines) || !strings.Contains(lines[headerIndex+1], "─") {
				t.Fatalf("table header rule line does not contain visible characters\n%s", view)
			}
		})
	}
}
