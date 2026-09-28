// Package tui implements apl's split-pane view: tig-style, no full-screen
// transitions. Moving the cursor in a list pane immediately updates the
// pane(s) to its right.
package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/cheoljoo/ai-prompt-log/internal/model"
	"github.com/cheoljoo/ai-prompt-log/internal/source"
)

// changeCheckInterval is how often apl checks the current project's session
// files for changes in the background (it never auto-reloads -- it only
// flags that Ctrl+L would pick up new data).
const changeCheckInterval = 30 * time.Second

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
	styleFinalBadge     = lipgloss.NewStyle().Reverse(true).Bold(true).Foreground(lipgloss.Color("4"))
	styleModifiedBadge  = lipgloss.NewStyle().Reverse(true).Bold(true).Foreground(lipgloss.Color("5"))
	styleAssistantBadge = lipgloss.NewStyle().Reverse(true).Bold(true).Foreground(lipgloss.Color("2"))
	styleDim            = lipgloss.NewStyle().Faint(true)
	styleMagenta        = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	styleBlue           = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	styleYellow         = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleRed            = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleCyan           = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	styleGreen          = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	styleBrightMagenta  = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	styleBold           = lipgloss.NewStyle().Bold(true)
	styleSearchMatch    = lipgloss.NewStyle().Reverse(true).Bold(true).Foreground(lipgloss.Color("3"))
	styleSearchTitle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3"))
	styleSearchBox      = lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(lipgloss.Color("3")).Background(lipgloss.Color("0")).Foreground(lipgloss.Color("15")).Padding(1, 2)
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

// parseISOTimestamp parses a stored (always-UTC) ISO8601 timestamp string,
// as produced by every parser in internal/model (RFC3339, optionally with
// fractional seconds). Returns ok=false for empty/unparseable strings.
func parseISOTimestamp(ts string) (t time.Time, ok bool) {
	if ts == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, ts); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// localTS renders a stored UTC timestamp in the system's local timezone,
// "YYYY-MM-DDTHH:MM:SS" (19 chars, matching the previous raw-UTC slice
// width) for compact table columns.
//
// All prompt/session timestamps are parsed/stored/sorted as UTC ("Z"-
// suffixed ISO8601) -- see docs/data-model.md -- which is correct for
// internal consistency, but was previously displayed as-is (just string-
// sliced), so a prompt made at 13:51 KST showed up as "...T04:51:38" with
// no timezone marker, easily misread as 4:51am local time. This converts
// to local wall-clock time for display only; parsing, sorting, and --save
// continue to use the raw UTC string.
func localTS(ts string) string {
	t, ok := parseISOTimestamp(ts)
	if !ok {
		if ts == "" {
			return "-"
		}
		if len(ts) > 19 {
			return ts[:19]
		}
		return ts
	}
	return t.Local().Format("2006-01-02T15:04:05")
}

// localTSFull is like localTS but keeps the UTC offset (e.g. "+09:00") so
// it's self-evidently local time, not UTC -- used where there's room to
// spare (the Detail pane's USER header), rather than a narrow table column.
func localTSFull(ts string) string {
	t, ok := parseISOTimestamp(ts)
	if !ok {
		return ts
	}
	return t.Local().Format(time.RFC3339)
}

func truncateValue(s string) string {
	runes := []rune(s)
	if len(runes) <= maxToolValueLen {
		return s
	}
	return string(runes[:maxToolValueLen]) + fmt.Sprintf("… (전체 %d자)", len(runes))
}

// finalResultText is the trailing run of "text" blocks at the end of the
// assistant's response -- its concluding remarks, after any tool calls.
// Empty if the response ends on a tool_use with no closing text.
func finalResultText(p model.Prompt) string {
	var texts []string
	for i := len(p.Blocks) - 1; i >= 0; i-- {
		if p.Blocks[i].Kind != "text" {
			break
		}
		texts = append(texts, p.Blocks[i].Text)
	}
	for i, j := 0, len(texts)-1; i < j; i, j = i+1, j-1 {
		texts[i], texts[j] = texts[j], texts[i]
	}
	return strings.Join(texts, "\n\n")
}

// searchPrefix returns the vi-style prefix character for a search mode:
// "/" for both fields, "<" for User Prompt only, ">" for Final Result only.
func searchPrefix(mode string) string {
	switch mode {
	case "user":
		return "<"
	case "final":
		return ">"
	default:
		return "/"
	}
}

// promptMatchesSearch reports whether `term` (case-insensitive) occurs in
// the field(s) selected by `mode`: "user" (User Prompt text only), "final"
// (Final Result text only), or "both" (either field).
func promptMatchesSearch(p model.Prompt, term, mode string) bool {
	if term == "" {
		return false
	}
	termL := strings.ToLower(term)
	if (mode == "both" || mode == "user") && strings.Contains(strings.ToLower(p.UserText), termL) {
		return true
	}
	if (mode == "both" || mode == "final") && strings.Contains(strings.ToLower(finalResultText(p)), termL) {
		return true
	}
	return false
}

// highlightText wraps every case-insensitive occurrence of `term` in
// `text` with reverse-video styling so search matches stand out. Returns
// `text` unchanged if `term` is empty or not found.
func highlightText(text, term string) string {
	if term == "" {
		return text
	}
	lowerText := strings.ToLower(text)
	lowerTerm := strings.ToLower(term)
	tlen := len(term)
	var b strings.Builder
	pos := 0
	for {
		rel := strings.Index(lowerText[pos:], lowerTerm)
		if rel == -1 {
			b.WriteString(text[pos:])
			break
		}
		idx := pos + rel
		b.WriteString(text[pos:idx])
		b.WriteString(styleSearchMatch.Render(text[idx : idx+tlen]))
		pos = idx + tlen
	}
	return b.String()
}

// formatPromptDetail renders USER -> FINAL-RESULT -> ASSISTANT (not just
// USER -> ASSISTANT) so the prompt and its conclusion are visible
// immediately, with the full tool-by-tool trace available below only if
// needed -- the final result text is deliberately repeated at the end of
// ASSISTANT too, in its original place in the trace.
func formatPromptDetail(p model.Prompt) string {
	return formatPromptDetailWithSearch(p, "", "both")
}

// formatPromptDetailWithSearch is formatPromptDetail plus highlighting of
// `searchTerm` occurrences within the User Prompt and/or Final Result text,
// according to `searchMode` ("both" | "user" | "final").
func formatPromptDetailWithSearch(p model.Prompt, searchTerm, searchMode string) string {
	var b strings.Builder

	srcBadge := styleCyan.Render("[claude]")
	switch p.Source {
	case "agy":
		srcBadge = styleGreen.Render("[agy]")
	case "opencode":
		srcBadge = styleMagenta.Render("[opencode]")
	case "gemini":
		srcBadge = styleBlue.Render("[gemini]")
	case "copilot":
		srcBadge = styleBrightMagenta.Render("[copilot]")
	}
	meta := []string{srcBadge, styleDim.Render(localTSFull(p.Timestamp))}
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
	if searchMode == "both" || searchMode == "user" {
		b.WriteString(highlightText(p.UserText, searchTerm))
	} else {
		b.WriteString(p.UserText)
	}
	b.WriteString("\n\n")

	if final := finalResultText(p); final != "" {
		b.WriteString(styleFinalBadge.Render(" FINAL-RESULT "))
		b.WriteString("\n\n")
		if searchMode == "both" || searchMode == "final" {
			b.WriteString(highlightText(final, searchTerm))
		} else {
			b.WriteString(final)
		}
		b.WriteString("\n\n")
	}

	fileChanges := p.FileChanges()
	if len(fileChanges) > 0 {
		b.WriteString(styleModifiedBadge.Render(" MODIFIED FILES "))
		b.WriteString("\n\n")
		for _, fc := range fileChanges {
			sym := styleDim.Render("?")
			switch fc.Action {
			case "created":
				sym = styleGreen.Render("+")
			case "modified":
				sym = styleYellow.Render("~")
			case "deleted":
				sym = styleRed.Render("-")
			}
			b.WriteString(fmt.Sprintf("  %s %s\n", sym, fc.Path))
		}
		b.WriteString("\n")
	}

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
	mode         string
	rootDir      string
	sourceFilter string

	projects      []model.Project
	projectsTable table.Model

	currentPrompts []model.Prompt
	promptsTable   table.Model

	detail viewport.Model
	focus  pane
	width  int
	height int

	// currentProjectIdx indexes m.projects for whichever project is shown
	// in promptsTable; currentMTimes is the session-file mtime snapshot
	// taken at that project's last load, used by Ctrl+L reload.
	currentProjectIdx int
	currentMTimes     map[string]time.Time
	changesPending    bool
	statusMessage     string

	// Search state (vi-style: /both, <user-only, >final-only, n/p to
	// navigate matches). searchMatches holds indices into currentPrompts.
	searchInput       textinput.Model
	searchBarVisible  bool
	searchTerm        string
	searchMode        string // "both" | "user" | "final"
	pendingSearchMode string
	searchMatches     []int
}

// NewWithFilter builds the initial model respecting the source filter.
func NewWithFilter(mode, rootDir, sourceFilter string) Model {
	if sourceFilter == "" {
		sourceFilter = "all"
	}
	m := Model{
		mode:         mode,
		rootDir:      rootDir,
		sourceFilter: sourceFilter,
		detail:       viewport.New(10, 10),
		searchMode:   "both",
	}
	m.searchInput = textinput.New()
	m.searchInput.Placeholder = "type keyword, Enter to search…"
	m.searchInput.Prompt = "/ "
	// Explicit, terminal-independent high-contrast styling (bold white on
	// black) so typed text is always clearly visible regardless of the
	// user's terminal theme -- don't rely on ambient/default colors.
	m.searchInput.TextStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(lipgloss.Color("0"))
	m.searchInput.PromptStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("3")).Background(lipgloss.Color("0"))
	m.searchInput.PlaceholderStyle = lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("8")).Background(lipgloss.Color("0"))
	m.searchInput.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("3"))

	promptCols := []table.Column{
		{Title: "Date", Width: 19},
		{Title: "Source", Width: 8},
		{Title: "Branch", Width: 10},
		{Title: "Tokens", Width: 7},
		{Title: "Tag", Width: 10},
		{Title: "Summary", Width: 40},
	}
	m.promptsTable = table.New(table.WithColumns(promptCols), table.WithFocused(false))

	if mode == "aggregate" {
		projCols := []table.Column{
			{Title: "Project", Width: 20},
			{Title: "Source", Width: 12},
			{Title: "Prompts", Width: 7},
			{Title: "Last activity", Width: 19},
		}
		m.projectsTable = table.New(table.WithColumns(projCols), table.WithFocused(true))
		m.projects = model.LoadProjectsWithFilter(rootDir, sourceFilter)
		rows := make([]table.Row, 0, len(m.projects))
		for _, p := range m.projects {
			rows = append(rows, table.Row{p.DisplayName, p.Source, fmt.Sprintf("%d", p.PromptCount), localTS(p.LastActivity)})
		}
		m.projectsTable.SetRows(rows)
		m.focus = paneProjects
		if len(m.projects) > 0 {
			m.loadPromptsFor(0)
		}
	} else {
		proj := model.LoadProjectWithFilter(rootDir, sourceFilter)
		m.projects = []model.Project{proj}
		m.currentProjectIdx = 0
		m.focus = panePrompts
		m.promptsTable = table.New(table.WithColumns(promptCols), table.WithFocused(true))
		m.setPromptsFrom(proj)
	}
	return m
}

// New builds the initial model for the given mode and root directory.
func New(mode, rootDir string) Model {
	return NewWithFilter(mode, rootDir, "all")
}

func (m *Model) setPromptsFrom(proj model.Project) {
	m.searchTerm = ""
	m.searchMatches = nil
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
		} else if p.IsTaskNotification() {
			tag = "[bg]"
		} else if p.Sidechain {
			tag = "[subagent]"
		}
		ts := localTS(p.Timestamp)
		rows = append(rows, table.Row{ts, p.Source, p.Branch, formatTokens(p.TotalTokens), tag, p.Summary()})
	}
	m.promptsTable.SetRows(rows)
	m.promptsTable.SetCursor(0)
	m.refreshDetail()

	m.currentMTimes = snapshotMTimes(proj)
	m.changesPending = false
	m.statusMessage = ""
}

func (m *Model) loadPromptsFor(projectIdx int) {
	if projectIdx < 0 || projectIdx >= len(m.projects) {
		return
	}
	m.currentProjectIdx = projectIdx
	m.setPromptsFrom(m.projects[projectIdx])
}

// snapshotMTimes records the modification time of every session file in a
// project, for later comparison to detect whether a reload would pick up new data.
func snapshotMTimes(proj model.Project) map[string]time.Time {
	files := proj.GetSessionFiles()
	mtimes := make(map[string]time.Time, len(files))
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			continue
		}
		mtimes[f] = info.ModTime()
	}
	return mtimes
}

func mtimesEqual(a, b map[string]time.Time) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || !bv.Equal(v) {
			return false
		}
	}
	return true
}

// hasChanges reports whether the current project's session files differ
// from the mtime snapshot taken at its last load.
func (m *Model) hasChanges() bool {
	if m.currentProjectIdx < 0 || m.currentProjectIdx >= len(m.projects) {
		return false
	}
	return !mtimesEqual(snapshotMTimes(m.projects[m.currentProjectIdx]), m.currentMTimes)
}

// reload re-reads the current project's prompts if its session files have
// changed since the last load; otherwise it just reports that there is
// nothing to do.
func (m *Model) reload() {
	if m.currentProjectIdx < 0 || m.currentProjectIdx >= len(m.projects) {
		return
	}
	if !m.hasChanges() {
		m.statusMessage = "변경 없음"
		return
	}
	refreshed := model.LoadProjectWithFilter(m.projects[m.currentProjectIdx].DirPath, m.sourceFilter)
	m.projects[m.currentProjectIdx] = refreshed
	m.setPromptsFrom(refreshed)
	if m.mode == "aggregate" {
		rows := m.projectsTable.Rows()
		if m.currentProjectIdx < len(rows) {
			rows[m.currentProjectIdx] = table.Row{refreshed.DisplayName, refreshed.Source, fmt.Sprintf("%d", refreshed.PromptCount), localTS(refreshed.LastActivity)}
			m.projectsTable.SetRows(rows)
		}
	}
	m.statusMessage = "다시 불러왔습니다"
}

type checkChangesMsg struct{}

func tickCheckChanges() tea.Cmd {
	return tea.Tick(changeCheckInterval, func(time.Time) tea.Msg {
		return checkChangesMsg{}
	})
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
	m.detail.SetContent(formatPromptDetailWithSearch(m.currentPrompts[idx], m.searchTerm, m.searchMode))
	m.detail.GotoTop()
}

func (m Model) Init() tea.Cmd {
	return tickCheckChanges()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case checkChangesMsg:
		if !m.changesPending && m.hasChanges() {
			m.changesPending = true
			m.statusMessage = "⚠ 변경 사항이 있습니다 — Ctrl+L로 새로고침하세요"
		}
		return m, tickCheckChanges()
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

	// Match renderSearchModal's box sizing so the input's visible width
	// stays within the centered dialog (box width minus border, padding,
	// and the "/ " prompt).
	boxWidth := m.width * 70 / 100
	if boxWidth > 90 {
		boxWidth = 90
	}
	if boxWidth < 30 {
		boxWidth = 30
	}
	if boxWidth > m.width-4 {
		boxWidth = m.width - 4
	}
	searchW := boxWidth - 2 /*border*/ - 4 /*padding*/ - 2 /*prompt*/
	if searchW < 10 {
		searchW = 10
	}
	m.searchInput.Width = searchW
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searchBarVisible {
		return m.handleSearchKey(msg)
	}
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
	case "ctrl+f", " ":
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
	case "ctrl+l":
		m.reload()
	case "/":
		return m, m.beginSearch("both")
	case "<":
		return m, m.beginSearch("user")
	case ">":
		return m, m.beginSearch("final")
	case "n":
		m.searchNext()
	case "p":
		m.searchPrev()
	}
	return m, nil
}

// handleSearchKey routes key events to the search input widget while the
// search bar is open, intercepting only Enter (submit) and Esc (cancel).
func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.submitSearch()
		return m, nil
	case tea.KeyEsc:
		m.cancelSearch()
		return m, nil
	}
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	return m, cmd
}

// beginSearch opens the search bar in the given mode ("both" | "user" |
// "final") and focuses it, returning the cursor-blink command.
func (m *Model) beginSearch(mode string) tea.Cmd {
	m.pendingSearchMode = mode
	m.searchBarVisible = true
	m.searchInput.Prompt = searchPrefix(mode) + " "
	m.searchInput.SetValue("")
	return m.searchInput.Focus()
}

// cancelSearch closes the search bar without changing any active search
// (Esc while the search bar is open).
func (m *Model) cancelSearch() {
	m.searchBarVisible = false
	m.searchInput.Blur()
}

// submitSearch runs on Enter inside the search bar: closes the bar,
// computes every matching prompt, and jumps to the first match at/after
// the current cursor (wrapping), vi-search style. An empty keyword clears
// any previously active search instead.
func (m *Model) submitSearch() {
	term := strings.TrimSpace(m.searchInput.Value())
	m.searchBarVisible = false
	m.searchInput.Blur()

	if term == "" {
		m.searchTerm = ""
		m.searchMatches = nil
		m.statusMessage = ""
		return
	}

	m.searchTerm = term
	m.searchMode = m.pendingSearchMode
	m.searchMatches = nil
	for i, p := range m.currentPrompts {
		if promptMatchesSearch(p, m.searchTerm, m.searchMode) {
			m.searchMatches = append(m.searchMatches, i)
		}
	}
	if len(m.searchMatches) > 0 {
		m.searchNext()
	} else {
		m.statusMessage = fmt.Sprintf("'%s': 검색 결과 없음", term)
	}
}

// searchNext jumps to the next match strictly after the cursor, wrapping
// around to the first match if the cursor is at or after the last one.
func (m *Model) searchNext() {
	if m.searchTerm == "" || len(m.searchMatches) == 0 {
		return
	}
	cur := m.promptsTable.Cursor()
	target := m.searchMatches[0]
	for _, idx := range m.searchMatches {
		if idx > cur {
			target = idx
			break
		}
	}
	m.jumpToSearchMatch(target)
}

// searchPrev jumps to the previous match strictly before the cursor,
// wrapping around to the last match if the cursor is at or before the
// first one.
func (m *Model) searchPrev() {
	if m.searchTerm == "" || len(m.searchMatches) == 0 {
		return
	}
	cur := m.promptsTable.Cursor()
	target := m.searchMatches[len(m.searchMatches)-1]
	for _, idx := range m.searchMatches {
		if idx < cur {
			target = idx
		} else {
			break
		}
	}
	m.jumpToSearchMatch(target)
}

func (m *Model) jumpToSearchMatch(target int) {
	m.promptsTable.SetCursor(target)
	m.refreshDetail()
	pos := 0
	for i, idx := range m.searchMatches {
		if idx == target {
			pos = i + 1
			break
		}
	}
	m.statusMessage = fmt.Sprintf("%s%s  (%d/%d)", searchPrefix(m.searchMode), m.searchTerm, pos, len(m.searchMatches))
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
	if m.searchBarVisible {
		return m.renderSearchModal()
	}

	var panes []string
	if m.mode == "aggregate" {
		panes = append(panes, m.renderPane("Projects", m.projectsTable.View(), m.focus == paneProjects))
	}
	panes = append(panes, m.renderPane("Prompts", m.promptsTable.View(), m.focus == panePrompts))
	panes = append(panes, m.renderPane("Detail", m.detail.View(), m.focus == paneDetail))

	body := lipgloss.JoinHorizontal(lipgloss.Top, panes...)
	footer := styleDim.Render("j/k move  g/G top/bottom  ^F/^B/Space page  ^D/^U half-page  l/Tab/Enter next pane  h/S-Tab/Esc prev pane  /search-both  <search-USER  >search-RESULT  n/p next/prev match  ^L reload  q quit")
	if m.statusMessage != "" {
		style := styleDim
		if m.changesPending {
			style = styleYellow
		}
		footer = style.Render(m.statusMessage) + "  " + footer
	}
	return body + "\n" + footer
}

// searchModeLabel is the human-readable title shown atop the search modal
// for each search mode.
func searchModeLabel(mode string) string {
	switch mode {
	case "user":
		return "Search — User Prompt only"
	case "final":
		return "Search — Final Result only"
	default:
		return "Search — User Prompt + Final Result"
	}
}

// renderSearchModal replaces the whole view with a centered, high-contrast
// (bold white on black, thick yellow border) dialog while the search bar is
// open -- Bubble Tea has no true overlay/layer compositing like Textual, so
// a full-screen modal is the simplest way to guarantee the search box (and
// the text being typed into it) is always clearly visible, centered,
// regardless of terminal theme.
func (m Model) renderSearchModal() string {
	width, height := m.width, m.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}

	boxWidth := width * 70 / 100
	if boxWidth > 90 {
		boxWidth = 90
	}
	if boxWidth < 30 {
		boxWidth = 30
	}
	if boxWidth > width-4 {
		boxWidth = width - 4
	}

	title := styleSearchTitle.Render(searchModeLabel(m.pendingSearchMode))
	content := title + "\n\n" + m.searchInput.View()
	box := styleSearchBox.Width(boxWidth).Render(content)

	return lipgloss.Place(
		width, height,
		lipgloss.Center, lipgloss.Center,
		box,
		lipgloss.WithWhitespaceBackground(lipgloss.Color("0")),
	)
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

// DetectAndNewWithFilter resolves mode/root from cwd + flags and builds the Model with source filter.
func DetectAndNewWithFilter(cwd string, aggregate bool, rootOverride, sourceFilter string) Model {
	if sourceFilter == "" {
		sourceFilter = "all"
	}
	mode, root := source.DetectModeWithFilter(cwd, aggregate, sourceFilter)
	if rootOverride != "" {
		mode, root = "aggregate", rootOverride
	}
	return NewWithFilter(mode, root, sourceFilter)
}

// DetectAndNew is a convenience wrapper mirroring the Python AplApp
// constructor: resolves mode/root from cwd + flags and builds the Model.
func DetectAndNew(cwd string, aggregate bool, rootOverride string) Model {
	return DetectAndNewWithFilter(cwd, aggregate, rootOverride, "all")
}
