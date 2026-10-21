package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NarayanaSabari/envkit/internal/store"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func projectKey(m Model, key string) Model {
	return press(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
}

func projectModel(t *testing.T, names ...string) Model {
	t.Helper()
	home := t.TempDir()
	for _, name := range names {
		if err := newTestStore(t, home, name).Set("TOKEN", name+"-secret", nil); err != nil {
			t.Fatal(err)
		}
	}
	return New(newTestStore(t, home, names[0]))
}

func TestDeleteProjectRequiresConfirmationAndUsesHighlightedProject(t *testing.T) {
	m := projectModel(t, "alpha", "beta")
	m = projectKey(m, "j")
	m = projectKey(m, "d")
	if m.mode != promptDeleteProject || m.pendingKey != "beta" || !strings.Contains(m.keyBar(120), "ALL keys") {
		t.Fatalf("wrong project confirmation: %s", m.keyBar(120))
	}
	for _, key := range []string{"x", "q"} {
		m = projectKey(m, key)
		if m.mode != promptDeleteProject {
			t.Fatal("unrelated key dismissed confirmation")
		}
	}
	m = projectKey(m, "n")
	if _, err := os.Stat(filepath.Join(m.Store.Home, "beta", ".env")); err != nil {
		t.Fatalf("cancel removed project: %v", err)
	}
	m = projectKey(projectKey(m, "d"), "y")
	if _, err := os.Stat(filepath.Join(m.Store.Home, "beta")); !os.IsNotExist(err) {
		t.Fatalf("highlighted project not deleted: %v", err)
	}
	if m.Store.Project != "alpha" || len(m.keys) != 1 || m.keys[0].Value != "alpha-secret" {
		t.Fatal("deleting highlighted project changed loaded project's keys")
	}
	if !strings.Contains(m.keyBar(120), "deleted project beta") {
		t.Fatal("delete result not visible")
	}
}

func TestDeleteLoadedAndLastProjectDoesNotRecreateIt(t *testing.T) {
	m := projectModel(t, "alpha", "beta")
	m.reveal = true
	m = projectKey(projectKey(m, "d"), "y")
	if m.Store.Project != "beta" || m.reveal || len(m.keys) != 1 || m.keys[0].Value != "beta-secret" {
		t.Fatal("deleting loaded project did not safely select remaining project")
	}
	m = projectKey(projectKey(m, "d"), "y")
	for _, action := range []string{"r", "tab", "d", "e"} {
		m = projectKey(m, action)
	}
	if len(m.projects) != 0 || len(m.keys) != 0 || m.mode != promptNone {
		t.Fatal("last deletion left stale projects, keys, or prompts")
	}
	m = New(m.Store)
	if len(m.projects) != 0 || len(m.keys) != 0 {
		t.Fatal("reopening TUI resurrected project")
	}
	if _, err := os.Stat(m.Store.ProjectDir()); !os.IsNotExist(err) {
		t.Fatalf("last project recreated: %v", err)
	}
	if strings.Contains(m.View(), "beta-secret") {
		t.Fatal("deleted secret remains visible")
	}
}

func TestRenameProjectTargetsSelectionAndHandlesErrors(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "highlighted", true: "loaded"}[active], func(t *testing.T) {
			m := projectModel(t, "alpha", "beta")
			old := "alpha"
			if !active {
				m = projectKey(m, "j")
				old = "beta"
			}
			m.reveal = true
			m = projectKey(m, "e")
			if m.mode != promptRenameProject || m.input.Value() != old || m.reveal {
				t.Fatal("rename prompt wrong or secret still revealed")
			}
			m.input.SetValue("renamed")
			m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
			if _, err := os.Stat(filepath.Join(m.Store.Home, old)); !os.IsNotExist(err) {
				t.Fatalf("old project remains: %v", err)
			}
			selected, _ := m.selectedProject()
			if selected != "renamed" || (active && m.Store.Project != "renamed") || (!active && m.Store.Project != "alpha") {
				t.Fatal("rename lost highlighted or loaded selection")
			}
			m = projectKey(m, "e")
			m.input.SetValue("../escape")
			m = press(m, tea.KeyMsg{Type: tea.KeyEnter})
			if !strings.Contains(m.keyBar(150), "invalid project") {
				t.Fatalf("rename validation error hidden: %s", m.keyBar(150))
			}
			if _, err := os.Stat(filepath.Join(m.Store.Home, "renamed", ".env")); err != nil {
				t.Fatal("invalid rename damaged project")
			}
		})
	}
}

func TestProjectActionsDoNotChangeKeyPaneBindings(t *testing.T) {
	m := projectModel(t, "alpha")
	m.focused = paneKeys
	m = projectKey(m, "e")
	if m.mode != promptEditValue {
		t.Fatal("key edit became project rename")
	}
	m = press(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = projectKey(m, "d")
	if m.mode != promptConfirmDelete {
		t.Fatal("key delete became project delete")
	}
	m = projectKey(m, "y")
	if _, err := os.Stat(m.Store.Path()); err != nil || len(m.projects) != 1 {
		t.Fatalf("key delete removed project: %v", err)
	}
}

func TestProjectDetailShowsKnownDirectoriesForHighlightedProject(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	t.Setenv("ENVKIT_HOME", home)
	t.Setenv("ENVKIT_PROJECT", "linked")
	s, err := store.New(repo)
	if err != nil {
		t.Fatal(err)
	}
	legacy := newTestStore(t, home, "legacy")
	if err := s.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Ensure(); err != nil {
		t.Fatal(err)
	}
	secondRoot := t.TempDir()
	second, err := store.New(secondRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Ensure(); err != nil {
		t.Fatal(err)
	}
	m := New(s)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 240, Height: 50})
	m = updated.(Model)
	view := ansi.Strip(m.View())
	for _, path := range []string{repo, secondRoot, s.Path()} {
		if !strings.Contains(view, path) {
			t.Fatalf("project detail clipped %q:\n%s", path, view)
		}
	}
	if detail := m.detail(200, 30); !strings.Contains(detail, repo) || !strings.Contains(detail, "Linked directories:") || !strings.Contains(detail, s.Path()) {
		t.Fatalf("missing related directory or vault distinction: %s", detail)
	}
	m = applyFilter(m, "legacy")
	if detail := m.detail(200, 30); !strings.Contains(detail, "Unknown") || strings.Contains(detail, repo) {
		t.Fatalf("legacy project inferred wrong source directory: %s", detail)
	}
}

func TestAddKeyInEmptyVaultCreatesProjectOnlyAfterSave(t *testing.T) {
	s := newTestStore(t, t.TempDir(), "new-project")
	m := New(s)
	if len(m.projects) != 0 {
		t.Fatal("opening TUI created project")
	}
	m = projectKey(m, "a")
	m.completePrompt("TOKEN")
	m.completePrompt("new-secret")
	m.completePrompt("")
	if len(m.projects) != 1 || len(m.keys) != 1 || m.keys[0].Value != "new-secret" {
		t.Fatal("saving first key did not initialize project")
	}
}
