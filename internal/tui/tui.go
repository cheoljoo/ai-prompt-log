// Package tui implements apl's split-pane view: tig-style, no full-screen
// transitions. Moving the cursor in a list pane immediately updates the
// pane(s) to its right.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/cheoljoo/ai-prompt-log/internal/model"
	"github.com/cheoljoo/ai-prompt-log/internal/source"
)

type pane int

const (
	paneProjects pane = iota
	panePrompts
	paneDetail
)

const maxToolArgsShown = 4
const maxToolValueLen = 160

var (
	styleUserBadge      = lipgloss.NewStyle().Reverse(true).Bold(true).Foreground(lipgloss.Color("6"))
	styleAssistantBadge = lipgloss.NewStyle().Reverse(true).Bold(true).Foreground(lipgloss.Color("2"))
	styleDim            = lipgloss.NewStyle().Faint(true)
	styleMagenta        = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	styleBlue           = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	styleYellow         = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleCyan           = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	styleBold           = lipgloss.NewStyle().Bold(true)
	paneStyle           = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("240"))
	paneFocusStyle      = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("6"))
	labelStyle          = lipgloss.NewStyle().Bold(true)
)

func formatTokens(n int64) string {
	switch {
	case n <= 0:
		return "-"
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func recencyStyle(ts string) lipgloss.Style {
	// Timestamps are ISO8601 strings; lexicographic comparison against
	// "now" formatted the same way is sufficient for recent/older buckets.
	return styleDim
}

func truncateValue(s string) string {
	runes := []rune(s)
	if len(runes) <= maxToolValueLen {
		return s
	}
	return string(runes[:maxToolValueLen]) + fmt.Sprintf("… (전체 %d자)", len(runes))
}

func formatPromptDetail(p model.Prompt) string {
	var b strings.Builder

	meta := []string{styleDim.Render(p.Timestamp)}
	if p.Branch != "" {
		meta = append(meta, styleMagenta.Render(p.Branch))
	}
	if p.TotalTokens > 0 {
		meta = append(meta, styleBlue.Render(formatTokens(p.TotalTokens)+" tokens"))
	}
	if p.Sidechain {
		meta = append(meta, styleYellow.Render("[subagent]"))
	}

	b.WriteString(styleUserBadge.Render(" USER "))
	b.WriteString("  ")
	b.WriteString(strings.Join(meta, "  "))
	b.WriteString("\n")
	b.WriteString(p.UserText)
	b.WriteString("\n\n")

	if len(p.Blocks) > 0 {
		b.WriteString(styleAssistantBadge.Render(" ASSISTANT "))
		b.WriteString("\n\n")
		for _, block := range p.Blocks {
			switch block.Kind {
			case "text":
				b.WriteString(block.Text)
				b.WriteString("\n\n")
			case "tool_use":
				b.WriteString("  ")
				b.WriteString(styleYellow.Render("▸ TOOL"))
				b.WriteString("  ")
				b.WriteString(styleBold.Render(block.ToolName))
				b.WriteString("\n")
				for i, kv := range block.ToolInput {
					if i >= maxToolArgsShown {
						break
					}
					s := strings.ReplaceAll(model.FormatTopLevel(kv.Value), "\n", " ⏎ ")
					s = truncateValue(s)
					b.WriteString("      ")
					b.WriteString(styleDim.Render(kv.Key + ":"))
					b.WriteString(" ")
					b.WriteString(s)
					b.WriteString("\n")
				}
				if len(block.ToolInput) > maxToolArgsShown {
					b.WriteString(styleDim.Render(fmt.Sprintf("      … 외 %d개 인자 생략", len(block.ToolInput)-maxToolArgsShown)))
					b.WriteString("\n")
				}
				b.WriteString("\n")
			}
		}
	} else {
		b.WriteString(styleDim.Render("(no assistant response captured)"))
	}
	return b.String()
}

// Model is the bubbletea model driving apl's split-pane UI.
type Model struct {
	mode    string
	rootDir string

	projects      []model.Project
	projectsTable table.Model

	currentPrompts []model.Prompt
	promptsTable   table.Model

	detail viewport.Model
	focus  pane
	width  int
	height int
}

// New builds the initial model for the given mode ("direct" | "aggregate")
// and root directory, loading data synchronously (matching the Python POC).
func New(mode, rootDir string) Model {
	m := Model{
		mode:    mode,
		rootDir: rootDir,
		detail:  viewport.New(10, 10),
	}

	promptCols := []table.Column{
		{Title: "Date", Width: 19},
		{Title: "Branch", Width: 10},
		{Title: "Tokens", Width: 7},
		{Title: "Tag", Width: 10},
		{Title: "Summary", Width: 40},
	}
	m.promptsTable = table.New(table.WithColumns(promptCols), table.WithFocused(false))

	if mode == "aggregate" {
		projCols := []table.Column{
			{Title: "Project", Width: 20},
			{Title: "Prompts", Width: 7},
			{Title: "Last activity", Width: 19},
		}
		m.projectsTable = table.New(table.WithColumns(projCols), table.WithFocused(true))
		m.projects = model.LoadProjects(rootDir)
		rows := make([]table.Row, 0, len(m.projects))
		for _, p := range m.projects {
			rows = append(rows, table.Row{p.DisplayName, fmt.Sprintf("%d", p.PromptCount), p.LastActivity})
		}
		m.projectsTable.SetRows(rows)
		m.focus = paneProjects
		if len(m.projects) > 0 {
			m.loadPromptsFor(0)
		}
	} else {
		proj := model.LoadProject(rootDir)
		m.projects = []model.Project{proj}
		m.setPromptsFrom(proj)
		m.focus = panePrompts
		m.promptsTable = table.New(table.WithColumns(promptCols), table.WithFocused(true))
		m.setPromptsFrom(proj)
	}
	return m
}

func (m *Model) setPromptsFrom(proj model.Project) {
	prompts := proj.LoadPrompts()
	m.currentPrompts = make([]model.Prompt, len(prompts))
	for i, p := range prompts {
		m.currentPrompts[len(prompts)-1-i] = p
	}
	rows := make([]table.Row, 0, len(m.currentPrompts))
	for _, p := range m.currentPrompts {
		tag := ""
		if p.IsCommand() {
			tag = "[cmd]"
		} else if p.Sidechain {
			tag = "[subagent]"
		}
		ts := p.Timestamp
		if len(ts) > 19 {
			ts = ts[:19]
		}
		rows = append(rows, table.Row{ts, p.Branch, formatTokens(p.TotalTokens), tag, p.Summary()})
	}
	m.promptsTable.SetRows(rows)
	m.promptsTable.SetCursor(0)
	m.refreshDetail()
}

func (m *Model) loadPromptsFor(projectIdx int) {
	if projectIdx < 0 || projectIdx >= len(m.projects) {
		return
	}
	m.setPromptsFrom(m.projects[projectIdx])
}

func (m *Model) refreshDetail() {
	if len(m.currentPrompts) == 0 {
		m.detail.SetContent(styleDim.Render("(no prompt selected)"))
		return
	}
	idx := m.promptsTable.Cursor()
	if idx < 0 || idx >= len(m.currentPrompts) {
		idx = 0
	}
	m.detail.SetContent(formatPromptDetail(m.currentPrompts[idx]))
	m.detail.GotoTop()
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) layout() {
	footerH := 1
	labelH := 1
	borderH := 2
	contentH := m.height - footerH - labelH - borderH
	if contentH < 1 {
		contentH = 1
	}

	var projectsW, promptsW, detailW int
	if m.mode == "aggregate" {
		projectsW = 28
		rest := m.width - projectsW
		promptsW = rest * 45 / 100
		detailW = rest - promptsW
	} else {
		promptsW = m.width * 40 / 100
		detailW = m.width - promptsW
	}

	borderW := 2
	if m.mode == "aggregate" {
		m.projectsTable.SetWidth(projectsW - borderW)
		m.projectsTable.SetHeight(contentH)
	}
	m.promptsTable.SetWidth(promptsW - borderW)
	m.promptsTable.SetHeight(contentH)
	m.detail.Width = detailW - borderW
	m.detail.Height = contentH
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "j", "down":
		m.cursorDown()
	case "k", "up":
		m.cursorUp()
	case "g":
		m.cursorTop()
	case "G":
		m.cursorBottom()
	case "ctrl+f":
		m.pageDown()
	case "ctrl+b":
		m.pageUp()
	case "ctrl+d":
		m.halfPageDown()
	case "ctrl+u":
		m.halfPageUp()
	case "l", "tab", "enter":
		m.focusNext()
	case "h", "shift+tab", "esc":
		m.focusPrev()
	}
	return m, nil
}

func (m *Model) cursorDown() {
	switch m.focus {
	case paneProjects:
		m.projectsTable.MoveDown(1)
		m.loadPromptsFor(m.projectsTable.Cursor())
	case panePrompts:
		m.promptsTable.MoveDown(1)
		m.refreshDetail()
	case paneDetail:
		m.detail.LineDown(1)
	}
}

func (m *Model) cursorUp() {
	switch m.focus {
	case paneProjects:
		m.projectsTable.MoveUp(1)
		m.loadPromptsFor(m.projectsTable.Cursor())
	case panePrompts:
		m.promptsTable.MoveUp(1)
		m.refreshDetail()
	case paneDetail:
		m.detail.LineUp(1)
	}
}

func (m *Model) cursorTop() {
	switch m.focus {
	case paneProjects:
		m.projectsTable.GotoTop()
		m.loadPromptsFor(m.projectsTable.Cursor())
	case panePrompts:
		m.promptsTable.GotoTop()
		m.refreshDetail()
	case paneDetail:
		m.detail.GotoTop()
	}
}

func (m *Model) cursorBottom() {
	switch m.focus {
	case paneProjects:
		m.projectsTable.GotoBottom()
		m.loadPromptsFor(m.projectsTable.Cursor())
	case panePrompts:
		m.promptsTable.GotoBottom()
		m.refreshDetail()
	case paneDetail:
		m.detail.GotoBottom()
	}
}

func (m *Model) pageDown() {
	switch m.focus {
	case paneProjects:
		m.projectsTable.MoveDown(m.projectsTable.Height())
		m.loadPromptsFor(m.projectsTable.Cursor())
	case panePrompts:
		m.promptsTable.MoveDown(m.promptsTable.Height())
		m.refreshDetail()
	case paneDetail:
		m.detail.PageDown()
	}
}

func (m *Model) pageUp() {
	switch m.focus {
	case paneProjects:
		m.projectsTable.MoveUp(m.projectsTable.Height())
		m.loadPromptsFor(m.projectsTable.Cursor())
	case panePrompts:
		m.promptsTable.MoveUp(m.promptsTable.Height())
		m.refreshDetail()
	case paneDetail:
		m.detail.PageUp()
	}
}

func (m *Model) halfPageDown() {
	half := m.paneHeight() / 2
	if half < 1 {
		half = 1
	}
	switch m.focus {
	case paneProjects:
		m.projectsTable.MoveDown(half)
		m.loadPromptsFor(m.projectsTable.Cursor())
	case panePrompts:
		m.promptsTable.MoveDown(half)
		m.refreshDetail()
	case paneDetail:
		m.detail.HalfPageDown()
	}
}

func (m *Model) halfPageUp() {
	half := m.paneHeight() / 2
	if half < 1 {
		half = 1
	}
	switch m.focus {
	case paneProjects:
		m.projectsTable.MoveUp(half)
		m.loadPromptsFor(m.projectsTable.Cursor())
	case panePrompts:
		m.promptsTable.MoveUp(half)
		m.refreshDetail()
	case paneDetail:
		m.detail.HalfPageUp()
	}
}

func (m *Model) paneHeight() int {
	switch m.focus {
	case paneProjects:
		return m.projectsTable.Height()
	case panePrompts:
		return m.promptsTable.Height()
	default:
		return m.detail.Height
	}
}

func (m *Model) focusNext() {
	if m.mode == "aggregate" {
		switch m.focus {
		case paneProjects:
			m.setFocus(panePrompts)
		case panePrompts:
			m.setFocus(paneDetail)
		}
	} else {
		if m.focus == panePrompts {
			m.setFocus(paneDetail)
		}
	}
}

func (m *Model) focusPrev() {
	if m.mode == "aggregate" {
		switch m.focus {
		case paneDetail:
			m.setFocus(panePrompts)
		case panePrompts:
			m.setFocus(paneProjects)
		}
	} else {
		if m.focus == paneDetail {
			m.setFocus(panePrompts)
		}
	}
}

func (m *Model) setFocus(p pane) {
	m.focus = p
	if m.mode == "aggregate" {
		if p == paneProjects {
			m.projectsTable.Focus()
		} else {
			m.projectsTable.Blur()
		}
	}
	if p == panePrompts {
		m.promptsTable.Focus()
	} else {
		m.promptsTable.Blur()
	}
}

func (m Model) View() string {
	var panes []string
	if m.mode == "aggregate" {
		panes = append(panes, m.renderPane("Projects", m.projectsTable.View(), m.focus == paneProjects))
	}
	panes = append(panes, m.renderPane("Prompts", m.promptsTable.View(), m.focus == panePrompts))
	panes = append(panes, m.renderPane("Detail", m.detail.View(), m.focus == paneDetail))

	body := lipgloss.JoinHorizontal(lipgloss.Top, panes...)
	footer := styleDim.Render("j/k move  g/G top/bottom  ^F/^B page  ^D/^U half-page  l/Tab/Enter next pane  h/S-Tab/Esc prev pane  q quit")
	return body + "\n" + footer
}

func (m Model) renderPane(title, body string, focused bool) string {
	style := paneStyle
	if focused {
		style = paneFocusStyle
	}
	label := labelStyle.Render(title)
	box := style.Render(body)
	return lipgloss.JoinVertical(lipgloss.Left, label, box)
}

// DetectAndNew is a convenience wrapper mirroring the Python AplApp
// constructor: resolves mode/root from cwd + flags and builds the Model.
func DetectAndNew(cwd string, aggregate bool, rootOverride string) Model {
	mode, root := source.DetectMode(cwd, aggregate)
	if rootOverride != "" {
		mode, root = "aggregate", rootOverride
	}
	return New(mode, root)
}
