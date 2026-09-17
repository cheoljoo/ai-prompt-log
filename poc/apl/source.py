"""Log source layer: locates Claude Code session jsonl files.

Reference: docs/data-model.md
"""
from __future__ import annotations

import re
from pathlib import Path

CLAUDE_PROJECTS_DIR = Path.home() / ".claude" / "projects"
CACHE_DIRNAME = ".ai-prompt-log-cache"


def encode_path(path: Path) -> str:
    """Mirror Claude Code's lossy cwd -> directory name encoding."""
    return re.sub(r"[^A-Za-z0-9]", "-", str(path))


def find_repo_root(start: Path) -> Path:
    cur = start.resolve()
    for parent in [cur, *cur.parents]:
        if (parent / ".git").exists():
            return parent
    return cur


def detect_mode(cwd: Path):
    """Return (mode, project_root_dir).

    mode == "aggregate": project_root_dir holds one subdir per project (mirrors
        the ~/.claude/projects layout) -> 3-level view.
    mode == "direct": project_root_dir is this project's own session directory
        (may not exist yet) -> 2-level view.
    """
    root = find_repo_root(cwd)
    cache_dir = root / CACHE_DIRNAME
    if cache_dir.is_dir():
        return "aggregate", cache_dir
    return "direct", CLAUDE_PROJECTS_DIR / encode_path(cwd.resolve())


def list_session_files(project_dir: Path) -> list[Path]:
    if not project_dir.is_dir():
        return []
    return sorted(project_dir.glob("*.jsonl"))


def list_project_dirs(aggregate_root: Path) -> list[Path]:
    if not aggregate_root.is_dir():
        return []
    return sorted(p for p in aggregate_root.iterdir() if p.is_dir())
