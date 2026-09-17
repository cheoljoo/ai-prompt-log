"""apl sync: mirror ~/.claude/projects into a local (git-ignored) cache dir.

See plan.md §2-1. Never commits raw prompt content to git.
"""
from __future__ import annotations

import shutil
from pathlib import Path

from . import source


def ensure_gitignore(repo_root: Path) -> None:
    gitignore = repo_root / ".gitignore"
    entry = f"/{source.CACHE_DIRNAME}/"
    existing = gitignore.read_text() if gitignore.exists() else ""
    if entry in existing or source.CACHE_DIRNAME in existing:
        return
    with gitignore.open("a") as fh:
        if existing and not existing.endswith("\n"):
            fh.write("\n")
        fh.write(f"{entry}\n")


def sync(dest_root: Path, src_root: Path = source.CLAUDE_PROJECTS_DIR) -> tuple[int, int]:
    """Incrementally mirror *.jsonl files. Returns (copied, skipped)."""
    dest_root.mkdir(parents=True, exist_ok=True)
    copied = skipped = 0
    if not src_root.is_dir():
        return copied, skipped
    for proj_dir in src_root.iterdir():
        if not proj_dir.is_dir():
            continue
        dest_proj_dir = dest_root / proj_dir.name
        for f in proj_dir.glob("*.jsonl"):
            dest_f = dest_proj_dir / f.name
            src_stat = f.stat()
            if dest_f.exists():
                dest_stat = dest_f.stat()
                if dest_stat.st_mtime >= src_stat.st_mtime and dest_stat.st_size == src_stat.st_size:
                    skipped += 1
                    continue
            dest_proj_dir.mkdir(parents=True, exist_ok=True)
            shutil.copy2(f, dest_f)
            copied += 1
    return copied, skipped


def run_sync() -> None:
    cwd = Path.cwd()
    repo_root = source.find_repo_root(cwd)
    dest_root = repo_root / source.CACHE_DIRNAME
    ensure_gitignore(repo_root)
    copied, skipped = sync(dest_root)
    print(f"apl sync: {copied} file(s) copied, {skipped} unchanged -> {dest_root}")
