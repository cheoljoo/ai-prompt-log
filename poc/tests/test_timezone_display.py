"""Regression tests for local-timezone timestamp display.

All prompt/session timestamps are parsed/stored/sorted as UTC ("Z"-suffixed
ISO8601) -- see docs/data-model.md -- which is correct for internal
consistency. But displaying that raw UTC string as-is (just string-sliced)
is a real bug: a prompt made at 13:51 KST shows up as "...T04:51:38" with
no timezone marker, easily misread as 4:51am local time (this is exactly
how a real user found the bug: "저는 한국시간으로 새벽 4시에 일을 한 적이
없습니다" -- "I never worked at 4am Korean time").

These tests pin down the fix: _local_ts()/_local_ts_full() must convert to
the system's local timezone before formatting for display.
"""
import unittest
from datetime import datetime, timedelta, timezone

from apl import tui


class TestLocalTimestampDisplay(unittest.TestCase):
    def test_utc_converts_to_local_wall_clock(self):
        # 04:51:38 UTC is NOT "4:51am" in every timezone -- it must be
        # shifted by the system's local UTC offset, not shown verbatim.
        local_offset = datetime.now().astimezone().utcoffset()
        expected = (
            datetime(2026, 9, 23, 4, 51, 38, tzinfo=timezone.utc) + local_offset
        )
        got = tui._local_ts("2026-09-23T04:51:38Z")
        self.assertEqual(got, expected.strftime("%Y-%m-%dT%H:%M:%S"))
        # Sanity: unless the local system happens to be UTC+0, the display
        # must NOT be the raw "04:51:38" (guards against a no-op "fix").
        if local_offset != timedelta(0):
            self.assertNotIn("04:51:38", got)

    def test_full_variant_includes_utc_offset(self):
        got = tui._local_ts_full("2026-09-23T04:51:38Z")
        # Must be self-evidently non-UTC: either a "+HH:MM"/"-HH:MM" offset,
        # or (only if the local system truly is UTC) a "+00:00" is fine too
        # -- the point is the offset must be *present*, unlike the old
        # behavior which showed the raw string with no offset at all.
        self.assertRegex(got, r"[+-]\d{2}:\d{2}$")

    def test_empty_timestamp_renders_as_placeholder(self):
        self.assertEqual(tui._local_ts(""), "-")
        self.assertEqual(tui._local_ts_full(""), "")

    def test_unparseable_timestamp_falls_back_gracefully(self):
        # Never raise on garbage input -- fall back to a slice of the raw
        # string rather than crashing the TUI.
        self.assertEqual(tui._local_ts("not-a-timestamp"), "not-a-timestamp")

    def test_local_ts_is_19_chars_like_the_old_raw_slice(self):
        # Table columns relied on a compact, fixed-shape "YYYY-MM-DDTHH:MM:SS"
        # (19 chars) -- the local-time conversion must preserve that shape.
        got = tui._local_ts("2026-09-23T04:51:38.123456Z")
        self.assertEqual(len(got), 19)


if __name__ == "__main__":
    unittest.main()
