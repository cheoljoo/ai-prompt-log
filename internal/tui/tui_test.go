package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cheoljoo/ai-prompt-log/internal/backup"
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
