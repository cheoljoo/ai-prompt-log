"""Log source layer: locates session logs for Claude Code, Antigravity CLI (agy),
and legacy Gemini CLI.

References: docs/data-model.md
"""
from __future__ import annotations

import json
import os
import re
import sqlite3
from pathlib import Path

CLAUDE_PROJECTS_DIR = Path.home() / ".claude" / "projects"
AGY_APP_DATA_DIR = Path(os.environ.get("ANTIGRAVITY_APP_DATA_DIR") or (Path.home() / ".gemini" / "antigravity-cli"))
GEMINI_TMP_DIR = Path.home() / ".gemini" / "tmp"


def encode_path(path: Path) -> str:
    """Mirror Claude Code's lossy cwd -> directory name encoding."""
    return re.sub(r"[^A-Za-z0-9]", "-", str(path))


def get_agy_conv_info(conv_id: str, base: Path = AGY_APP_DATA_DIR) -> dict:
    """Extract workspace directory, git branch, and title for an AGY conversation."""
    info = {"id": conv_id, "workspace": "", "branch": "", "title": ""}

    # 1. Try conversation_summaries.db
    summaries_db = base / "conversation_summaries.db"
    if summaries_db.exists():
        try:
            conn = sqlite3.connect(summaries_db)
            cur = conn.cursor()
            cur.execute(
                "SELECT preview, workspace_uris FROM conversation_summaries WHERE conversation_id=?",
                (conv_id,),
            )
            row = cur.fetchone()
            if row:
                if row[0]:
                    info["title"] = row[0]
                if row[1]:
                    uris = json.loads(row[1])
                    if uris and isinstance(uris, list) and uris[0].startswith("file://"):
                        info["workspace"] = uris[0][7:]
            conn.close()
        except Exception:
            pass

    # 2. Try conversations/<conv_id>.db (protobuf trajectory_metadata_blob)
    db_path = base / "conversations" / f"{conv_id}.db"
    if db_path.exists():
        try:
            conn = sqlite3.connect(db_path)
            cur = conn.cursor()
            cur.execute("SELECT data FROM trajectory_metadata_blob WHERE id='main'")
            row = cur.fetchone()
            if row and row[0]:
                data = row[0]
                idx = 0
                while idx < len(data):
                    tag = data[idx]
                    idx += 1
                    wire = tag & 7
                    fnum = tag >> 3
                    if wire == 2:
                        length = 0
                        shift = 0
                        while True:
                            b = data[idx]
                            idx += 1
                            length |= (b & 0x7f) << shift
                            if not (b & 0x80):
                                break
                            shift += 7
                        val = data[idx : idx + length]
                        idx += length
                        if fnum == 7 and not info["workspace"]:
                            s = val.decode("utf-8", errors="ignore")
                            if s.startswith("file://"):
                                info["workspace"] = s[7:]
                        elif fnum == 1:
                            sidx = 0
                            while sidx < len(val):
                                stag = val[sidx]
                                sidx += 1
                                swire = stag & 7
                                sfnum = stag >> 3
                                if swire == 2:
                                    slen = 0
                                    sshift = 0
                                    while True:
                                        b = val[sidx]
                                        sidx += 1
                                        slen |= (b & 0x7f) << sshift
                                        if not (b & 0x80):
                                            break
                                        sshift += 7
                                    sval = val[sidx : sidx + slen]
                                    sidx += slen
                                    if sfnum == 1 and not info["workspace"]:
                                        s = sval.decode("utf-8", errors="ignore")
                                        if s.startswith("file://"):
                                            info["workspace"] = s[7:]
                                    elif sfnum == 4 and not info["branch"]:
                                        info["branch"] = sval.decode("utf-8", errors="ignore")
                                elif swire == 0:
                                    while True:
                                        b = val[sidx]
                                        sidx += 1
                                        if not (b & 0x80):
                                            break
                                else:
                                    break
                    elif wire == 0:
                        while True:
                            b = data[idx]
                            idx += 1
                            if not (b & 0x80):
                                break
                    else:
                        break
            conn.close()
        except Exception:
            pass

    return info


def scan_agy_conversations(base: Path = AGY_APP_DATA_DIR) -> list[dict]:
    """Return all AGY conversations with their workspace, branch, and transcript file."""
    convs = []
    brain_dir = base / "brain"
    if not brain_dir.is_dir():
        return convs

    for conv_dir in brain_dir.iterdir():
        if not conv_dir.is_dir():
            continue
        transcript = conv_dir / ".system_generated" / "logs" / "transcript.jsonl"
        if not transcript.exists():
            transcript = conv_dir / ".system_generated" / "logs" / "transcript_full.jsonl"
        if transcript.exists():
            info = get_agy_conv_info(conv_dir.name, base)
            info["transcript"] = transcript
            convs.append(info)
    return convs


def scan_gemini_tmp_projects(base: Path = GEMINI_TMP_DIR) -> list[dict]:
    """Scan legacy Gemini CLI project directories in ~/.gemini/tmp."""
    projects = []
    if not base.is_dir():
        return projects

    for pdir in base.iterdir():
        if not pdir.is_dir():
            continue
        root_f = pdir / ".project_root"
        ws = root_f.read_text().strip() if root_f.exists() else ""
        chats_dir = pdir / "chats"
        if chats_dir.is_dir():
            chat_files = sorted(
                f for f in chats_dir.iterdir()
                if f.is_file() and f.suffix in (".json", ".jsonl")
            )
            if chat_files:
                projects.append({
                    "dir_name": pdir.name,
                    "workspace": ws,
                    "chat_files": chat_files,
                })
    return projects


def find_direct_project_dir(cwd: Path) -> Path:
    """Walk up from cwd to the nearest ancestor with recorded Claude sessions.

    Maintained for backward compatibility.
    """
    cur = cwd.resolve()
    for candidate in (cur, *cur.parents):
        d = CLAUDE_PROJECTS_DIR / encode_path(candidate)
        if d.is_dir():
            return d
    return CLAUDE_PROJECTS_DIR / encode_path(cur)


def find_direct_project_info(cwd: Path, source_filter: str = "all") -> dict:
    """Walk up from cwd to find the nearest ancestor project with recorded sessions.

    Checks Claude, AGY, and Gemini CLI sessions according to source_filter.
    Returns a dict with project info and matching session files.
    """
    include_claude = source_filter in ("all", "claude")
    include_agy = source_filter in ("all", "agy", "gemini")

    cur = cwd.resolve()
    all_agy = scan_agy_conversations() if include_agy else []
    all_gemini = scan_gemini_tmp_projects() if include_agy else []

    for candidate in (cur, *cur.parents):
        cand_str = str(candidate)
        claude_files: list[Path] = []
        if include_claude:
            d = CLAUDE_PROJECTS_DIR / encode_path(candidate)
            if d.is_dir():
                claude_files = sorted(d.glob("*.jsonl"))

        agy_convs: list[dict] = []
        if include_agy:
            for c in all_agy:
                c_ws = c.get("workspace", "")
                if c_ws and Path(c_ws).resolve() == candidate:
                    agy_convs.append(c)

        gemini_files: list[Path] = []
        if include_agy:
            for g in all_gemini:
                g_ws = g.get("workspace", "")
                if g_ws and Path(g_ws).resolve() == candidate:
                    gemini_files.extend(g.get("chat_files", []))

        if claude_files or agy_convs or gemini_files:
            return {
                "cwd": cand_str,
                "display_name": candidate.name,
                "claude_files": claude_files,
                "agy_convs": agy_convs,
                "gemini_files": gemini_files,
                "path": candidate,
            }

    # Fallback to cur
    d = CLAUDE_PROJECTS_DIR / encode_path(cur)
    return {
        "cwd": str(cur),
        "display_name": cur.name,
        "claude_files": sorted(d.glob("*.jsonl")) if (include_claude and d.is_dir()) else [],
        "agy_convs": [],
        "gemini_files": [],
        "path": cur,
    }


def detect_mode(
    cwd: Path,
    aggregate: bool = False,
    root_override: Path | None = None,
    source_filter: str = "all",
) -> tuple[str, Path]:
    """Return (mode, target_dir).

    mode == "aggregate": target_dir is the root directory (backup dir or None) -> 3-level view.
    mode == "direct": target_dir is the resolved project directory -> 2-level view.
    """
    if root_override is not None:
        return "aggregate", root_override
    if aggregate:
        return "aggregate", CLAUDE_PROJECTS_DIR if source_filter == "claude" else Path.home()

    info = find_direct_project_info(cwd, source_filter=source_filter)
    return "direct", info["path"]


def list_session_files(project_dir: Path) -> list[Path]:
    if not project_dir.is_dir():
        return []
    return sorted(project_dir.glob("*.jsonl"))


def list_project_dirs(aggregate_root: Path) -> list[Path]:
    if not aggregate_root.is_dir():
        return []
    return sorted(p for p in aggregate_root.iterdir() if p.is_dir())
