import json
import shutil
import tempfile
import unittest
from pathlib import Path

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


class TestSearchModalIsCenteredAndVisible(unittest.IsolatedAsyncioTestCase):
    """Confirms the search dialog is a centered, high-contrast overlay (not
    an easy-to-miss line docked at the bottom next to the Footer), and that
    typed characters render with a clearly visible (non-default) style."""

    def setUp(self):
        self.tmpdir = Path(tempfile.mkdtemp())
        self.backup_root = self.tmpdir / "backup"
        proj_dir = self.backup_root / "my-project"
        proj_dir.mkdir(parents=True)
        f = proj_dir / "s1.jsonl"
        f.write_text(
            json.dumps(
                {
                    "type": "user",
                    "sessionId": "c1",
                    "timestamp": "2026-09-01T10:00:00Z",
                    "message": {"content": "fix the login bug"},
                }
            )
            + "\n"
        )

    def tearDown(self):
        shutil.rmtree(self.tmpdir)

    async def test_modal_is_centered_and_text_is_high_contrast(self):
        from apl.tui import AplApp

        app = AplApp(root_override=self.backup_root)
        async with app.run_test(size=(100, 40)) as pilot:
            screen = app.screen
            overlay = screen.query_one("#search-overlay")
            self.assertFalse(overlay.display, "search overlay should start hidden")

            await pilot.press("/")
            await pilot.pause()
            self.assertTrue(overlay.display, "search overlay should open on '/'")

            for ch in "bug":
                await pilot.press(ch)
            await pilot.pause()

            box = screen.query_one("#search-box")
            # The box should be roughly centered on both axes (within 2
            # cells, to allow for even/odd rounding).
            box_mid_x = box.region.x + box.region.width / 2
            box_mid_y = box.region.y + box.region.height / 2
            self.assertAlmostEqual(box_mid_x, screen.size.width / 2, delta=2)
            self.assertAlmostEqual(box_mid_y, screen.size.height / 2, delta=2)

            inp = screen.query_one("#search-input")
            strip = inp.render_line(0)
            typed_segments = [seg for seg in strip if seg.text.strip() == "bug"]
            self.assertTrue(typed_segments, "expected the typed 'bug' text to be rendered")
            style = typed_segments[0].style
            # High-contrast, explicit (non-transparent/non-default) colors:
            # bold, and a light foreground clearly distinct from the dark
            # background -- guards against the text blending invisibly into
            # its background regardless of the active theme/terminal.
            self.assertTrue(style.bold)
            self.assertIsNotNone(style.color)
            self.assertIsNotNone(style.bgcolor)
            self.assertNotEqual(style.color.get_truecolor(), style.bgcolor.get_truecolor())

            # Regression guard: Textual's Input widget defaults to its own
            # 3-row-tall bordered box (border: tall ...; height: 3;). If
            # #search-input's border/height ever stop being overridden to
            # "none"/1, the widget renders 3 rows tall instead of 1 --
            # overflowing past our single-line #search-row and corrupting
            # the outer box's bottom border/padding, reproducing the "blue
            # box inside the yellow box isn't fully visible" bug (even
            # though its bottom edge can still land inside the outer box's
            # bounding rectangle by coincidence, so a containment check
            # alone wouldn't catch this -- assert the exact height instead).
            self.assertEqual(
                inp.region.height,
                1,
                "search-input must render as a single line; Textual's Input "
                "defaults to a bordered 3-line box unless height/border are "
                "explicitly overridden",
            )


if __name__ == "__main__":
    unittest.main()
