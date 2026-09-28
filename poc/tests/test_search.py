import unittest

from apl import model, tui


def _prompt(user_text: str, final_text: str = "") -> model.Prompt:
    blocks = [model.AssistantBlock("text", text=final_text)] if final_text else []
    return model.Prompt(
        session_id="s1",
        timestamp="2026-09-28T00:00:00Z",
        branch="",
        sidechain=False,
        user_text=user_text,
        blocks=blocks,
    )


class TestPromptMatchesSearch(unittest.TestCase):
    def setUp(self):
        self.p_user_only = _prompt("please fix the login BUG", "all good now")
        self.p_final_only = _prompt("add a new feature", "fixed a Bug along the way")
        self.p_neither = _prompt("unrelated request", "unrelated response")

    def test_both_mode_matches_user_field(self):
        self.assertTrue(tui.prompt_matches_search(self.p_user_only, "bug", "both"))

    def test_both_mode_matches_final_field(self):
        self.assertTrue(tui.prompt_matches_search(self.p_final_only, "bug", "both"))

    def test_both_mode_no_match(self):
        self.assertFalse(tui.prompt_matches_search(self.p_neither, "bug", "both"))

    def test_user_mode_only_checks_user_text(self):
        self.assertTrue(tui.prompt_matches_search(self.p_user_only, "bug", "user"))
        self.assertFalse(tui.prompt_matches_search(self.p_final_only, "bug", "user"))

    def test_final_mode_only_checks_final_text(self):
        self.assertTrue(tui.prompt_matches_search(self.p_final_only, "bug", "final"))
        self.assertFalse(tui.prompt_matches_search(self.p_user_only, "bug", "final"))

    def test_case_insensitive(self):
        self.assertTrue(tui.prompt_matches_search(self.p_user_only, "BUG", "user"))
        self.assertTrue(tui.prompt_matches_search(self.p_user_only, "Bug", "both"))

    def test_empty_term_never_matches(self):
        self.assertFalse(tui.prompt_matches_search(self.p_user_only, "", "both"))


class TestHighlightRendering(unittest.TestCase):
    def test_highlight_wraps_all_occurrences_case_insensitively(self):
        rendered = tui._highlight("Bug here, another bug there", "bug")
        self.assertEqual(rendered.count("[reverse bold yellow]"), 2)
        self.assertIn("[reverse bold yellow]Bug[/reverse bold yellow]", rendered)
        self.assertIn("[reverse bold yellow]bug[/reverse bold yellow]", rendered)

    def test_highlight_no_term_just_escapes(self):
        rendered = tui._highlight("a [b] c", "")
        self.assertEqual(rendered, tui.escape("a [b] c"))

    def test_highlight_no_match_just_escapes(self):
        rendered = tui._highlight("nothing to see", "zzz")
        self.assertEqual(rendered, tui.escape("nothing to see"))


class TestFormatPromptDetailSearch(unittest.TestCase):
    def test_highlights_user_text_in_both_mode(self):
        p = _prompt("fix the bug please", "done, no more bug")
        out = tui.format_prompt_detail(p, search_term="bug", search_mode="both")
        # Both occurrences (user + final) should be highlighted in "both" mode.
        self.assertEqual(out.count("[reverse bold yellow]"), 2)

    def test_highlights_only_user_text_in_user_mode(self):
        p = _prompt("fix the bug please", "done, no more bug")
        out = tui.format_prompt_detail(p, search_term="bug", search_mode="user")
        self.assertEqual(out.count("[reverse bold yellow]"), 1)

    def test_highlights_only_final_text_in_final_mode(self):
        p = _prompt("fix the bug please", "done, no more bug")
        out = tui.format_prompt_detail(p, search_term="bug", search_mode="final")
        self.assertEqual(out.count("[reverse bold yellow]"), 1)

    def test_no_search_term_no_highlighting(self):
        p = _prompt("fix the bug please", "done, no more bug")
        out = tui.format_prompt_detail(p)
        self.assertNotIn("[reverse bold yellow]", out)


class FakeDataTable:
    """Minimal stand-in for textual.widgets.DataTable, only implementing the
    bits AplScreen's search logic touches (cursor_row, move_cursor,
    border_subtitle), so the next/prev wraparound logic can be unit tested
    without booting a real Textual App/Screen."""

    def __init__(self, cursor_row: int = 0):
        self.cursor_row = cursor_row
        self.border_subtitle = ""

    def move_cursor(self, row: int) -> None:
        self.cursor_row = row


class TestSearchNavigation(unittest.TestCase):
    """Exercises AplScreen's next/prev match wraparound math directly,
    without going through query_one()/a mounted app."""

    def _screen_stub(self, matches, cursor_row, term="bug", mode="both"):
        screen = tui.AplScreen.__new__(tui.AplScreen)
        screen._search_term = term
        screen._search_mode = mode
        screen._search_matches = matches
        table = FakeDataTable(cursor_row=cursor_row)
        screen.query_one = lambda selector, *a, **k: table  # type: ignore[method-assign]
        return screen, table

    def test_next_from_before_first_match(self):
        screen, table = self._screen_stub(matches=[2, 5, 8], cursor_row=0)
        screen.action_search_next()
        self.assertEqual(table.cursor_row, 2)

    def test_next_wraps_after_last_match(self):
        screen, table = self._screen_stub(matches=[2, 5, 8], cursor_row=8)
        screen.action_search_next()
        self.assertEqual(table.cursor_row, 2)

    def test_next_from_middle(self):
        screen, table = self._screen_stub(matches=[2, 5, 8], cursor_row=3)
        screen.action_search_next()
        self.assertEqual(table.cursor_row, 5)

    def test_prev_from_after_last_match(self):
        screen, table = self._screen_stub(matches=[2, 5, 8], cursor_row=9)
        screen.action_search_prev()
        self.assertEqual(table.cursor_row, 8)

    def test_prev_wraps_before_first_match(self):
        screen, table = self._screen_stub(matches=[2, 5, 8], cursor_row=2)
        screen.action_search_prev()
        self.assertEqual(table.cursor_row, 8)

    def test_prev_from_middle(self):
        screen, table = self._screen_stub(matches=[2, 5, 8], cursor_row=6)
        screen.action_search_prev()
        self.assertEqual(table.cursor_row, 5)

    def test_no_matches_is_noop(self):
        screen, table = self._screen_stub(matches=[], cursor_row=3)
        screen.action_search_next()
        self.assertEqual(table.cursor_row, 3)
        screen.action_search_prev()
        self.assertEqual(table.cursor_row, 3)

    def test_border_subtitle_shows_position_and_prefix(self):
        screen, table = self._screen_stub(matches=[2, 5, 8], cursor_row=0, term="foo", mode="user")
        screen.action_search_next()
        self.assertEqual(table.border_subtitle, "<foo  (1/3)")
        screen.action_search_next()
        self.assertEqual(table.border_subtitle, "<foo  (2/3)")


if __name__ == "__main__":
    unittest.main()
