package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// Regression tests for local-timezone timestamp display.
//
// All prompt/session timestamps are parsed/stored/sorted as UTC ("Z"-
// suffixed ISO8601) -- see docs/data-model.md -- which is correct for
// internal consistency. But displaying that raw UTC string as-is (just
// string-sliced) is a real bug: a prompt made at 13:51 KST shows up as
// "...T04:51:38" with no timezone marker, easily misread as 4:51am local
// time (this is exactly how a real user found the bug: "저는 한국시간으로
// 새벽 4시에 일을 한 적이 없습니다" -- "I never worked at 4am Korean time").
//
// These tests pin down the fix: localTS()/localTSFull() must convert to
// the system's local timezone before formatting for display.

func TestLocalTSConvertsUTCToLocalWallClock(t *testing.T) {
	utc, err := time.Parse(time.RFC3339, "2026-09-23T04:51:38Z")
	if err != nil {
		t.Fatal(err)
	}
	want := utc.Local().Format("2006-01-02T15:04:05")

	got := localTS("2026-09-23T04:51:38Z")
	if got != want {
		t.Fatalf("localTS(...) = %q, want %q (converted to local wall clock)", got, want)
	}

	// Sanity: unless the local system happens to be UTC+0, the display
	// must NOT be the raw "04:51:38" (guards against a no-op "fix").
	_, offset := time.Now().Zone()
	if offset != 0 && strings.Contains(got, "04:51:38") {
		t.Fatalf("localTS(...) still shows the raw UTC time %q; expected conversion to local time", got)
	}
}

func TestLocalTSFullIncludesUTCOffset(t *testing.T) {
	got := localTSFull("2026-09-23T04:51:38Z")
	// Must be self-evidently non-UTC: a trailing "+HH:MM"/"-HH:MM" offset
	// must be present, unlike the old behavior which rendered the raw
	// string with no offset at all.
	if !regexp.MustCompile(`[+-]\d{2}:\d{2}$`).MatchString(got) {
		t.Fatalf("localTSFull(...) = %q, expected a trailing UTC offset like +09:00", got)
	}
}

func TestLocalTSEmptyRendersPlaceholder(t *testing.T) {
	if got := localTS(""); got != "-" {
		t.Fatalf("localTS(\"\") = %q, want \"-\"", got)
	}
	if got := localTSFull(""); got != "" {
		t.Fatalf("localTSFull(\"\") = %q, want \"\"", got)
	}
}

func TestLocalTSUnparseableFallsBackGracefully(t *testing.T) {
	// Never panic on garbage input -- fall back to the raw string rather
	// than crashing the TUI.
	if got := localTS("not-a-timestamp"); got != "not-a-timestamp" {
		t.Fatalf("localTS(garbage) = %q, want the raw string back", got)
	}
}

func TestLocalTSIs19CharsLikeTheOldRawSlice(t *testing.T) {
	// Table columns relied on a compact, fixed-shape "YYYY-MM-DDTHH:MM:SS"
	// (19 chars) -- the local-time conversion must preserve that shape.
	got := localTS("2026-09-23T04:51:38.123456Z")
	if len(got) != len("2026-09-23T04:51:38") {
		t.Fatalf("localTS(...) = %q (len %d), want 19 chars", got, len(got))
	}
}
