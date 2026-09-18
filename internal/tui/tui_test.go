package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cheoljoo/ai-prompt-log/internal/backup"
	"github.com/cheoljoo/ai-prompt-log/internal/model"
	"github.com/cheoljoo/ai-prompt-log/internal/source"
)

func update(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return next.(Model)
}

func key(k string) tea.KeyMsg {
	switch k {
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+f":
		return tea.KeyMsg{Type: tea.KeyCtrlF}
	case "ctrl+b":
		return tea.KeyMsg{Type: tea.KeyCtrlB}
	case "ctrl+d":
		return tea.KeyMsg{Type: tea.KeyCtrlD}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+l":
		return tea.KeyMsg{Type: tea.KeyCtrlL}
	case "q":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
}

func TestDirectModeRealData(t *testing.T) {
	root := "/data01/cheoljoo.lee/code/ai-prompt-log"
	mode, dir := source.DetectMode(root, false)
	if mode != "direct" {
		t.Fatalf("expected direct mode, got %s", mode)
	}

	m := New(mode, dir)
	m = update(m, tea.WindowSizeMsg{Width: 160, Height: 40})

	if len(m.currentPrompts) == 0 {
		t.Fatal("expected real prompts for this project, got none")
	}
	t.Logf("direct mode prompt count: %d", len(m.currentPrompts))

	if m.focus != panePrompts {
		t.Fatalf("expected initial focus on prompts pane, got %v", m.focus)
	}

	before := m.promptsTable.Cursor()
	m = update(m, key("down"))
	if m.promptsTable.Cursor() != before+1 {
		t.Fatalf("j/down: expected cursor %d, got %d", before+1, m.promptsTable.Cursor())
	}
	m = update(m, key("up"))
	if m.promptsTable.Cursor() != before {
		t.Fatalf("k/up: expected cursor back to %d, got %d", before, m.promptsTable.Cursor())
	}

	m = update(m, key("enter"))
	if m.focus != paneDetail {
		t.Fatalf("enter: expected focus on detail pane, got %v", m.focus)
	}
	if m.detail.View() == "" {
		t.Fatal("expected non-empty detail content after focusing detail pane")
	}

	m = update(m, key("esc"))
	if m.focus != panePrompts {
		t.Fatalf("esc: expected focus back on prompts pane, got %v", m.focus)
	}
}

func TestAggregateModeRealData(t *testing.T) {
	mode, dir := source.DetectMode("/data01/cheoljoo.lee/code/ai-prompt-log", true)
	if mode != "aggregate" {
		t.Fatalf("expected aggregate mode, got %s", mode)
	}

	m := New(mode, dir)
	m = update(m, tea.WindowSizeMsg{Width: 200, Height: 45})

	realDirs := source.ListProjectDirs(source.ClaudeProjectsDir())
	if len(m.projects) != len(realDirs) {
		t.Fatalf("expected %d real projects, got %d", len(realDirs), len(m.projects))
	}
	t.Logf("aggregate project count: %d", len(m.projects))

	if m.focus != paneProjects {
		t.Fatalf("expected initial focus on projects pane, got %v", m.focus)
	}
	if len(m.currentPrompts) == 0 {
		t.Fatal("expected the first project's prompts to be preloaded")
	}

	// move to a different project and confirm prompts pane live-updates
	m = update(m, key("down"))
	promptsAfterMove := len(m.currentPrompts)
	t.Logf("prompts for 2nd project: %d", promptsAfterMove)

	m = update(m, key("tab"))
	if m.focus != panePrompts {
		t.Fatalf("tab: expected focus on prompts pane, got %v", m.focus)
	}
	m = update(m, key("tab"))
	if m.focus != paneDetail {
		t.Fatalf("tab: expected focus on detail pane, got %v", m.focus)
	}
	m = update(m, key("shift+tab"))
	m = update(m, key("shift+tab"))
	if m.focus != paneProjects {
		t.Fatalf("shift+tab x2: expected focus back on projects pane, got %v", m.focus)
	}
}

func TestPagingAndHalfPaging(t *testing.T) {
	mode, dir := source.DetectMode("/data01/cheoljoo.lee/code/ai-prompt-log", true)
	m := New(mode, dir)
	m = update(m, tea.WindowSizeMsg{Width: 200, Height: 45})

	// find a project with plenty of prompts to make paging meaningful
	best := -1
	for i, p := range m.projects {
		if p.PromptCount > 10 {
			best = i
			break
		}
	}
	if best < 0 {
		t.Skip("no project with >10 prompts found in real data")
	}
	for i := 0; i < best; i++ {
		m = update(m, key("down"))
	}
	m = update(m, key("tab")) // focus prompts
	if m.focus != panePrompts {
		t.Fatalf("expected prompts focus, got %v", m.focus)
	}

	m.promptsTable.SetCursor(0)
	m = update(m, key("ctrl+d"))
	afterHalf := m.promptsTable.Cursor()
	if afterHalf <= 0 {
		t.Fatalf("ctrl+d: expected cursor to move down, stayed at %d", afterHalf)
	}
	t.Logf("ctrl+d moved cursor 0 -> %d", afterHalf)

	m = update(m, key("ctrl+u"))
	if m.promptsTable.Cursor() >= afterHalf {
		t.Fatalf("ctrl+u: expected cursor to move back up from %d, got %d", afterHalf, m.promptsTable.Cursor())
	}
}

func TestQuit(t *testing.T) {
	mode, dir := source.DetectMode("/data01/cheoljoo.lee/code/ai-prompt-log", false)
	m := New(mode, dir)
	m = update(m, tea.WindowSizeMsg{Width: 160, Height: 40})
	_, cmd := m.Update(key("q"))
	if cmd == nil {
		t.Fatal("expected q to return a quit command")
	}
}

func TestViewBackupRealData(t *testing.T) {
	backupDir := t.TempDir()
	depth := 1
	stats := backup.Run("/data01/cheoljoo.lee/code/ai-prompt-log", &depth, backupDir)
	if stats.Projects != 16 {
		t.Fatalf("expected backup.Run to find 16 real projects, got %d", stats.Projects)
	}

	m := DetectAndNew("/data01/cheoljoo.lee/code/ai-prompt-log", false, backupDir)
	if m.mode != "aggregate" {
		t.Fatalf("expected aggregate mode via root override, got %s", m.mode)
	}
	if m.rootDir != backupDir {
		t.Fatalf("expected root override to be used, got %s", m.rootDir)
	}
	m = update(m, tea.WindowSizeMsg{Width: 200, Height: 45})
	if len(m.projects) != 16 {
		t.Fatalf("expected 16 backed-up projects, got %d", len(m.projects))
	}
	if len(m.currentPrompts) == 0 {
		t.Fatal("expected first project's prompts preloaded from backup")
	}
	t.Logf("view-backup: %d projects, first project prompts=%d", len(m.projects), len(m.currentPrompts))
}

func TestSpaceAndGRealData(t *testing.T) {
	mode, dir := source.DetectMode("/data01/cheoljoo.lee/code/ai-prompt-log", false)
	m := New(mode, dir)
	m = update(m, tea.WindowSizeMsg{Width: 160, Height: 40})
	before := m.promptsTable.Cursor()

	m = update(m, key(" "))
	afterSpace := m.promptsTable.Cursor()
	if afterSpace <= before {
		t.Fatalf("space should page down like ctrl+f: %d -> %d", before, afterSpace)
	}
	t.Logf("space: cursor %d -> %d", before, afterSpace)

	m = update(m, key("G"))
	bottom := m.promptsTable.Cursor()
	if bottom != len(m.promptsTable.Rows())-1 {
		t.Fatalf("G should reach the last row: got %d, row_count=%d", bottom, len(m.promptsTable.Rows()))
	}
	t.Logf("G: cursor -> %d (row_count=%d)", bottom, len(m.promptsTable.Rows()))
}

func TestFinalResultSectionRealData(t *testing.T) {
	mode, dir := source.DetectMode("/data01/cheoljoo.lee/code/ai-prompt-log", false)
	m := New(mode, dir)
	m = update(m, tea.WindowSizeMsg{Width: 160, Height: 40})

	var target *model.Prompt
	for i := range m.currentPrompts {
		p := &m.currentPrompts[i]
		hasTool := false
		for _, b := range p.Blocks {
			if b.Kind == "tool_use" {
				hasTool = true
			}
		}
		if hasTool && len(p.Blocks) > 0 && p.Blocks[len(p.Blocks)-1].Kind == "text" {
			target = p
			break
		}
	}
	if target == nil {
		t.Fatal("expected a real prompt with tool calls + trailing text")
	}
	text := formatPromptDetail(*target)
	if !strings.Contains(text, "FINAL-RESULT") {
		t.Fatal("expected a FINAL-RESULT section")
	}
	userIdx := strings.Index(text, "USER")
	finalIdx := strings.Index(text, "FINAL-RESULT")
	assistantIdx := strings.Index(text, "ASSISTANT")
	if !(userIdx < finalIdx && finalIdx < assistantIdx) {
		t.Fatalf("expected USER -> FINAL-RESULT -> ASSISTANT order, got indices %d, %d, %d", userIdx, finalIdx, assistantIdx)
	}
	t.Logf("FINAL-RESULT section present and correctly ordered")
}

// TestReloadRealData exercises Ctrl+L reload and the 30s background change
// check against a real (copied) session file, never touching the actual
// ~/.claude/projects data.
func TestReloadRealData(t *testing.T) {
	backupDir := t.TempDir()
	depth := 1
	backup.Run("/data01/cheoljoo.lee/code/ai-prompt-log", &depth, backupDir)

	m := DetectAndNew("/data01/cheoljoo.lee/code/ai-prompt-log", false, backupDir)
	m = update(m, tea.WindowSizeMsg{Width: 160, Height: 40})

	proj := m.projects[m.currentProjectIdx]
	files := source.ListSessionFiles(proj.DirPath)
	if len(files) == 0 {
		t.Fatal("expected at least one copied session file")
	}
	promptsBefore := len(m.currentPrompts)

	// No changes yet: Ctrl+L should report nothing to do and leave prompts untouched.
	m = update(m, key("ctrl+l"))
	if m.changesPending {
		t.Fatal("changesPending should stay false with no file changes")
	}
	if m.statusMessage != "변경 없음" {
		t.Fatalf("expected no-change status message, got %q", m.statusMessage)
	}
	if len(m.currentPrompts) != promptsBefore {
		t.Fatalf("no-op reload should not change prompt count: %d -> %d", promptsBefore, len(m.currentPrompts))
	}

	// Modify one of the real copied session files and force its mtime
	// forward so the change is detectable regardless of filesystem mtime
	// resolution.
	target := files[0]
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(original), "\n"), "\n")
	if len(lines) == 0 {
		t.Fatal("expected at least one line in the copied session file")
	}
	appended := string(original) + lines[len(lines)-1] + "\n"
	if err := os.WriteFile(target, []byte(appended), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(target, future, future); err != nil {
		t.Fatal(err)
	}

	if !m.hasChanges() {
		t.Fatal("expected hasChanges to detect the modified session file")
	}

	// The periodic background check should flag it without reloading.
	m = update(m, checkChangesMsg{})
	if !m.changesPending {
		t.Fatal("expected changesPending to be set by the periodic check")
	}
	if !strings.Contains(m.statusMessage, "Ctrl+L") {
		t.Fatalf("expected a change notice mentioning Ctrl+L, got %q", m.statusMessage)
	}
	if len(m.currentPrompts) != promptsBefore {
		t.Fatalf("periodic check must not auto-reload: %d -> %d", promptsBefore, len(m.currentPrompts))
	}

	// Ctrl+L now reloads and clears the pending flag.
	m = update(m, key("ctrl+l"))
	if m.changesPending {
		t.Fatal("changesPending should clear after reload")
	}
	if m.statusMessage != "다시 불러왔습니다" {
		t.Fatalf("expected reload confirmation, got %q", m.statusMessage)
	}
	if m.hasChanges() {
		t.Fatal("hasChanges should be false right after a successful reload")
	}
	t.Logf("reload: %d -> %d prompts after picking up the change", promptsBefore, len(m.currentPrompts))
}
