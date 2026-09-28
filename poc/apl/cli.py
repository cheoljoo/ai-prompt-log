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
            "Claude Code, Antigravity CLI (agy / gemini), OpenCode, and "
            "GitHub Copilot CLI session logs, read directly from "
            "~/.claude/projects, ~/.gemini/antigravity-cli, "
            "~/.local/share/opencode, and ~/.copilot/session-state "
            "(no copying).\n\n"
            "Plain `apl` shows just the current project's own prompts "
            "(2 panes: Prompts | Detail). `apl --all` shows every project "
            "at once (3 panes: Projects | Prompts | Detail).\n\n"
            "`apl --backup` copies session logs into a durable backup "
            "directory (outside live directories, so it survives a "
            "project directory being deleted). `apl --view-backup` browses "
            "that backup with the same 3-pane view.\n\n"
            "`apl --save` writes the current view (respecting --all/"
            "--source/--since/--days) to a JSON file -- start/end time, "
            "user prompt, final result, and modified files per prompt -- "
            "then exits. Without --save-file, always writes to "
            "apl-save.json in the current directory (overwritten each "
            "run)."
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
        help="Browse every project (3-pane aggregate view)",
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
    parser.add_argument(
        "--save",
        action="store_true",
        help=(
            "Save the current view (respecting --all/--source filters, "
            "--since/--days) to a JSON file -- start/end time, user "
            "prompt, final result, and modified files per prompt -- "
            "then exit."
        ),
    )
    parser.add_argument(
        "--save-file",
        type=Path,
        default=None,
        metavar="PATH",
        help="Output path for --save (default: apl-save.json in the current directory, overwritten each time)",
    )
    since_grp = parser.add_mutually_exclusive_group()
    since_grp.add_argument(
        "--since",
        default=None,
        metavar="YYYY-MM-DD",
        help=(
            "With --save: only include prompts on/after this date. "
            "Default: no date filtering (include everything)."
        ),
    )
    since_grp.add_argument(
        "--days",
        type=int,
        default=None,
        metavar="N",
        help=(
            "With --save: only include prompts from the last N days. "
            "Default: no date filtering (include everything)."
        ),
    )

    source_grp = parser.add_mutually_exclusive_group()
    source_grp.add_argument(
        "-s",
        "--source",
        choices=["all", "claude", "agy", "gemini", "opencode", "copilot"],
        default=None,
        help="AI assistant log source to display (default: all available)",
    )
    source_grp.add_argument(
        "--agy",
        action="store_true",
        help="Display only Antigravity CLI (agy) logs",
    )
    source_grp.add_argument(
        "--claude",
        action="store_true",
        help="Display only Claude Code logs",
    )
    source_grp.add_argument(
        "--gemini",
        action="store_true",
        help="Display only Antigravity / Gemini CLI logs (alias for --agy)",
    )
    source_grp.add_argument(
        "--opencode",
        action="store_true",
        help="Display only OpenCode logs",
    )
    source_grp.add_argument(
        "--copilot",
        action="store_true",
        help="Display only GitHub Copilot CLI logs",
    )

    return parser


def main() -> None:
    parser = build_parser()
    args = parser.parse_args()

    if args.depth is not None and not args.backup:
        parser.error("--depth only makes sense with --backup")
    if args.backup_dir is not None and not (args.backup or args.view_backup):
        parser.error("--backup-dir only makes sense with --backup or --view-backup")
    if args.save and (args.backup or args.view_backup):
        parser.error("--save is mutually exclusive with --backup and --view-backup")
    if args.save_file is not None and not args.save:
        parser.error("--save-file only makes sense with --save")
    if (args.since or args.days is not None) and not args.save:
        parser.error("--since/--days only make sense with --save")

    if args.agy or args.gemini:
        source_filter = "agy"
    elif args.claude:
        source_filter = "claude"
    elif args.opencode:
        source_filter = "opencode"
    elif args.copilot:
        source_filter = "copilot"
    elif args.source:
        source_filter = "agy" if args.source == "gemini" else args.source
    else:
        source_filter = "all"

    from . import backup as backup_mod

    backup_dir = args.backup_dir or backup_mod.DEFAULT_BACKUP_DIR

    if args.backup:
        stats = backup_mod.run_backup(
            Path.cwd(), args.depth, backup_dir, source_filter=source_filter
        )
        print(backup_mod.format_summary(stats, backup_dir))
        if stats.projects == 0:
            print(
                "no matching project found for this directory",
                file=sys.stderr,
            )
        return

    if args.save:
        _run_save(args, source_filter)
        return

    from .tui import AplApp

    if args.view_backup:
        AplApp(root_override=backup_dir, source_filter=source_filter).run()
    else:
        AplApp(aggregate=args.all, source_filter=source_filter).run()


def _run_save(args: argparse.Namespace, source_filter: str) -> None:
    import json
    from datetime import datetime, timedelta, timezone

    from . import model, source as source_mod

    since = None
    if args.since:
        try:
            since = datetime.strptime(args.since, "%Y-%m-%d")
        except ValueError:
            print(f"invalid --since date: {args.since!r} (expected YYYY-MM-DD)", file=sys.stderr)
            sys.exit(1)
    elif args.days is not None:
        since = datetime.now(timezone.utc) - timedelta(days=args.days)

    mode, root = source_mod.detect_mode(
        Path.cwd(), aggregate=args.all, source_filter=source_filter
    )
    if mode == "aggregate":
        projects = model.load_projects(root, source_filter=source_filter)
    else:
        projects = [model.load_project(root, source_filter=source_filter)]

    doc = model.build_save_document(projects, source_filter, since=since)

    save_path = args.save_file
    if save_path is None:
        save_path = Path("apl-save.json")

    with save_path.open("w") as fh:
        json.dump(doc, fh, indent=2, ensure_ascii=False)
        fh.write("\n")

    total_prompts = sum(p["prompt_count"] for p in doc["projects"])
    print(
        f"apl save: {len(doc['projects'])} project(s), {total_prompts} prompt(s) -> {save_path}"
    )
    if total_prompts == 0:
        print("no matching prompts for the given filters", file=sys.stderr)


if __name__ == "__main__":
    main()
