"""Entry point: `apl` launches the TUI (or runs a one-shot backup).

`apl`               -> 2 panes, this project's own prompts only
`apl --all`         -> 3 panes, every project under ~/.claude/projects
`apl --backup`      -> copy session jsonl files into a durable backup dir
`apl --view-backup` -> 3 panes, reading from that backup dir instead
"""
from __future__ import annotations

import argparse
import sys
from importlib.metadata import PackageNotFoundError, version as _pkg_version
from pathlib import Path

GIT_URL = "https://github.com/cheoljoo/ai-prompt-log"

KEYBINDINGS_HELP = """\
Keybindings (vi-style):
  j/k, up/down     move within the focused pane
  g/G              jump to top / bottom
  Ctrl+F/Ctrl+B/Space  page down / up
  Ctrl+D/Ctrl+U    half page down / up
  l, Tab, Enter    focus next pane (drill in)
  h, Shift+Tab, Esc  focus previous pane (back)
  F1               command palette (theme, screenshot, quit, ...)
  q                quit
"""


def _version() -> str:
    try:
        return _pkg_version("apl")
    except PackageNotFoundError:
        return "dev"


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="apl",
        description=(
            "apl (AI Prompt Log viewer) - a tig-style TUI for browsing "
            "Claude Code session logs, read directly from "
            "~/.claude/projects (no copying).\n\n"
            "Plain `apl` shows just the current project's own prompts "
            "(2 panes: Prompts | Detail). `apl --all` shows every project "
            "at once (3 panes: Projects | Prompts | Detail).\n\n"
            "`apl --backup` copies session logs into a durable backup "
            "directory (outside ~/.claude/projects, so it survives a "
            "project directory being deleted). `apl --view-backup` browses "
            "that backup with the same 3-pane view."
        ),
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=f"{KEYBINDINGS_HELP}\napl {_version()}\nSource: {GIT_URL}",
    )
    parser.add_argument(
        "-v",
        "--version",
        action="version",
        version=f"apl {_version()}",
    )
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument(
        "-a",
        "--all",
        action="store_true",
        help="Browse every project under ~/.claude/projects (3-pane aggregate view)",
    )
    mode.add_argument(
        "--backup",
        action="store_true",
        help=(
            "Copy session jsonl files into --backup-dir (default "
            "~/ai-prompt-log.backup/), incrementally, and exit. Never "
            "deletes anything already in the backup."
        ),
    )
    mode.add_argument(
        "--view-backup",
        action="store_true",
        help="Browse a previous --backup (3-pane aggregate view, rooted at --backup-dir)",
    )
    parser.add_argument(
        "--depth",
        type=int,
        default=None,
        metavar="N",
        help=(
            "With --backup: walk up N directories from cwd and back up "
            "every project whose real cwd is that directory or a "
            "descendant of it. Omit to back up only the current project."
        ),
    )
    parser.add_argument(
        "--backup-dir",
        type=Path,
        default=None,
        metavar="PATH",
        help="Backup directory for --backup / --view-backup (default: ~/ai-prompt-log.backup/)",
    )
    return parser


def main() -> None:
    parser = build_parser()
    args = parser.parse_args()

    if args.depth is not None and not args.backup:
        parser.error("--depth only makes sense with --backup")
    if args.backup_dir is not None and not (args.backup or args.view_backup):
        parser.error("--backup-dir only makes sense with --backup or --view-backup")

    from . import backup as backup_mod

    backup_dir = args.backup_dir or backup_mod.DEFAULT_BACKUP_DIR

    if args.backup:
        stats = backup_mod.run_backup(Path.cwd(), args.depth, backup_dir)
        print(backup_mod.format_summary(stats, backup_dir))
        if stats.projects == 0:
            print(
                "no matching project found under ~/.claude/projects "
                "for this directory",
                file=sys.stderr,
            )
        return

    from .tui import AplApp

    if args.view_backup:
        AplApp(root_override=backup_dir).run()
    else:
        AplApp(aggregate=args.all).run()


if __name__ == "__main__":
    main()
