package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NarayanaSabari/envkit/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func newTestStore(t *testing.T, home, project string) *store.Store {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "envkit test")
	t.Setenv("GIT_AUTHOR_EMAIL", "envkit-test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "envkit test")
	t.Setenv("GIT_COMMITTER_EMAIL", "envkit-test@example.com")
	return store.NewForProject(home, project)
}

func TestModelRendersAndMovesCursor(t *testing.T) {
	s := newTestStore(t, t.TempDir(), "project")
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
	s := newTestStore(t, t.TempDir(), "narayana__envkit")
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
	s := newTestStore(t, t.TempDir(), "project")
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
	s := newTestStore(t, t.TempDir(), "project")
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
			model.focused = paneKeys
			updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
			model = updated.(Model)

			view := ansi.Strip(model.View())
			if !strings.Contains(view, value) {
				t.Fatalf("revealed value is truncated or missing\n%s", view)
			}
			var row string
			for _, line := range strings.Split(view, "\n") {
				if strings.Contains(line, "VELVET_TOKEN") && strings.Contains(line, value) {
					row = line
					break
				}
			}
			if row == "" {
				t.Fatalf("row for VELVET_TOKEN not found\n%s", view)
			}
			if !strings.Contains(row, value+"  "+comment) {
				t.Fatalf("revealed value is not followed by a two-space gutter before the comment: %q", row)
			}
			detail := ansi.Strip(model.detail(model.contentWidth(size.Width-model.projectsWidth(size.Width)), size.Height))
			if !strings.Contains(detail, value) {
				t.Fatalf("Detail does not contain the complete revealed value\n%s", detail)
			}
			if !strings.Contains(detail, "set VELVET_TOKEN") {
				t.Fatalf("Detail does not contain the history subject\n%s", detail)
			}
			if strings.Contains(detail, s.Project+": set VELVET_TOKEN") {
				t.Fatalf("Detail history subject still contains the project prefix\n%s", detail)
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
	s := newTestStore(t, t.TempDir(), "project")
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

func press(model Model, key tea.KeyMsg) Model {
	updated, _ := model.Update(key)
	return updated.(Model)
}

func applyFilter(model Model, value string) Model {
	model = press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	model.input.SetValue(value)
	return press(model, tea.KeyMsg{Type: tea.KeyEnter})
}

func TestProjectFilterClampsCursorBeforeLoading(t *testing.T) {
	home := t.TempDir()
	for _, project := range []string{"alpha", "beta", "gamma"} {
		if err := newTestStore(t, home, project).Ensure(); err != nil {
			t.Fatal(err)
		}
	}
	model := New(newTestStore(t, home, "gamma"))
	if model.projectCursor != 2 {
		t.Fatalf("project cursor = %d, want 2", model.projectCursor)
	}
	model = applyFilter(model, "alpha")
	if model.projectCursor != 0 {
		t.Fatalf("filtered project cursor = %d, want 0", model.projectCursor)
	}
	model = press(model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.Store.Project != "alpha" {
		t.Fatalf("loaded project = %q, want alpha", model.Store.Project)
	}
	for _, cursor := range []int{-1, 3} {
		model.projectCursor = cursor
		model = press(model, tea.KeyMsg{Type: tea.KeyEnter})
		if model.Store.Project != "alpha" {
			t.Fatalf("invalid cursor loaded project %q", model.Store.Project)
		}
	}
}

func TestClampCursorsUsesFocusedFilteredLists(t *testing.T) {
	for _, focused := range []int{paneProjects, paneKeys} {
		for _, cursor := range []int{-1, 2} {
			model := Model{
				projects: []string{"alpha", "beta", "gamma"},
				keys:     []store.Entry{{Key: "ALPHA"}, {Key: "BETA"}, {Key: "GAMMA"}},
				focused:  focused, filter: "alpha", projectCursor: cursor, keyCursor: cursor,
			}
			model.clampCursors()
			wantProject, wantKey := max(0, cursor), max(0, cursor)
			if focused == paneProjects {
				wantProject = 0
			} else {
				wantKey = 0
			}
			if model.projectCursor != wantProject || model.keyCursor != wantKey {
				t.Fatalf("focused %d cursors = %d/%d, want %d/%d", focused, model.projectCursor, model.keyCursor, wantProject, wantKey)
			}
			model = press(model, tea.KeyMsg{Type: tea.KeyTab})
			if model.projectCursor != 0 || model.keyCursor != 0 {
				t.Fatalf("focus change left cursors = %d/%d", model.projectCursor, model.keyCursor)
			}
			model.mode = promptFilter
			model.completePrompt("absent")
			if model.projectCursor != 0 || model.keyCursor != 0 {
				t.Fatalf("empty filter left cursors = %d/%d", model.projectCursor, model.keyCursor)
			}
		}
	}
}

func TestRevealResetsOnSelectionChanges(t *testing.T) {
	home := t.TempDir()
	s := newTestStore(t, home, "alpha")
	for _, entry := range []store.Entry{{Key: "A", Value: "first-secret"}, {Key: "B", Value: "second-secret"}} {
		if err := s.Set(entry.Key, entry.Value, nil); err != nil {
			t.Fatal(err)
		}
	}
	other := newTestStore(t, home, "beta")
	if err := other.Set("B", "other-project-secret", nil); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"filter", "focus", "project", "load", "refresh", "projects"} {
		t.Run(action, func(t *testing.T) {
			model := New(s)
			model.focused = paneKeys
			model = applyFilter(model, "A")
			model = press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
			if !model.reveal || !strings.Contains(model.View(), "first-secret") {
				t.Fatal("selected A was not revealed")
			}
			switch action {
			case "filter":
				model = applyFilter(model, "B")
				if entry, ok := model.selectedKey(); !ok || entry.Key != "B" {
					t.Fatalf("selected key after filtering = %+v, %v", entry, ok)
				}
			case "focus":
				model = press(model, tea.KeyMsg{Type: tea.KeyShiftTab})
			case "project":
				model.focused = paneProjects
				model.filter = ""
				model.projectCursor = 1
				model = press(model, tea.KeyMsg{Type: tea.KeyEnter})
				if model.Store.Project != "beta" {
					t.Fatalf("loaded project = %q, want beta", model.Store.Project)
				}
			case "load":
				model.loadKeys()
			case "refresh":
				model = press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
			case "projects":
				model.refreshProjects()
			}
			if model.reveal {
				t.Fatal("selection change preserved reveal")
			}
			view := model.View()
			for _, secret := range []string{"first-secret", "second-secret", "other-project-secret"} {
				if strings.Contains(view, secret) {
					t.Fatalf("selection change exposed %q", secret)
				}
			}
		})
	}
}

func TestViewAndNavigationUseCachedHistoryWithoutGitOrWrites(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Git recorder uses a POSIX shell")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	calls := filepath.Join(bin, "calls")
	t.Setenv("ENVKIT_TEST_GIT", realGit)
	t.Setenv("ENVKIT_TEST_GIT_CALLS", calls)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$ENVKIT_TEST_GIT_CALLS\"\nexec \"$ENVKIT_TEST_GIT\" \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	s := newTestStore(t, t.TempDir(), "project")
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	var contents strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&contents, "KEY_%03d=secret\n", i)
	}
	if err := os.WriteFile(s.Path(), []byte(contents.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit("project: initial keys"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(calls, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	model := New(s)
	model.focused = paneKeys
	loadedCalls, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(loadedCalls), "log --format="); got != 1 {
		t.Fatalf("history calls during load = %d, want 1", got)
	}
	if err := os.RemoveAll(s.ProjectDir()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		view := model.View()
		if !strings.Contains(view, "initial keys") {
			t.Fatalf("cached history missing from detail\n%s", view)
		}
		model = press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		model = applyFilter(model, "KEY_00")
	}
	afterCalls, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterCalls) != string(loadedCalls) {
		t.Fatalf("render or navigation invoked Git:\n%s", afterCalls[len(loadedCalls):])
	}
	if _, err := os.Stat(s.ProjectDir()); !os.IsNotExist(err) {
		t.Fatalf("render or navigation recreated project directory: %v", err)
	}
}

func TestHistoryCacheReloadsOnRefreshAndMutation(t *testing.T) {
	s := newTestStore(t, t.TempDir(), "project")
	if err := s.Set("TOKEN", "secret", nil); err != nil {
		t.Fatal(err)
	}
	model := New(s)
	initial, err := model.selectedKeyHistory()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("TOKEN", "rotated-secret", nil); err != nil {
		t.Fatal(err)
	}
	cached, _ := model.selectedKeyHistory()
	if !reflect.DeepEqual(cached, initial) {
		t.Fatal("history changed without reload")
	}
	model = press(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	refreshed, err := model.selectedKeyHistory()
	if err != nil || len(refreshed) != len(initial)+1 {
		t.Fatalf("refreshed history = %v, %v", refreshed, err)
	}
	model.pendingKey = "TOKEN"
	model.mode = promptEditValue
	model.completePrompt("updated-secret")
	mutated, err := model.selectedKeyHistory()
	if err != nil || len(mutated) != len(refreshed)+1 {
		t.Fatalf("history after edit = %v, %v", mutated, err)
	}
}

func TestWrapHandlesUnicodeAndNarrowWidths(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		width      int
		want       []string
	}{
		{"wide rune in one cell", "界", 1, []string{"", "界"}},
		{"wide runes", "界界", 2, []string{"界", "界"}},
		{"combining marks", "e\u0301e\u0301", 1, []string{"e\u0301", "e\u0301"}},
		{"ansi", "\x1b[31m界a\x1b[0m", 2, []string{"\x1b[31m界", "a\x1b[0m"}},
		{"spaces and newlines", "a b\nc", 2, []string{"a ", "b", "c"}},
		{"zero width", "界", 0, []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := wrap(tc.text, tc.width); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("wrap(%q, %d) = %q, want %q", tc.text, tc.width, got, tc.want)
			}
		})
	}
}
