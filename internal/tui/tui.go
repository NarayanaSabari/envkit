// Package tui implements envkit's terminal interface.
package tui

import (
	"fmt"
	"strings"

	"github.com/NarayanaSabari/envkit/internal/store"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	paneProjects = iota
	paneKeys
)

type promptMode int

const (
	promptNone promptMode = iota
	promptAddKey
	promptAddValue
	promptAddComment
	promptEditValue
	promptEditComment
	promptFilter
	promptConfirmDelete
)

var (
	focusedBorder = lipgloss.Color("62")
	mutedColor    = lipgloss.Color("241")
	accentColor   = lipgloss.Color("212")
	borderStyle   = lipgloss.RoundedBorder()
)

// Model is the state of the lazydocker-style envkit TUI.
type Model struct {
	Store         *store.Store
	projects      []string
	keys          []store.Entry
	projectCursor int
	keyCursor     int
	focused       int
	width         int
	height        int
	reveal        bool
	input         textinput.Model
	mode          promptMode
	pendingKey    string
	filter        string
	status        string
}

// New creates a model and loads the selected project without starting a terminal program.
func New(s *store.Store) Model {
	input := textinput.New()
	input.Prompt = ""
	m := Model{Store: s, input: input, width: 80, height: 24}
	m.refreshProjects()
	m.loadKeys()
	return m
}

// Run starts the interactive terminal UI.
func Run(s *store.Store) error {
	_, err := tea.NewProgram(New(s), tea.WithAltScreen()).Run()
	return err
}

func (m *Model) refreshProjects() {
	projects, err := m.Store.Projects()
	if err != nil {
		m.status = err.Error()
		return
	}
	m.projects = projects
	for i, project := range projects {
		if project == m.Store.Project {
			m.projectCursor = i
			break
		}
	}
	m.clampCursors()
}

func (m *Model) loadKeys() {
	keys, err := m.Store.Entries()
	if err != nil {
		m.status = err.Error()
		return
	}
	m.keys = keys
	m.clampCursors()
}

func (m *Model) clampCursors() {
	if len(m.projects) == 0 {
		m.projectCursor = 0
	} else if m.projectCursor >= len(m.projects) {
		m.projectCursor = len(m.projects) - 1
	}
	if len(m.filteredKeys()) == 0 {
		m.keyCursor = 0
	} else if m.keyCursor >= len(m.filteredKeys()) {
		m.keyCursor = len(m.filteredKeys()) - 1
	}
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = size.Width, size.Height
		return m, nil
	}
	if m.mode != promptNone {
		return m.updatePrompt(msg)
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "tab":
		m.focused = (m.focused + 1) % 2
	case "shift+tab":
		m.focused = (m.focused + 1) % 2
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "enter":
		if m.focused == paneProjects && len(m.filteredProjects()) > 0 {
			m.Store = store.NewForProject(m.Store.Home, m.filteredProjects()[m.projectCursor])
			m.filter = ""
			m.keyCursor = 0
			m.loadKeys()
		}
	case "a":
		m.startPrompt(promptAddKey, "key")
	case "e":
		if entry, ok := m.selectedKey(); ok {
			m.pendingKey = entry.Key
			m.startPrompt(promptEditValue, "new value", true)
		}
	case "c":
		if entry, ok := m.selectedKey(); ok {
			m.pendingKey = entry.Key
			m.startPrompt(promptEditComment, "comment")
		}
	case "d":
		if entry, ok := m.selectedKey(); ok {
			m.pendingKey = entry.Key
			m.mode = promptConfirmDelete
			m.status = fmt.Sprintf("Delete %s? (y/n)", entry.Key)
		}
	case "v":
		if _, ok := m.selectedKey(); ok {
			m.reveal = !m.reveal
		}
	case "/":
		m.startPrompt(promptFilter, "filter")
	case "r":
		m.refreshProjects()
		m.loadKeys()
		m.status = "refreshed"
	}
	return m, nil
}

func (m Model) updatePrompt(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if m.mode == promptConfirmDelete {
		if !isKey {
			return m, nil
		}
		switch key.String() {
		case "y":
			if err := m.Store.Unset(m.pendingKey); err != nil {
				m.status = err.Error()
			} else {
				m.status = "deleted " + m.pendingKey
				m.loadKeys()
			}
		case "n", "esc":
			m.status = "cancelled"
		}
		m.mode = promptNone
		return m, nil
	}
	if isKey && key.String() == "esc" {
		m.mode = promptNone
		m.input.Blur()
		m.status = "cancelled"
		return m, nil
	}
	if isKey && key.String() == "enter" {
		value := m.input.Value()
		m.input.Reset()
		m.input.Blur()
		m.completePrompt(value)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) completePrompt(value string) {
	switch m.mode {
	case promptAddKey:
		if err := store.ValidateKey(value); err != nil {
			m.status = err.Error()
			m.mode = promptNone
			return
		}
		m.pendingKey = value
		m.startPrompt(promptAddValue, "value", true)
		return
	case promptAddValue:
		m.pendingKey += "\x00" + value
		m.startPrompt(promptAddComment, "comment (optional)")
		return
	case promptAddComment:
		key, secret, _ := strings.Cut(m.pendingKey, "\x00")
		var comment *string
		if value != "" {
			comment = &value
		}
		if err := m.Store.Set(key, secret, comment); err != nil {
			m.status = err.Error()
		} else {
			m.status = "saved " + key
			m.loadKeys()
		}
	case promptEditValue:
		if err := m.Store.Set(m.pendingKey, value, nil); err != nil {
			m.status = err.Error()
		} else {
			m.status = "updated " + m.pendingKey
			m.loadKeys()
		}
	case promptEditComment:
		if err := m.Store.Comment(m.pendingKey, value); err != nil {
			m.status = err.Error()
		} else {
			m.status = "commented " + m.pendingKey
			m.loadKeys()
		}
	case promptFilter:
		m.filter = value
		m.clampCursors()
		m.status = "filter: " + value
	}
	m.mode = promptNone
	m.pendingKey = ""
}

func (m *Model) startPrompt(mode promptMode, placeholder string, password ...bool) {
	m.mode = mode
	m.input.Reset()
	m.input.Placeholder = placeholder
	m.input.Prompt = placeholder + ": "
	m.input.EchoMode = textinput.EchoNormal
	if len(password) > 0 && password[0] {
		m.input.EchoMode = textinput.EchoPassword
		m.input.EchoCharacter = '•'
	}
	m.input.Focus()
}

func (m *Model) move(delta int) {
	m.reveal = false
	if m.focused == paneProjects {
		items := m.filteredProjects()
		if len(items) > 0 {
			m.projectCursor = (m.projectCursor + delta + len(items)) % len(items)
		}
		return
	}
	items := m.filteredKeys()
	if len(items) > 0 {
		m.keyCursor = (m.keyCursor + delta + len(items)) % len(items)
	}
}

func (m Model) filteredProjects() []string {
	if m.filter == "" || m.focused != paneProjects {
		return m.projects
	}
	return filterStrings(m.projects, m.filter)
}

func (m Model) filteredKeys() []store.Entry {
	if m.filter == "" || m.focused != paneKeys {
		return m.keys
	}
	needle := strings.ToLower(m.filter)
	var filtered []store.Entry
	for _, entry := range m.keys {
		if strings.Contains(strings.ToLower(entry.Key+" "+entry.Comment), needle) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func filterStrings(items []string, filter string) []string {
	needle := strings.ToLower(filter)
	var result []string
	for _, item := range items {
		if strings.Contains(strings.ToLower(item), needle) {
			result = append(result, item)
		}
	}
	return result
}

func (m Model) selectedKey() (store.Entry, bool) {
	keys := m.filteredKeys()
	if m.keyCursor < 0 || m.keyCursor >= len(keys) {
		return store.Entry{}, false
	}
	return keys[m.keyCursor], true
}

func (m Model) View() string {
	width := max(1, m.width)
	bodyHeight := max(1, m.height-1)
	leftWidth := min(26, max(1, width/4))
	rightWidth := max(1, width-leftWidth)
	topHeight := max(1, bodyHeight*3/5)
	detailHeight := max(1, bodyHeight-topHeight)
	projects := m.pane("Projects", m.projectList(m.contentWidth(leftWidth)), leftWidth, topHeight, m.focused == paneProjects)
	keys := m.pane("Keys: "+m.Store.Project, m.keyTable(m.contentWidth(rightWidth), max(3, topHeight-4)), rightWidth, topHeight, m.focused == paneKeys)
	top := lipgloss.JoinHorizontal(lipgloss.Top, projects, keys)
	detail := m.pane("Detail", m.detail(m.contentWidth(width)), width, detailHeight, false)
	body := lipgloss.JoinVertical(lipgloss.Left, top, detail)
	status := "tab panes  j/k move  enter load  a add  e value  c comment  d delete  v reveal  / filter  r refresh  q quit"
	if m.status != "" {
		status = m.status + "  |  " + status
	}
	if m.mode != promptNone && m.mode != promptConfirmDelete {
		status = m.input.View() + "  esc cancel"
	}
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("236")).Width(width)
	return body + "\n" + statusStyle.Render(truncate(status, width))
}

func (m Model) pane(title, content string, width, height int, focused bool) string {
	border := lipgloss.NewStyle().Foreground(mutedColor)
	if focused {
		border = lipgloss.NewStyle().Foreground(focusedBorder)
	}
	title = truncate(title, m.contentWidth(width))
	return border.Border(borderStyle).Width(max(1, width-2)).Height(max(1, height-2)).Padding(0, 1).Render(lipgloss.NewStyle().Bold(true).Foreground(accentColor).Render(title) + "\n" + content)
}

func (m Model) contentWidth(paneWidth int) int { return max(1, paneWidth-4) }

func (m Model) projectList(width int) string {
	items := m.filteredProjects()
	if len(items) == 0 {
		return muted("No projects")
	}
	lines := make([]string, 0, len(items))
	for i, project := range items {
		prefix := "  "
		if i == m.projectCursor {
			prefix = "> "
		}
		lines = append(lines, truncate(prefix+project, width))
	}
	return strings.Join(lines, "\n")
}

func (m Model) keyTable(width, height int) string {
	entries := m.filteredKeys()
	if len(entries) == 0 {
		return muted("No keys")
	}

	updated := make([]string, len(entries))
	updatedWidth := lipgloss.Width("UPDATED")
	keyWidth := 8
	for i, entry := range entries {
		changed, _ := m.Store.KeyHistory(entry.Key, "%ar")
		updated[i] = strings.TrimSpace(strings.Split(changed, "\n")[0])
		updatedWidth = max(updatedWidth, lipgloss.Width(updated[i]))
		keyWidth = max(keyWidth, lipgloss.Width(entry.Key))
	}
	keyWidth = min(keyWidth, 40)
	updatedWidth = min(updatedWidth, 16)

	const (
		valueWidth        = 10
		maximumTableWidth = 90
	)
	// A compact maximum keeps the table close to the left edge of its pane on
	// very wide terminals, instead of spreading its columns across the screen.
	width = min(width, maximumTableWidth)
	// Three separator columns make the boundaries part of the actual table,
	// rather than relying on padding that grows with the terminal width.
	fixedWidth := valueWidth + updatedWidth + 3
	keyWidth = min(keyWidth, max(1, width-fixedWidth-lipgloss.Width("COMMENT")))
	commentWidth := min(60, max(1, width-fixedWidth-keyWidth))

	columns := []table.Column{
		{Title: "KEY", Width: keyWidth},
		{Title: "│", Width: 1},
		{Title: "VALUE", Width: valueWidth},
		{Title: "│", Width: 1},
		{Title: "COMMENT", Width: commentWidth},
		{Title: "│", Width: 1},
		{Title: fmt.Sprintf("%*s", updatedWidth, "UPDATED"), Width: updatedWidth},
	}
	rows := make([]table.Row, 0, len(entries))
	for i, entry := range entries {
		value := "••••••••"
		if i == m.keyCursor && m.reveal {
			value = entry.Value
		}
		rows = append(rows, table.Row{
			truncate(entry.Key, keyWidth),
			"│",
			truncate(value, valueWidth),
			"│",
			truncate(strings.ReplaceAll(entry.Comment, "\n", " "), commentWidth),
			"│",
			fmt.Sprintf("%*s", updatedWidth, truncate(updated[i], updatedWidth)),
		})
	}

	styles := table.DefaultStyles()
	styles.Header = lipgloss.NewStyle().Bold(true).BorderBottom(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(mutedColor)
	styles.Cell = lipgloss.NewStyle()
	styles.Selected = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("57"))
	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithWidth(keyWidth+valueWidth+commentWidth+updatedWidth+3),
		table.WithHeight(height),
		table.WithStyles(styles),
	)
	t.SetCursor(m.keyCursor)
	return t.View()
}

func (m Model) detail(width int) string {
	entry, ok := m.selectedKey()
	if !ok {
		return muted("Select a key to inspect its comment and history")
	}
	history, err := m.Store.KeyHistory(entry.Key, "%h  %ar  %s")
	if err != nil {
		history = err.Error()
	}
	comment := entry.Comment
	if comment == "" {
		comment = "(no comment)"
	}
	lines := []string{"Key: " + entry.Key, "Comment: " + strings.ReplaceAll(comment, "\n", " "), "", "History:"}
	for _, line := range strings.Split(strings.TrimSpace(history), "\n") {
		if line == "" {
			continue
		}
		lines = append(lines, strings.Replace(line, m.Store.Project+": ", "", 1))
	}
	for i, line := range lines {
		lines[i] = truncate(line, width)
	}
	return strings.Join(lines, "\n")
}

func muted(text string) string { return lipgloss.NewStyle().Foreground(mutedColor).Render(text) }

func truncate(text string, length int) string {
	if length <= 0 || lipgloss.Width(text) <= length {
		return text
	}
	if length == 1 {
		return "…"
	}
	var b strings.Builder
	for _, r := range text {
		if lipgloss.Width(b.String()+string(r)+"…") > length {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + "…"
}
