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
	focusedBorder = lipgloss.Color("141")
	mutedColor    = lipgloss.Color("240")
	accentColor   = lipgloss.Color("141")
	normalColor   = lipgloss.Color("250")
	commentColor  = lipgloss.Color("246")
	selectedBG    = lipgloss.Color("60")
	inactiveBG    = lipgloss.Color("238")
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
	m.input.Prompt = promptLabel(mode, m.pendingKey, placeholder)
	m.input.EchoMode = textinput.EchoNormal
	if len(password) > 0 && password[0] {
		m.input.EchoMode = textinput.EchoPassword
		m.input.EchoCharacter = '•'
	}
	m.input.Focus()
}

func promptLabel(mode promptMode, key, placeholder string) string {
	switch mode {
	case promptAddKey:
		return "Key: "
	case promptAddValue:
		return "Value for " + key + ": "
	case promptAddComment:
		key, _, _ = strings.Cut(key, "\x00")
		return "Comment for " + key + ": "
	case promptEditValue:
		return "Value for " + key + ": "
	case promptEditComment:
		return "Comment for " + key + ": "
	case promptFilter:
		return "Filter: "
	default:
		return placeholder + ": "
	}
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
	leftWidth := m.projectsWidth(width)
	rightWidth := max(1, width-leftWidth)
	detailHeight := m.detailHeight(bodyHeight)
	keysHeight := max(3, bodyHeight-detailHeight)
	projects := m.pane("Projects", m.projectList(m.contentWidth(leftWidth)), leftWidth, bodyHeight, m.focused == paneProjects)
	keys := m.pane(
		fmt.Sprintf("Keys: %s (%d)", m.Store.Project, len(m.filteredKeys())),
		m.keyTable(m.contentWidth(rightWidth), max(1, keysHeight-2)),
		rightWidth,
		keysHeight,
		m.focused == paneKeys,
	)
	detail := m.pane("Detail", m.detail(m.contentWidth(rightWidth), max(1, detailHeight-2)), rightWidth, detailHeight, false)
	right := lipgloss.JoinVertical(lipgloss.Left, keys, detail)
	body := lipgloss.JoinHorizontal(lipgloss.Top, projects, right)
	return body + "\n" + m.keyBar(width)
}

func (m Model) projectsWidth(width int) int {
	longest := 0
	for _, project := range m.projects {
		longest = max(longest, lipgloss.Width(project))
	}
	ideal := max(24, longest+4)
	cap := max(1, width*2/5)
	return min(max(1, width-1), min(ideal, cap))
}

func (m Model) detailHeight(bodyHeight int) int {
	maxHeight := max(1, m.height*2/5)
	history, _ := m.selectedKeyHistory()
	desired := 7 + len(history)
	return min(bodyHeight-3, min(maxHeight, max(8, desired)))
}

func (m Model) keyBar(width int) string {
	if m.mode == promptConfirmDelete {
		return padRight("Delete "+m.pendingKey+"? y/n", width)
	}
	if m.mode != promptNone {
		return padRight(m.input.View()+"  esc cancel", width)
	}

	items := []struct{ key, label string }{
		{"tab", "panes"}, {"j/k", "move"}, {"enter", "load"}, {"a", "add"},
		{"e", "value"}, {"c", "comment"}, {"d", "delete"}, {"v", "reveal"},
		{"/", "filter"}, {"r", "refresh"}, {"q", "quit"},
	}
	keyStyle := lipgloss.NewStyle().Bold(true).Foreground(accentColor)
	labelStyle := lipgloss.NewStyle().Foreground(commentColor)
	parts := make([]string, 0, len(items))
	for _, item := range items {
		part := keyStyle.Render(item.key) + " " + labelStyle.Render(item.label)
		if lipgloss.Width(strings.Join(parts, "  ")+"  "+part) > width {
			break
		}
		parts = append(parts, part)
	}
	return lipgloss.NewStyle().Background(lipgloss.Color("236")).Render(padRight(strings.Join(parts, "  "), width))
}

func (m Model) pane(title, content string, width, height int, focused bool) string {
	width = max(3, width)
	height = max(3, height)
	inner := width - 2
	borderColor := mutedColor
	titleColor := mutedColor
	if focused {
		borderColor = focusedBorder
		titleColor = accentColor
	}
	border := lipgloss.NewStyle().Foreground(borderColor)
	title = truncate(title, max(1, inner-4))
	titleText := " " + title + " "
	topFill := max(0, inner-lipgloss.Width(titleText)-1)
	top := border.Render("╭─") + lipgloss.NewStyle().Bold(true).Foreground(titleColor).Render(titleText) + border.Render(strings.Repeat("─", topFill)+"╮")

	contentLines := strings.Split(content, "\n")
	lines := []string{top}
	for i := 0; i < height-2; i++ {
		line := ""
		if i < len(contentLines) {
			line = contentLines[i]
		}
		lines = append(lines, border.Render("│")+padRight(truncate(line, inner), inner)+border.Render("│"))
	}
	lines = append(lines, border.Render("╰")+border.Render(strings.Repeat("─", inner))+border.Render("╯"))
	return strings.Join(lines, "\n")
}

func (m Model) contentWidth(paneWidth int) int { return max(1, paneWidth-2) }

func (m Model) projectList(width int) string {
	items := m.filteredProjects()
	if len(items) == 0 {
		return muted("no projects")
	}
	lines := make([]string, 0, len(items))
	for i, project := range items {
		style := lipgloss.NewStyle().Foreground(normalColor)
		if i == m.projectCursor {
			background := inactiveBG
			if m.focused == paneProjects {
				background = selectedBG
			}
			style = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255")).Background(background)
		}
		lines = append(lines, style.Render(padRight(truncate(project, width), width)))
	}
	return strings.Join(lines, "\n")
}

func (m Model) keyTable(width, height int) string {
	entries := m.filteredKeys()
	if len(entries) == 0 {
		return lipgloss.NewStyle().Foreground(mutedColor).Width(width).Height(max(1, height)).Align(lipgloss.Center, lipgloss.Center).Render("no keys yet - press a to add")
	}

	updated := make([]string, len(entries))
	keyWidth := 12
	for i, entry := range entries {
		changed, _ := m.Store.KeyHistory(entry.Key, "%ar")
		updated[i] = strings.TrimSpace(strings.Split(changed, "\n")[0])
		keyWidth = max(keyWidth, lipgloss.Width(entry.Key))
	}
	keyWidth = min(keyWidth, 40)
	const valueWidth = 12
	const updatedWidth = 16
	const minimumCommentWidth = len("COMMENT")
	// The first three columns reserve two trailing spaces as gutters. The last
	// column is right aligned and takes the pane's remaining width exactly.
	keyWidth = min(keyWidth, max(3, width-valueWidth-updatedWidth-6-minimumCommentWidth))
	commentWidth := max(1, width-(keyWidth+2)-(valueWidth+2)-updatedWidth-2)

	columns := []table.Column{
		{Title: "KEY", Width: keyWidth + 2},
		{Title: "VALUE", Width: valueWidth + 2},
		{Title: "COMMENT", Width: commentWidth + 2},
		{Title: fmt.Sprintf("%*s", updatedWidth, "UPDATED"), Width: updatedWidth},
	}
	rows := make([]table.Row, 0, len(entries))
	for i, entry := range entries {
		value := "••••••••"
		if i == m.keyCursor && m.reveal {
			value = entry.Value
		}
		selected := i == m.keyCursor
		keyStyle := lipgloss.NewStyle().Foreground(normalColor)
		valueStyle := lipgloss.NewStyle().Foreground(mutedColor)
		commentStyle := lipgloss.NewStyle().Foreground(commentColor)
		updatedStyle := lipgloss.NewStyle().Foreground(mutedColor)
		if selected {
			keyStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
			valueStyle = keyStyle
			commentStyle = keyStyle
			updatedStyle = keyStyle
		}
		rows = append(rows, table.Row{
			keyStyle.Render(truncate(entry.Key, keyWidth)),
			valueStyle.Render(truncate(value, valueWidth)),
			commentStyle.Render(truncate(strings.ReplaceAll(entry.Comment, "\n", " "), commentWidth)),
			updatedStyle.Render(fmt.Sprintf("%*s", updatedWidth, truncate(updated[i], updatedWidth))),
		})
	}

	styles := table.DefaultStyles()
	styles.Header = lipgloss.NewStyle().Bold(true).Foreground(normalColor).BorderBottom(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(mutedColor)
	styles.Cell = lipgloss.NewStyle()
	background := inactiveBG
	if m.focused == paneKeys {
		background = selectedBG
	}
	styles.Selected = lipgloss.NewStyle().Background(background)
	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithWidth(width),
		table.WithHeight(height),
		table.WithStyles(styles),
	)
	t.SetCursor(m.keyCursor)
	return t.View()
}

func (m Model) detail(width, height int) string {
	entry, ok := m.selectedKey()
	if !ok {
		return muted("select a key to inspect its comment and history")
	}
	history, err := m.Store.KeyHistory(entry.Key, "%h  %ar  %s")
	comment := entry.Comment
	if comment == "" {
		comment = muted("no comment")
	} else {
		comment = lipgloss.NewStyle().Foreground(commentColor).Render(strings.ReplaceAll(comment, "\n", " "))
	}
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(normalColor).Render(entry.Key),
		comment,
		"",
	}
	if err != nil {
		lines = append(lines, muted(err.Error()))
	} else {
		for _, line := range strings.Split(strings.TrimSpace(history), "\n") {
			if line == "" || len(lines) >= height {
				continue
			}
			lines = append(lines, m.historyLine(line, width))
		}
	}
	for i, line := range lines {
		lines[i] = truncate(line, width)
	}
	return strings.Join(lines, "\n")
}

func (m Model) selectedKeyHistory() ([]string, error) {
	entry, ok := m.selectedKey()
	if !ok {
		return nil, nil
	}
	history, err := m.Store.KeyHistory(entry.Key, "%h  %ar  %s")
	if err != nil {
		return nil, err
	}
	return nonEmptyLines(history), nil
}

func (m Model) historyLine(line string, width int) string {
	hash, rest, found := strings.Cut(line, "  ")
	if !found {
		return truncate(line, width)
	}
	when, subject, found := strings.Cut(rest, "  ")
	if !found {
		return muted(hash + "  " + rest)
	}
	dim := lipgloss.NewStyle().Foreground(mutedColor)
	return dim.Render(hash) + "  " + dim.Render(when) + "  " + lipgloss.NewStyle().Foreground(normalColor).Render(subject)
}

func nonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func muted(text string) string { return lipgloss.NewStyle().Foreground(mutedColor).Render(text) }

func padRight(text string, width int) string {
	text = truncate(text, width)
	return text + strings.Repeat(" ", max(0, width-lipgloss.Width(text)))
}

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
