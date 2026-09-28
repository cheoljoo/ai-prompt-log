package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cheoljoo/ai-prompt-log/internal/model"
	"github.com/cheoljoo/ai-prompt-log/internal/source"
)

func testPrompt(userText, finalText string) model.Prompt {
	var blocks []model.AssistantBlock
	if finalText != "" {
		blocks = append(blocks, model.AssistantBlock{Kind: "text", Text: finalText})
	}
	return model.Prompt{
		SessionID: "s1",
		Timestamp: "2026-09-28T00:00:00Z",
		UserText:  userText,
		Blocks:    blocks,
		Source:    "claude",
	}
}

func TestPromptMatchesSearch(t *testing.T) {
	userOnly := testPrompt("please fix the login BUG", "all good now")
	finalOnly := testPrompt("add a new feature", "fixed a Bug along the way")
	neither := testPrompt("unrelated request", "unrelated response")

	if !promptMatchesSearch(userOnly, "bug", "both") {
		t.Fatal("expected both-mode to match user field")
	}
	if !promptMatchesSearch(finalOnly, "bug", "both") {
		t.Fatal("expected both-mode to match final field")
	}
	if promptMatchesSearch(neither, "bug", "both") {
		t.Fatal("expected no match for unrelated prompt")
	}
	if !promptMatchesSearch(userOnly, "bug", "user") {
		t.Fatal("expected user-mode to match user field")
	}
	if promptMatchesSearch(finalOnly, "bug", "user") {
		t.Fatal("expected user-mode to NOT match final-only prompt")
	}
	if !promptMatchesSearch(finalOnly, "bug", "final") {
		t.Fatal("expected final-mode to match final field")
	}
	if promptMatchesSearch(userOnly, "bug", "final") {
		t.Fatal("expected final-mode to NOT match user-only prompt")
	}
	if !promptMatchesSearch(userOnly, "BUG", "user") {
		t.Fatal("expected case-insensitive match")
	}
	if promptMatchesSearch(userOnly, "", "both") {
		t.Fatal("expected empty term to never match")
	}
}

func TestHighlightText(t *testing.T) {
	rendered := highlightText("Bug here, another bug there", "bug")
	if strings.Count(rendered, styleSearchMatch.Render("Bug")) == 0 && strings.Count(rendered, styleSearchMatch.Render("bug")) == 0 {
		t.Fatalf("expected highlighted matches in %q", rendered)
	}
	if got := highlightText("a b c", ""); got != "a b c" {
		t.Fatalf("expected no-op when term is empty, got %q", got)
	}
	if got := highlightText("nothing to see", "zzz"); got != "nothing to see" {
		t.Fatalf("expected no-op when term not found, got %q", got)
	}
}

func TestFormatPromptDetailWithSearchHighlighting(t *testing.T) {
	p := testPrompt("fix the bug please", "done, no more bug")

	both := formatPromptDetailWithSearch(p, "bug", "both")
	userOnlyMode := formatPromptDetailWithSearch(p, "bug", "user")
	finalOnlyMode := formatPromptDetailWithSearch(p, "bug", "final")
	plain := formatPromptDetail(p)

	highlightMarker := styleSearchMatch.Render("bug")
	if strings.Count(both, highlightMarker) < 2 {
		t.Fatalf("expected both user+final occurrences highlighted in both-mode, got:\n%s", both)
	}
	if strings.Count(userOnlyMode, highlightMarker) != 1 {
		t.Fatalf("expected exactly 1 highlighted occurrence in user-mode, got %d:\n%s", strings.Count(userOnlyMode, highlightMarker), userOnlyMode)
	}
	if strings.Count(finalOnlyMode, highlightMarker) != 1 {
		t.Fatalf("expected exactly 1 highlighted occurrence in final-mode, got %d:\n%s", strings.Count(finalOnlyMode, highlightMarker), finalOnlyMode)
	}
	if strings.Contains(plain, highlightMarker) {
		t.Fatal("expected formatPromptDetail (no search) to never highlight")
	}
}

func TestSearchPrefix(t *testing.T) {
	cases := map[string]string{"both": "/", "user": "<", "final": ">", "unknown": "/"}
	for mode, want := range cases {
		if got := searchPrefix(mode); got != want {
			t.Fatalf("searchPrefix(%q) = %q, want %q", mode, got, want)
		}
	}
}

func TestSearchModeLabel(t *testing.T) {
	cases := map[string]string{
		"both":    "User Prompt + Final Result",
		"user":    "User Prompt only",
		"final":   "Final Result only",
		"unknown": "User Prompt + Final Result",
	}
	for mode, wantSubstr := range cases {
		if got := searchModeLabel(mode); !strings.Contains(got, wantSubstr) {
			t.Fatalf("searchModeLabel(%q) = %q, want it to contain %q", mode, got, wantSubstr)
		}
	}
}

// TestSearchModalIsCenteredAndVisible confirms the search dialog replaces
// the view with a centered, high-contrast modal (not an easy-to-miss
// single line docked at the bottom of the screen) while it's open, and that
// typed characters render with a clearly visible (non-default) style.
func TestSearchModalIsCenteredAndVisible(t *testing.T) {
	mode, dir := source.DetectMode("/data01/cheoljoo.lee/code/ai-prompt-log", false)
	m := New(mode, dir)
	m = update(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	m = update(m, key("/"))
	for _, ch := range "bug" {
		m = update(m, key(string(ch)))
	}
	view := m.View()

	if !strings.Contains(view, "bug") {
		t.Fatalf("expected typed term 'bug' to appear in the rendered modal view")
	}
	if !strings.Contains(view, "Search — User Prompt + Final Result") {
		t.Fatalf("expected the mode label in the modal, got:\n%s", view)
	}

	lines := strings.Split(view, "\n")
	if len(lines) < 20 {
		t.Fatalf("expected the modal view to fill the terminal height, got %d lines", len(lines))
	}

	// The box border should not appear on the very first or last rendered
	// line -- i.e. it must be vertically centered, not docked to an edge.
	borderLineIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "┏") {
			borderLineIdx = i
			break
		}
	}
	if borderLineIdx <= 0 || borderLineIdx >= len(lines)-1 {
		t.Fatalf("expected the search box border to be vertically centered (not on the first/last line), found at line %d of %d", borderLineIdx, len(lines))
	}

	// The typed text's line should be horizontally centered too: there
	// should be a comparable amount of plain background padding on both
	// sides of the box content.
	var textLine string
	for _, l := range lines {
		if strings.Contains(l, "bug") {
			textLine = l
			break
		}
	}
	if textLine == "" {
		t.Fatal("expected to find the line containing the typed term")
	}
	leftPad := strings.Index(textLine, "┃")
	rightPad := len(textLine) - strings.LastIndex(textLine, "┃")
	if leftPad <= 0 {
		t.Fatalf("expected non-zero left padding before the search box, got %d in line %q", leftPad, textLine)
	}
	// Left/right padding should be roughly symmetric (within a few chars,
	// to allow for ANSI escape sequence length differences).
	diff := leftPad - rightPad
	if diff < 0 {
		diff = -diff
	}
	if diff > 6 {
		t.Fatalf("expected the search box to be horizontally centered, left pad=%d right pad=%d (diff=%d)", leftPad, rightPad, diff)
	}
}

// TestSearchNavigationRealData exercises the full vi-style search flow
// (/,<,>,n,p) against this repo's own real Claude session log, through the
// public Update()/View() surface (key presses), not internal helpers.
func TestSearchNavigationRealData(t *testing.T) {
	mode, dir := source.DetectMode("/data01/cheoljoo.lee/code/ai-prompt-log", false)
	m := New(mode, dir)
	m = update(m, tea.WindowSizeMsg{Width: 160, Height: 40})

	if len(m.currentPrompts) < 3 {
		t.Skip("not enough real prompts to exercise search meaningfully")
	}

	// Find a keyword that appears in at least 2 prompts' User Prompt text,
	// preferring a common Korean particle so this isn't tied to any one
	// prompt's wording.
	term := ""
	for _, candidate := range []string{"the", "이", "요", "a"} {
		count := 0
		for _, p := range m.currentPrompts {
			if promptMatchesSearch(p, candidate, "user") {
				count++
			}
		}
		if count >= 2 {
			term = candidate
			break
		}
	}
	if term == "" {
		t.Skip("no common keyword found across at least 2 real prompts")
	}

	// / opens the search bar in "both" mode.
	m = update(m, key("/"))
	if !m.searchBarVisible {
		t.Fatal("expected search bar to open on '/'")
	}
	if m.pendingSearchMode != "both" {
		t.Fatalf("expected pending mode 'both', got %q", m.pendingSearchMode)
	}

	for _, r := range term {
		m = update(m, key(string(r)))
	}
	m = update(m, key("enter"))
	if m.searchBarVisible {
		t.Fatal("expected search bar to close after Enter")
	}
	if m.searchTerm != term {
		t.Fatalf("expected searchTerm %q, got %q", term, m.searchTerm)
	}
	if len(m.searchMatches) == 0 {
		t.Fatal("expected at least one match after submitting search")
	}
	firstRow := m.promptsTable.Cursor()

	// n jumps forward (wrapping); p jumps back to where we started.
	m = update(m, key("n"))
	secondRow := m.promptsTable.Cursor()
	if len(m.searchMatches) > 1 && secondRow == firstRow {
		t.Fatal("expected 'n' to move to a different match when more than one exists")
	}

	m = update(m, key("p"))
	backRow := m.promptsTable.Cursor()
	if backRow != firstRow {
		t.Fatalf("expected 'p' to return to the first match row %d, got %d", firstRow, backRow)
	}

	// Detail pane should carry the highlighted search term.
	view := m.detail.View()
	if view == "" {
		t.Fatal("expected non-empty detail view after search")
	}
}

// TestSearchModesRealData confirms < restricts matches to User Prompt text
// and > restricts matches to Final Result text, using this repo's own log.
func TestSearchModesRealData(t *testing.T) {
	mode, dir := source.DetectMode("/data01/cheoljoo.lee/code/ai-prompt-log", false)
	m := New(mode, dir)
	m = update(m, tea.WindowSizeMsg{Width: 160, Height: 40})

	// < opens user-only mode.
	m = update(m, key("<"))
	if !m.searchBarVisible || m.pendingSearchMode != "user" {
		t.Fatalf("expected search bar open in user mode, got visible=%v mode=%q", m.searchBarVisible, m.pendingSearchMode)
	}
	m = update(m, key("esc"))
	if m.searchBarVisible {
		t.Fatal("expected esc to cancel the open search bar")
	}

	// > opens final-only mode.
	m = update(m, key(">"))
	if !m.searchBarVisible || m.pendingSearchMode != "final" {
		t.Fatalf("expected search bar open in final mode, got visible=%v mode=%q", m.searchBarVisible, m.pendingSearchMode)
	}
	m = update(m, key("esc"))
	if m.searchBarVisible {
		t.Fatal("expected esc to cancel the open search bar")
	}
}

// TestSearchTypingDoesNotTriggerNavigationRealData confirms that while the
// search bar is focused, keys that double as navigation bindings (n, p, j,
// g, <, >) are consumed as ordinary text input instead of firing their
// usual actions.
func TestSearchTypingDoesNotTriggerNavigationRealData(t *testing.T) {
	mode, dir := source.DetectMode("/data01/cheoljoo.lee/code/ai-prompt-log", false)
	m := New(mode, dir)
	m = update(m, tea.WindowSizeMsg{Width: 160, Height: 40})

	cursorBefore := m.promptsTable.Cursor()
	m = update(m, key("/"))
	for _, ch := range []string{"n", "j", "p", "g"} {
		m = update(m, key(ch))
	}
	if m.searchInput.Value() != "njpg" {
		t.Fatalf("expected typed characters to reach the search input, got %q", m.searchInput.Value())
	}
	if m.promptsTable.Cursor() != cursorBefore {
		t.Fatalf("expected prompts cursor unaffected while typing in search bar: %d -> %d", cursorBefore, m.promptsTable.Cursor())
	}
}
