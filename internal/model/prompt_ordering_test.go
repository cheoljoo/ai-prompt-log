package model

import (
	"os"
	"path/filepath"
	"testing"
)

// Regression tests for a real user-reported bug: an old ("agy") session
// file appeared ABOVE much more recent ("claude") prompts at the very top
// of the Prompts list, because BuildPromptsForFiles only sorted whole
// FILES by a per-file heuristic timestamp and then concatenated each
// file's prompts verbatim -- so a file whose heuristic timestamp diverges
// from its actual prompt content's timestamps (or that spans a huge time
// range because it kept getting reopened/appended to) could strand its
// prompts far from where they chronologically belong. The fix re-sorts
// the merged list by each individual Prompt's own timestamp.

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOldSessionNoLongerStrandsAboveRecentOnes(t *testing.T) {
	dir := t.TempDir()

	// An old AGY session (Aug 6) whose transcript's own first line is a
	// metadata/session-id record with NO real timestamp field at all (as
	// apl --backup's synthesized "agy_metadata" header is, and as some
	// real Antigravity transcripts' leading records can be) -- the
	// file-level heuristic can't find a timestamp on that line and must
	// fall through to the real (old) prompt content below it.
	oldAgy := writeTestFile(t, dir, "old_agy.jsonl",
		`{"type":"agy_metadata","sessionId":"79ce52e6-5e7b-40e2-b77c-29aa8dcb3ac6"}`+"\n"+
			`{"step_index":0,"type":"USER_INPUT","source":"USER_EXPLICIT","created_at":"2026-08-06T12:41:56Z","content":"<USER_REQUEST>old agy prompt</USER_REQUEST>"}`+"\n")

	recentClaude := writeTestFile(t, dir, "recent_claude.jsonl",
		`{"type":"user","sessionId":"c1","timestamp":"2026-09-28T15:06:17Z","message":{"content":"recent claude prompt"}}`+"\n")

	prompts := BuildPromptsForFiles([]string{oldAgy, recentClaude}, nil)
	if len(prompts) != 2 {
		t.Fatalf("expected 2 prompts, got %d: %+v", len(prompts), prompts)
	}
	// Ascending (oldest-first) order: the Aug agy prompt must sort BEFORE
	// the Sept claude prompt, regardless of file iteration order or which
	// file "looks newer" by a naive per-file heuristic.
	if prompts[0].Source != "agy" || prompts[0].UserText != "old agy prompt" {
		t.Fatalf("expected prompts[0] to be the old agy prompt, got %+v", prompts[0])
	}
	if prompts[1].Source != "claude" || prompts[1].UserText != "recent claude prompt" {
		t.Fatalf("expected prompts[1] to be the recent claude prompt, got %+v", prompts[1])
	}
}

func TestLongLivedFilePromptsInterleaveWithOtherFiles(t *testing.T) {
	dir := t.TempDir()

	// A single file that started early but kept getting appended to over
	// days (e.g. a long-running OpenCode/Claude session) must not drag
	// its LATER prompts to the front just because its FIRST prompt's
	// timestamp made the whole file sort early.
	longLived := writeTestFile(t, dir, "long_lived.jsonl",
		`{"type":"user","sessionId":"s1","timestamp":"2026-09-23T04:51:38Z","message":{"content":"day1 prompt"}}`+"\n"+
			`{"type":"user","sessionId":"s1","timestamp":"2026-09-28T10:35:54Z","message":{"content":"day5 prompt"}}`+"\n")

	shortLived := writeTestFile(t, dir, "short_lived.jsonl",
		`{"type":"user","sessionId":"s2","timestamp":"2026-09-23T05:02:09Z","message":{"content":"same-day other session"}}`+"\n")

	prompts := BuildPromptsForFiles([]string{longLived, shortLived}, nil)
	want := []string{"day1 prompt", "same-day other session", "day5 prompt"}
	if len(prompts) != len(want) {
		t.Fatalf("expected %d prompts, got %d: %+v", len(want), len(prompts), prompts)
	}
	for i, w := range want {
		if prompts[i].UserText != w {
			t.Fatalf("prompts[%d].UserText = %q, want %q (full order: %v)", i, prompts[i].UserText, w, promptTexts(prompts))
		}
	}
}

func promptTexts(prompts []Prompt) []string {
	texts := make([]string, len(prompts))
	for i, p := range prompts {
		texts[i] = p.UserText
	}
	return texts
}
