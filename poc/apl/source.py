"""Log source layer: locates Claude Code session jsonl files.

Reference: docs/data-model.md
"""
from __future__ import annotations

import re
from pathlib import Path

CLAUDE_PROJECTS_DIR = Path.home() / ".claude" / "projects"


def encode_path(path: Path) -> str:
    """Mirror Claude Code's lossy cwd -> directory name encoding."""
    return re.sub(r"[^A-Za-z0-9]", "-", str(path))


def find_direct_project_dir(cwd: Path) -> Path:
    """Walk up from cwd to the nearest ancestor with recorded sessions.

    Claude Code logs sessions under the exact directory it was launched
    from - usually a project's root - not every subdirectory you `cd`
    into afterwards. Without this, running `apl` from e.g. `agents/`
    would show nothing even though the parent project has logs.
    """
    cur = cwd.resolve()
    for candidate in (cur, *cur.parents):
        d = CLAUDE_PROJECTS_DIR / encode_path(candidate)
        if d.is_dir():
            return d
    return CLAUDE_PROJECTS_DIR / encode_path(cur)


def detect_mode(cwd: Path, aggregate: bool = False, root_override: Path | None = None):
    """Return (mode, project_root_dir).

    mode == "aggregate": project_root_dir holds one subdir per project
        (~/.claude/projects itself, or root_override) -> 3-level view.
    mode == "direct": project_root_dir is the nearest ancestor's session
        directory (may not exist if truly nothing was ever logged) -> 2-level view.

    root_override lets a caller point the aggregate 3-pane view at a
    directory laid out like ~/.claude/projects but that isn't it -- e.g.
    `apl --view-backup`'s backup directory (see backup.py).
    """
    if root_override is not None:
        return "aggregate", root_override
    if aggregate:
        return "aggregate", CLAUDE_PROJECTS_DIR
    return "direct", find_direct_project_dir(cwd)


def list_session_files(project_dir: Path) -> list[Path]:
    if not project_dir.is_dir():
        return []
    return sorted(project_dir.glob("*.jsonl"))


def list_project_dirs(aggregate_root: Path) -> list[Path]:
    if not aggregate_root.is_dir():
        return []
    return sorted(p for p in aggregate_root.iterdir() if p.is_dir())
