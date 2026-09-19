"""Durable backup layer: copies session jsonl files out of live agent locations
(~/.claude/projects, ~/.gemini/antigravity-cli, ~/.gemini/tmp) into a location
independent of any one project's lifetime, so deleting a project directory
doesn't lose its prompt history.

Mirrors the <encoded-cwd>/*.jsonl layout so model.py's loading code works
against the backup unchanged -- just point project_dir/aggregate_root at the
backup dir instead.
"""
from __future__ import annotations

import json
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
    root: Path, source_filter: str = "all"
) -> list[model.Project]:
    """Every project whose real cwd is `root` itself or a descendant of it."""
    matches = []
    for proj in model.load_projects(source_filter=source_filter):
        if proj.cwd and _is_root_or_descendant(proj.cwd, root):
            matches.append(proj)
    return matches


def select_projects(
    cwd: Path, depth: int | None, source_filter: str = "all"
) -> list[model.Project]:
    """Which projects `apl --backup` should copy."""
    if depth is None:
        p = model.load_project(cwd, source_filter=source_filter)
        return [p] if p.session_files else []
    root = resolve_depth_root(cwd, depth)
    return find_projects_by_cwd_ancestry(root, source_filter=source_filter)


def copy_project(proj: model.Project, dest_root: Path) -> tuple[int, int, int]:
    """Incrementally copy one project's session files into dest_root/<encoded-cwd>/.

    Returns (copied, updated, unchanged). Never deletes anything from the destination.
    """
    dest_dir = dest_root / source.encode_path(Path(proj.cwd))
    copied = updated = unchanged = 0

    for f in proj.session_files:
        fmt = model.detect_file_format(f)

        if fmt == "agy":
            conv_id = (
                proj.conv_metadata.get(f.stem, {}).get("id")
                or (
                    f.parent.parent.parent.name
                    if f.parent.name == "logs"
                    else f.stem
                )
            )
            if conv_id.startswith("agy-"):
                conv_id = conv_id[4:]
            dest_f = dest_dir / f"agy-{conv_id}.jsonl"
            existed = dest_f.exists()
            if existed and dest_f.stat().st_mtime >= f.stat().st_mtime:
                unchanged += 1
                continue
            dest_dir.mkdir(parents=True, exist_ok=True)
            meta = {
                "type": "agy_metadata",
                "cwd": proj.cwd,
                "sessionId": conv_id,
                "gitBranch": proj.conv_metadata.get(conv_id, {}).get("branch", ""),
            }
            with dest_f.open("w", errors="ignore") as out_fh:
                out_fh.write(json.dumps(meta) + "\n")
                with f.open("r", errors="ignore") as in_fh:
                    shutil.copyfileobj(in_fh, out_fh)
            if existed:
                updated += 1
            else:
                copied += 1
        elif fmt in ("gemini_json", "gemini_jsonl"):
            dest_f = dest_dir / f"gemini-{f.stem}.jsonl"
            existed = dest_f.exists()
            if existed and dest_f.stat().st_mtime >= f.stat().st_mtime:
                unchanged += 1
                continue
            dest_dir.mkdir(parents=True, exist_ok=True)
            meta = {
                "type": "gemini_metadata",
                "cwd": proj.cwd,
                "sessionId": f.stem,
            }
            with dest_f.open("w", errors="ignore") as out_fh:
                out_fh.write(json.dumps(meta) + "\n")
                with f.open("r", errors="ignore") as in_fh:
                    shutil.copyfileobj(in_fh, out_fh)
            if existed:
                updated += 1
            else:
                copied += 1
        else:
            # Standard Claude session file
            dest_f = dest_dir / f.name
            src_stat = f.stat()
            existed = dest_f.exists()
            if existed:
                dest_stat = dest_f.stat()
                if (
                    dest_stat.st_mtime >= src_stat.st_mtime
                    and dest_stat.st_size == src_stat.st_size
                ):
                    unchanged += 1
                    continue
                shutil.copy2(f, dest_f)
                updated += 1
                continue
            dest_dir.mkdir(parents=True, exist_ok=True)
            shutil.copy2(f, dest_f)
            copied += 1

    return copied, updated, unchanged


def run_backup(
    cwd: Path, depth: int | None, backup_dir: Path, source_filter: str = "all"
) -> BackupStats:
    backup_dir.mkdir(parents=True, exist_ok=True)
    projects = select_projects(cwd, depth, source_filter=source_filter)
    stats = BackupStats(projects=len(projects))
    for proj in projects:
        copied, updated, unchanged = copy_project(proj, backup_dir)
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

