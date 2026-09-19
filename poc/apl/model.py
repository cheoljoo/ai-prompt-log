"""Parse & model layer: JSONL records -> Project / Prompt.

Boundary and ordering rules follow docs/data-model.md.
"""
from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Iterator

from . import source

COMMAND_RE = re.compile(r"<command-name>\s*(.*?)\s*</command-name>", re.DOTALL)
# Background/async completion notices (forked subagents incl. /btw, background
# Bash commands, Monitor watches, scheduled wakeups, ...) are all delivered
# back into the session as a normal user-turn wrapped in <task-notification>;
# its <summary> is a human-readable one-liner of what actually happened.
TASK_NOTIFICATION_RE = re.compile(r"<task-notification>.*?<summary>\s*(.*?)\s*</summary>", re.DOTALL)


@dataclass
class AssistantBlock:
    kind: str  # "text" | "tool_use"
    text: str = ""
    tool_name: str = ""
    tool_input: dict = field(default_factory=dict)


@dataclass
class Prompt:
    session_id: str
    timestamp: str
    branch: str
    sidechain: bool
    user_text: str
    blocks: list = field(default_factory=list)  # list[AssistantBlock]
    total_tokens: int = 0

    @property
    def is_command(self) -> bool:
        return bool(COMMAND_RE.search(self.user_text or ""))

    @property
    def is_task_notification(self) -> bool:
        return bool(TASK_NOTIFICATION_RE.search(self.user_text or ""))

    @property
    def summary(self) -> str:
        m = COMMAND_RE.search(self.user_text or "")
        if m:
            return f"[cmd] {m.group(1)}"
        m = TASK_NOTIFICATION_RE.search(self.user_text or "")
        if m:
            text = m.group(1).strip()
            if len(text) > 100:
                text = text[:97] + "..."
            return f"[bg] {text}"
        first_line = (self.user_text or "").strip().splitlines()[0] if self.user_text else ""
        first_line = first_line.strip()
        if len(first_line) > 100:
            first_line = first_line[:97] + "..."
        return first_line or "(empty)"


@dataclass
class Project:
    dir_path: Path
    display_name: str = ""
    cwd: str = ""
    last_activity: str = ""
    prompt_count: int = 0

    def load_prompts(self) -> list:
        return build_prompts(self.dir_path)


def _iter_records(path: Path) -> Iterator[dict]:
    try:
        with path.open("r", errors="ignore") as fh:
            for line in fh:
                line = line.strip()
                if not line:
                    continue
                try:
                    yield json.loads(line)
                except json.JSONDecodeError:
                    continue
    except OSError:
        return


def _is_prompt_boundary(rec: dict) -> bool:
    if rec.get("type") != "user":
        return False
    if rec.get("isMeta"):
        return False
    content = rec.get("message", {}).get("content")
    return isinstance(content, str)


def _extract_assistant_blocks(rec: dict) -> list:
    out = []
    content = rec.get("message", {}).get("content")
    if not isinstance(content, list):
        return out
    for block in content:
        btype = block.get("type")
        if btype == "text" and block.get("text"):
            out.append(AssistantBlock("text", block["text"]))
        elif btype == "tool_use":
            out.append(
                AssistantBlock(
                    "tool_use",
                    tool_name=block.get("name", "?"),
                    tool_input=block.get("input") or {},
                )
            )
    return out


def _usage_tokens(rec: dict) -> int:
    usage = rec.get("message", {}).get("usage")
    if not isinstance(usage, dict):
        return 0
    return sum(
        usage.get(k) or 0
        for k in ("input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens")
    )


def parse_session_file(path: Path) -> list:
    """Return Prompts found in a single session jsonl, in file order."""
    prompts: list = []
    current: Prompt | None = None
    session_id = path.stem
    for rec in _iter_records(path):
        if _is_prompt_boundary(rec):
            current = Prompt(
                session_id=rec.get("sessionId", session_id),
                timestamp=rec.get("timestamp", ""),
                branch=rec.get("gitBranch") or "",
                sidechain=bool(rec.get("isSidechain")),
                user_text=rec.get("message", {}).get("content", ""),
            )
            prompts.append(current)
        elif rec.get("type") == "assistant" and current is not None:
            current.blocks.extend(_extract_assistant_blocks(rec))
            current.total_tokens += _usage_tokens(rec)
    return prompts


def _session_start_timestamp(path: Path) -> str:
    for rec in _iter_records(path):
        ts = rec.get("timestamp")
        if ts:
            return ts
    return ""


def build_prompts(project_dir: Path) -> list:
    """Merge every session file under project_dir into one time-ordered list."""
    files = source.list_session_files(project_dir)
    files_with_ts = sorted(files, key=_session_start_timestamp)
    prompts: list = []
    for f in files_with_ts:
        prompts.extend(parse_session_file(f))
    return prompts


def _find_cwd_field(project_dir: Path) -> str:
    for f in source.list_session_files(project_dir):
        for rec in _iter_records(f):
            cwd = rec.get("cwd")
            if cwd:
                return cwd
    return ""


def load_project(project_dir: Path) -> Project:
    cwd = _find_cwd_field(project_dir)
    display_name = Path(cwd).name if cwd else project_dir.name
    files = source.list_session_files(project_dir)
    last_activity = max((_session_start_timestamp(f) for f in files), default="")
    prompt_count = sum(1 for _ in build_prompts(project_dir))
    return Project(
        dir_path=project_dir,
        display_name=display_name,
        cwd=cwd,
        last_activity=last_activity,
        prompt_count=prompt_count,
    )


def load_projects(aggregate_root: Path) -> list:
    return [load_project(d) for d in source.list_project_dirs(aggregate_root)]
