"""Entry point: `apl` launches the TUI.

`apl`       -> 2 panes, this project's own prompts only
`apl --all` -> 3 panes, every project under ~/.claude/projects
"""
from __future__ import annotations

import argparse

GIT_URL = "https://github.com/cheoljoo/ai-prompt-log"

KEYBINDINGS_HELP = """\
Keybindings (vi-style):
  j/k, up/down     move within the focused pane
  g/G              jump to top / bottom
  Ctrl+F/Ctrl+B    page down / up
  Ctrl+D/Ctrl+U    half page down / up
  l, Tab, Enter    focus next pane (drill in)
  h, Shift+Tab, Esc  focus previous pane (back)
  F1               command palette (theme, screenshot, quit, ...)
  q                quit
"""


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="apl",
        description=(
            "apl (AI Prompt Log viewer) - a tig-style TUI for browsing "
            "Claude Code session logs, read directly from "
            "~/.claude/projects (no copying).\n\n"
            "Plain `apl` shows just the current project's own prompts "
            "(2 panes: Prompts | Detail). `apl --all` shows every project "
            "at once (3 panes: Projects | Prompts | Detail)."
        ),
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=f"{KEYBINDINGS_HELP}\nSource: {GIT_URL}",
    )
    parser.add_argument(
        "-a",
        "--all",
        action="store_true",
        help="Browse every project under ~/.claude/projects (3-pane aggregate view)",
    )
    return parser


def main() -> None:
    args = build_parser().parse_args()
    from .tui import AplApp

    AplApp(aggregate=args.all).run()


if __name__ == "__main__":
    main()
