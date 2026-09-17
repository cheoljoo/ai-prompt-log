"""Durable backup layer: copies session jsonl files out of
~/.claude/projects into a location independent of any one project's
lifetime, so deleting a project directory doesn't lose its prompt history.

Mirrors the same <encoded-cwd>/*.jsonl layout as source.CLAUDE_PROJECTS_DIR
so model.py's existing loading code works against the backup unchanged --
just point project_dir/aggregate_root at the backup dir instead.

Incremental-copy logic (mtime + size) is carried over from the now-deleted
poc/apl/sync.py (see `git show c7200c4:poc/apl/sync.py`). The one
deliberate difference from that ephemeral cache: this backup never deletes
anything -- it is an append-only archive.
"""
from __future__ import annotations

import shutil
from dataclasses import dataclass
from pathlib import Path

from . import model, source

DEFAULT_BACKUP_DIR = Path.home() / "ai-prompt-log.backup"


@dataclass
class BackupStats:
    projects: int = 0
    copied: int = 0
    updated: int = 0
    unchanged: int = 0


def resolve_depth_root(cwd: Path, depth: int) -> Path:
    """Walk up `depth` directories from cwd. depth=0 -> cwd itself."""
    cur = cwd.resolve()
    for _ in range(depth):
        if cur.parent == cur:
            break
        cur = cur.parent
    return cur


def _is_root_or_descendant(cwd_field: str, root: Path) -> bool:
    try:
        p = Path(cwd_field).resolve()
    except (OSError, ValueError):
        return False
    return p == root or root in p.parents


def find_projects_by_cwd_ancestry(
    root: Path, aggregate_root: Path = source.CLAUDE_PROJECTS_DIR
) -> list[Path]:
    """Every project dir under aggregate_root whose real (jsonl) cwd field
    is `root` itself or a descendant of it.

    Deliberately does not decode the encoded directory name (lossy, see
    docs/data-model.md) -- it reads each project's own recorded `cwd`.
    """
    matches = []
    for proj_dir in source.list_project_dirs(aggregate_root):
        cwd_field = model._find_cwd_field(proj_dir)
        if cwd_field and _is_root_or_descendant(cwd_field, root):
            matches.append(proj_dir)
    return matches


def select_projects(cwd: Path, depth: int | None) -> list[Path]:
    """Which ~/.claude/projects subdirectories `apl --backup` should copy.

    depth is None (no --depth given) -> just the current project, same
    nearest-ancestor resolution as direct-mode viewing.
    depth is an int -> every project whose real cwd is at or below the
    directory `depth` levels above cwd.
    """
    if depth is None:
        d = source.find_direct_project_dir(cwd)
        return [d] if d.is_dir() else []
    root = resolve_depth_root(cwd, depth)
    return find_projects_by_cwd_ancestry(root)


def copy_project(src_dir: Path, dest_root: Path) -> tuple[int, int, int]:
    """Incrementally copy one project's *.jsonl into dest_root/<same-name>/.

    Returns (copied, updated, unchanged). Never deletes anything from the
    destination, even if it no longer matches the source directory.
    """
    dest_dir = dest_root / src_dir.name
    copied = updated = unchanged = 0
    for f in source.list_session_files(src_dir):
        dest_f = dest_dir / f.name
        src_stat = f.stat()
        if dest_f.exists():
            dest_stat = dest_f.stat()
            if dest_stat.st_mtime >= src_stat.st_mtime and dest_stat.st_size == src_stat.st_size:
                unchanged += 1
                continue
            shutil.copy2(f, dest_f)
            updated += 1
            continue
        dest_dir.mkdir(parents=True, exist_ok=True)
        shutil.copy2(f, dest_f)
        copied += 1
    return copied, updated, unchanged


def run_backup(cwd: Path, depth: int | None, backup_dir: Path) -> BackupStats:
    backup_dir.mkdir(parents=True, exist_ok=True)
    project_dirs = select_projects(cwd, depth)
    stats = BackupStats(projects=len(project_dirs))
    for proj_dir in project_dirs:
        copied, updated, unchanged = copy_project(proj_dir, backup_dir)
        stats.copied += copied
        stats.updated += updated
        stats.unchanged += unchanged
    return stats


def format_summary(stats: BackupStats, backup_dir: Path) -> str:
    return (
        f"apl backup: {stats.projects} project(s), "
        f"{stats.copied} copied, {stats.updated} updated, "
        f"{stats.unchanged} unchanged -> {backup_dir}"
    )
