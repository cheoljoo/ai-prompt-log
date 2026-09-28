"""Parse & model layer: JSONL records -> Project / Prompt.

Boundary and ordering rules follow docs/data-model.md.
Supports Claude Code, Antigravity CLI (agy), legacy Gemini CLI, and OpenCode.
"""
from __future__ import annotations

import json
import re
import shlex
from dataclasses import dataclass, field
from pathlib import Path
from typing import Iterator

from . import source

COMMAND_RE = re.compile(r"<command-name>\s*(.*?)\s*</command-name>", re.DOTALL)
TASK_NOTIFICATION_RE = re.compile(
    r"<task-notification>.*?<summary>\s*(.*?)\s*</summary>", re.DOTALL
)
USER_REQUEST_RE = re.compile(r"<USER_REQUEST>\s*(.*?)\s*</USER_REQUEST>", re.DOTALL)
SLASH_CMD_RE = re.compile(r"^/([a-zA-Z0-9_\-]+)(?:\s.*)?$")


@dataclass
class AssistantBlock:
    kind: str  # "text" | "tool_use"
    text: str = ""
    tool_name: str = ""
    tool_input: dict = field(default_factory=dict)


@dataclass
class Prompt:
    session_id: str
    timestamp: str  # start time (first user message)
    branch: str
    sidechain: bool
    user_text: str
    blocks: list = field(default_factory=list)  # list[AssistantBlock]
    total_tokens: int = 0
    source: str = "claude"  # "claude" | "agy" | "gemini" | "opencode" | "copilot"
    end_timestamp: str = ""  # end time (last assistant activity for this prompt)

    @property
    def final_result(self) -> str:
        """The last assistant "text" block's content for this prompt -- i.e.
        the final response text shown to the user, ignoring any
        intermediate tool_use blocks. Empty if the assistant never produced
        text (e.g. session cut short before a response)."""
        for block in reversed(self.blocks):
            if block.kind == "text":
                return block.text
        return ""

    @property
    def is_command(self) -> bool:
        if bool(COMMAND_RE.search(self.user_text or "")):
            return True
        text = (self.user_text or "").strip()
        first_line = text.splitlines()[0].strip() if text else ""
        if SLASH_CMD_RE.match(first_line):
            if not any(
                first_line.startswith(p)
                for p in ("/data", "/home", "/usr", "/etc", "/tmp", "/var", "/opt")
            ):
                return True
        return False

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
        text = (self.user_text or "").strip()
        first_line = text.splitlines()[0].strip() if text else ""
        m_cmd = SLASH_CMD_RE.match(first_line)
        if m_cmd and not any(
            first_line.startswith(p)
            for p in ("/data", "/home", "/usr", "/etc", "/tmp", "/var", "/opt")
        ):
            if len(first_line) > 100:
                first_line = first_line[:97] + "..."
            return f"[cmd] {first_line}"
        if len(first_line) > 100:
            first_line = first_line[:97] + "..."
        return first_line or "(empty)"

    @property
    def file_changes(self) -> list[tuple[str, str]]:
        """Best-effort (path, action) list of files created/modified/deleted
        by this prompt's tool calls. action is one of
        "created" | "modified" | "deleted". See extract_file_changes()."""
        return extract_file_changes(self)


# tool_name -> (input key holding the file path, action)
_FILE_TOOL_OPS: dict[str, tuple[str, str]] = {
    "Write": ("file_path", "created"),
    "Edit": ("file_path", "modified"),
    "MultiEdit": ("file_path", "modified"),
    "NotebookEdit": ("notebook_path", "modified"),
    "write_to_file": ("TargetFile", "created"),
    "replace_file_content": ("TargetFile", "modified"),
    "write_file": ("file_path", "created"),
    "create": ("path", "created"),  # Copilot CLI
}

# tool_name -> input key holding the raw shell command string
_SHELL_TOOL_COMMAND_KEYS: dict[str, str] = {
    "Bash": "command",
    "run_command": "CommandLine",
    "run_shell_command": "command",
    "bash": "command",
}


def _bash_file_ops(command: str) -> list[tuple[str, str]]:
    """Best-effort scan of a shell command string for rm/mv/cp/touch/mkdir
    invocations, returning (path, action) pairs. Deliberately conservative:
    only handles simple statement-per-segment commands, not full shell
    parsing (pipes into xargs, variables, globs, etc. are not resolved)."""
    ops: list[tuple[str, str]] = []
    if not command:
        return ops
    for stmt in re.split(r"&&|\|\||;|\n", command):
        stmt = stmt.strip()
        if not stmt:
            continue
        m = re.match(r"^(rm|rmdir)\b(.*)$", stmt)
        if m:
            try:
                args = shlex.split(m.group(2))
            except ValueError:
                args = m.group(2).split()
            for a in args:
                if not a.startswith("-"):
                    ops.append((a, "deleted"))
            continue
        m = re.match(r"^touch\b(.*)$", stmt)
        if m:
            try:
                args = shlex.split(m.group(1))
            except ValueError:
                args = m.group(1).split()
            for a in args:
                if not a.startswith("-"):
                    ops.append((a, "created"))
            continue
        m = re.match(r"^mkdir\b(.*)$", stmt)
        if m:
            try:
                args = shlex.split(m.group(1))
            except ValueError:
                args = m.group(1).split()
            for a in args:
                if not a.startswith("-"):
                    ops.append((a, "created"))
            continue
        m = re.match(r"^mv\b(.*)$", stmt)
        if m:
            try:
                args = [a for a in shlex.split(m.group(1)) if not a.startswith("-")]
            except ValueError:
                args = [a for a in m.group(1).split() if not a.startswith("-")]
            if len(args) >= 2:
                ops.append((args[0], "deleted"))
                ops.append((args[-1], "created"))
            continue
        m = re.match(r"^cp\b(.*)$", stmt)
        if m:
            try:
                args = [a for a in shlex.split(m.group(1)) if not a.startswith("-")]
            except ValueError:
                args = [a for a in m.group(1).split() if not a.startswith("-")]
            if len(args) >= 2:
                ops.append((args[-1], "created"))
            continue
    return ops


def extract_file_changes(prompt: "Prompt") -> list[tuple[str, str]]:
    """Best-effort list of (path, action) file changes derived from a
    prompt's tool calls, across all supported sources (Claude/AGY/Gemini/
    OpenCode). action is "created" | "modified" | "deleted".

    Structured file tools (Write/Edit, write_to_file/replace_file_content,
    write_file, opencode's edit) are reliable. Shell/bash-style tools are
    only regex-scanned for rm/mv/cp/touch/mkdir, so are best-effort and may
    miss or misinterpret complex commands.
    """
    raw: list[tuple[str, str]] = []
    for block in prompt.blocks:
        if block.kind != "tool_use":
            continue
        name = block.tool_name
        inp = block.tool_input or {}

        if name in _FILE_TOOL_OPS:
            key, action = _FILE_TOOL_OPS[name]
            path = inp.get(key)
            if path:
                raw.append((path, action))
        elif name == "edit":  # OpenCode's edit tool (filePath) or Copilot's (path)
            if inp.get("filePath"):
                action = "modified" if inp.get("oldString") else "created"
                raw.append((inp["filePath"], action))
            elif inp.get("path"):
                raw.append((inp["path"], "modified"))
        elif name in _SHELL_TOOL_COMMAND_KEYS:
            command = inp.get(_SHELL_TOOL_COMMAND_KEYS[name], "")
            raw.extend(_bash_file_ops(command))

    # Merge multiple ops on the same path within one prompt into its net
    # effect: a later "modified" doesn't downgrade an earlier "created", but
    # a later "created"/"deleted" replaces whatever came before.
    merged: dict[str, str] = {}
    for path, action in raw:
        prev = merged.get(path)
        if prev == "created" and action == "modified":
            continue
        merged[path] = action
    return list(merged.items())


@dataclass
class Project:
    dir_path: Path
    display_name: str = ""
    cwd: str = ""
    last_activity: str = ""
    prompt_count: int = 0
    source: str = "all"  # "claude" | "agy" | "gemini" | "opencode" | "all" | "both"
    session_files: list[Path] = field(default_factory=list)
    conv_metadata: dict[str, dict] = field(default_factory=dict)

    def get_session_files(self) -> list[Path]:
        if self.session_files:
            return self.session_files
        return source.list_session_files(self.dir_path)

    def load_prompts(self) -> list[Prompt]:
        files = self.get_session_files()
        if files:
            return build_prompts_from_files(files, self.conv_metadata)
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


def _is_claude_prompt_boundary(rec: dict) -> bool:
    if rec.get("type") != "user":
        return False
    if rec.get("isMeta"):
        return False
    content = rec.get("message", {}).get("content")
    return isinstance(content, str)


def _clean_tool_input(inp: dict) -> dict:
    cleaned = {}
    for k, v in inp.items():
        if isinstance(v, str) and v.startswith('"') and v.endswith('"') and len(v) >= 2:
            try:
                v = json.loads(v)
            except Exception:
                pass
        cleaned[k] = v
    return cleaned


def _extract_assistant_blocks(rec: dict) -> list[AssistantBlock]:
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
                    tool_input=_clean_tool_input(block.get("input") or {}),
                )
            )
    return out


def _usage_tokens(rec: dict) -> int:
    usage = rec.get("message", {}).get("usage")
    if not isinstance(usage, dict):
        return 0
    return sum(
        usage.get(k) or 0
        for k in (
            "input_tokens",
            "cache_creation_input_tokens",
            "cache_read_input_tokens",
            "output_tokens",
        )
    )


def parse_claude_session_file(path: Path) -> list[Prompt]:
    """Return Prompts found in a single Claude session jsonl, in file order."""
    prompts: list[Prompt] = []
    current: Prompt | None = None
    session_id = path.stem
    for rec in _iter_records(path):
        if _is_claude_prompt_boundary(rec):
            ts = rec.get("timestamp", "")
            current = Prompt(
                session_id=rec.get("sessionId", session_id),
                timestamp=ts,
                end_timestamp=ts,
                branch=rec.get("gitBranch") or "",
                sidechain=bool(rec.get("isSidechain")),
                user_text=rec.get("message", {}).get("content", ""),
                source="claude",
            )
            prompts.append(current)
        elif rec.get("type") == "assistant" and current is not None:
            current.blocks.extend(_extract_assistant_blocks(rec))
            current.total_tokens += _usage_tokens(rec)
            if rec.get("timestamp"):
                current.end_timestamp = rec["timestamp"]
    return prompts


def parse_opencode_session_file(path: Path) -> list[Prompt]:
    """Return Prompts found in a materialized OpenCode session jsonl.

    OpenCode itself stores sessions in a sqlite DB (see source.py); by the
    time it reaches here, source.scan_opencode_conversations() has already
    rendered the session into the same Claude-schema jsonl shape (a leading
    "opencode_metadata" line, then "user"/"assistant" records), so we can
    reuse the Claude parser verbatim and just relabel the source.
    """
    prompts = parse_claude_session_file(path)
    for p in prompts:
        p.source = "opencode"
    return prompts


def parse_copilot_events_file(
    path: Path, session_id: str = "", default_branch: str = ""
) -> list[Prompt]:
    """Return Prompts found in a GitHub Copilot CLI session's events.jsonl.
    See docs/data-model.md §11."""
    prompts: list[Prompt] = []
    current: Prompt | None = None
    sid = session_id
    branch = default_branch

    for rec in _iter_records(path):
        rtype = rec.get("type")
        data = rec.get("data") or {}

        if rtype == "copilot_metadata":
            if data.get("gitBranch"):
                branch = data["gitBranch"]
            if data.get("sessionId"):
                sid = data["sessionId"]
            continue

        if rtype == "user.message":
            content = data.get("content", "")
            ts = rec.get("timestamp", "")
            current = Prompt(
                session_id=sid,
                timestamp=ts,
                end_timestamp=ts,
                branch=branch,
                sidechain=False,
                user_text=content,
                source="copilot",
            )
            prompts.append(current)
        elif rtype == "assistant.message":
            if current is None:
                continue
            text = data.get("content", "")
            if text:
                current.blocks.append(AssistantBlock("text", text=text))
            for tr in data.get("toolRequests") or []:
                name = tr.get("name") or "?"
                targs = _clean_tool_input(tr.get("arguments") or {})
                current.blocks.append(
                    AssistantBlock("tool_use", tool_name=name, tool_input=targs)
                )
            if rec.get("timestamp"):
                current.end_timestamp = rec["timestamp"]

    if not sid:
        sid = path.parent.name
    for p in prompts:
        if not p.session_id:
            p.session_id = sid
    return prompts


def parse_agy_transcript_file(
    path: Path, session_id: str = "", default_branch: str = ""
) -> list[Prompt]:
    """Return Prompts found in an AGY transcript jsonl file."""
    prompts: list[Prompt] = []
    current: Prompt | None = None
    branch = default_branch
    sid = session_id or path.parent.parent.name
    if sid == "brain":
        sid = path.stem
    if sid.startswith("agy-"):
        sid = sid[4:]

    for rec in _iter_records(path):
        stype = rec.get("type")
        source_val = rec.get("source")

        if stype == "agy_metadata":
            if rec.get("gitBranch"):
                branch = rec["gitBranch"]
            if rec.get("sessionId"):
                sid = rec["sessionId"]
            continue

        if stype == "USER_INPUT" or (source_val == "USER_EXPLICIT" and "content" in rec):
            content = rec.get("content", "")
            m_req = USER_REQUEST_RE.search(content)
            if m_req:
                user_text = m_req.group(1).strip()
            else:
                clean = re.sub(
                    r"<ADDITIONAL_METADATA>.*?</ADDITIONAL_METADATA>",
                    "",
                    content,
                    flags=re.DOTALL,
                )
                clean = re.sub(
                    r"<USER_SETTINGS_CHANGE>.*?</USER_SETTINGS_CHANGE>",
                    "",
                    clean,
                    flags=re.DOTALL,
                )
                user_text = clean.strip()
            ts = rec.get("created_at") or rec.get("timestamp") or ""
            current = Prompt(
                session_id=sid,
                timestamp=ts,
                end_timestamp=ts,
                branch=branch,
                sidechain=False,
                user_text=user_text,
                source="agy",
            )
            prompts.append(current)
        elif stype == "PLANNER_RESPONSE" and current is not None:
            tcalls = rec.get("tool_calls") or []
            for tc in tcalls:
                tname = tc.get("name", "?")
                targs = _clean_tool_input(tc.get("args") or {})
                current.blocks.append(
                    AssistantBlock("tool_use", tool_name=tname, tool_input=targs)
                )
            text = rec.get("content", "")
            if text:
                current.blocks.append(AssistantBlock("text", text=text))
            resp_ts = rec.get("created_at") or rec.get("timestamp")
            if resp_ts:
                current.end_timestamp = resp_ts
    return prompts


def parse_gemini_json_file(path: Path) -> list[Prompt]:
    """Parse legacy Gemini CLI session from a JSON file."""
    try:
        with path.open("r", errors="ignore") as fh:
            data = json.load(fh)
    except Exception:
        return []

    session_id = data.get("sessionId", path.stem)
    prompts: list[Prompt] = []
    current: Prompt | None = None

    for msg in data.get("messages", []):
        mtype = msg.get("type")
        if mtype == "user":
            texts = []
            for c in msg.get("content", []):
                if isinstance(c, dict) and "text" in c:
                    texts.append(c["text"])
                elif isinstance(c, str):
                    texts.append(c)
            user_text = "\n".join(texts)
            ts = msg.get("timestamp", "")
            current = Prompt(
                session_id=session_id,
                timestamp=ts,
                end_timestamp=ts,
                branch="",
                sidechain=False,
                user_text=user_text,
                source="gemini",
            )
            prompts.append(current)
        elif mtype == "gemini" and current is not None:
            text = msg.get("content", "")
            if text:
                current.blocks.append(AssistantBlock("text", text=text))
            tokens = msg.get("tokens", {}).get("total", 0)
            current.total_tokens += tokens
            if msg.get("timestamp"):
                current.end_timestamp = msg["timestamp"]
    return prompts


def parse_gemini_jsonl_file(path: Path) -> list[Prompt]:
    """Parse legacy Gemini CLI session from a JSONL file."""
    prompts: list[Prompt] = []
    current: Prompt | None = None
    session_id = path.stem

    for rec in _iter_records(path):
        if rec.get("type") == "gemini_metadata":
            if rec.get("sessionId"):
                session_id = rec["sessionId"]
            continue
        if "sessionId" in rec and "startTime" in rec:
            session_id = rec.get("sessionId", session_id)
            continue
        if "$set" in rec:
            for msg in rec.get("$set", {}).get("messages", []):
                mtype = msg.get("type")
                if mtype == "user":
                    texts = [
                        c.get("text", "") if isinstance(c, dict) else str(c)
                        for c in msg.get("content", [])
                    ]
                    ts = msg.get("timestamp", "")
                    current = Prompt(
                        session_id=session_id,
                        timestamp=ts,
                        end_timestamp=ts,
                        branch="",
                        sidechain=False,
                        user_text="\n".join(texts),
                        source="gemini",
                    )
                    prompts.append(current)
                elif mtype == "gemini" and current is not None:
                    text = msg.get("content", "")
                    if text:
                        current.blocks.append(AssistantBlock("text", text=text))
                    tokens = msg.get("tokens", {}).get("total", 0)
                    current.total_tokens += tokens
                    if msg.get("timestamp"):
                        current.end_timestamp = msg["timestamp"]
            continue

        mtype = rec.get("type")
        if mtype == "user":
            texts = [
                c.get("text", "") if isinstance(c, dict) else str(c)
                for c in rec.get("content", [])
            ]
            ts = rec.get("timestamp", "")
            current = Prompt(
                session_id=rec.get("id", session_id),
                timestamp=ts,
                end_timestamp=ts,
                branch="",
                sidechain=False,
                user_text="\n".join(texts),
                source="gemini",
            )
            prompts.append(current)
        elif mtype == "gemini" and current is not None:
            text = rec.get("content", "")
            if text:
                current.blocks.append(AssistantBlock("text", text=text))
            tokens = rec.get("tokens", {}).get("total", 0)
            current.total_tokens += tokens
            if rec.get("timestamp"):
                current.end_timestamp = rec["timestamp"]
    return prompts


def detect_file_format(path: Path) -> str:
    """Detect format of a session log file: 'claude', 'agy', 'opencode',
    'copilot', 'gemini_json', or 'gemini_jsonl'."""
    try:
        with path.open("r", errors="ignore") as fh:
            chunk = fh.read(4096)
            if not chunk.strip():
                return "unknown"
            if chunk.strip().startswith("{"):
                try:
                    obj = json.loads(chunk)
                    if isinstance(obj, dict) and "messages" in obj:
                        return "gemini_json"
                except Exception:
                    pass
            for line in chunk.splitlines():
                line = line.strip()
                if not line:
                    continue
                try:
                    rec = json.loads(line)
                except Exception:
                    continue
                rtype = rec.get("type")
                if rtype == "agy_metadata":
                    return "agy"
                if rtype == "opencode_metadata":
                    return "opencode"
                if rtype == "gemini_metadata":
                    return "gemini_jsonl"
                if rtype == "copilot_metadata":
                    return "copilot"
                if rtype in ("session.start", "user.message", "assistant.message"):
                    return "copilot"
                if rtype in ("USER_INPUT", "PLANNER_RESPONSE", "LIST_DIRECTORY", "VIEW_FILE"):
                    return "agy"
                if rec.get("source") in ("USER_EXPLICIT", "MODEL"):
                    return "agy"
                if "message" in rec and rtype in ("user", "assistant"):
                    return "claude"
                if rtype == "gemini" or (
                    rtype == "user" and isinstance(rec.get("content"), list)
                ):
                    return "gemini_jsonl"
                if "projectHash" in rec or "$set" in rec:
                    return "gemini_jsonl"
    except Exception:
        pass
    return "claude"


def parse_session_file(
    path: Path, default_branch: str = "", session_id: str = ""
) -> list[Prompt]:
    """Parse session file according to detected format."""
    fmt = detect_file_format(path)
    if fmt == "agy":
        return parse_agy_transcript_file(
            path, session_id=session_id, default_branch=default_branch
        )
    if fmt == "opencode":
        return parse_opencode_session_file(path)
    if fmt == "copilot":
        return parse_copilot_events_file(
            path, session_id=session_id, default_branch=default_branch
        )
    if fmt == "gemini_json":
        return parse_gemini_json_file(path)
    if fmt == "gemini_jsonl":
        return parse_gemini_jsonl_file(path)
    return parse_claude_session_file(path)


def _format_ts_str(ts) -> str:
    if not ts:
        return ""
    if isinstance(ts, (int, float)):
        if ts > 100_000_000_000:
            ts = ts / 1000.0
        from datetime import datetime, timezone
        return datetime.fromtimestamp(ts, tz=timezone.utc).isoformat()
    return str(ts)


def _session_start_timestamp(path: Path) -> str:
    try:
        with path.open("r", errors="ignore") as fh:
            first_chunk = fh.read(4096)
            if first_chunk.strip().startswith("{"):
                try:
                    obj = json.loads(first_chunk)
                    if isinstance(obj, dict):
                        if "startTime" in obj:
                            return _format_ts_str(obj["startTime"])
                        if "timestamp" in obj:
                            return _format_ts_str(obj["timestamp"])
                except Exception:
                    pass
            for line in first_chunk.splitlines():
                line = line.strip()
                if not line:
                    continue
                try:
                    rec = json.loads(line)
                    if rec.get("created_at"):
                        return _format_ts_str(rec["created_at"])
                    if rec.get("timestamp"):
                        return _format_ts_str(rec["timestamp"])
                    if rec.get("startTime"):
                        return _format_ts_str(rec["startTime"])
                except Exception:
                    continue
    except Exception:
        pass
    return ""


def build_prompts_from_files(
    files: list[Path], conv_metadata: dict[str, dict] | None = None
) -> list[Prompt]:
    """Merge given session files into one time-ordered list."""
    files_with_ts = sorted(files, key=_session_start_timestamp)
    prompts: list[Prompt] = []
    conv_metadata = conv_metadata or {}
    for f in files_with_ts:
        # Match metadata by filename or conv_id
        meta = conv_metadata.get(f.stem) or {}
        if not meta and f.parent.name == "logs" and f.parent.parent.name == ".system_generated":
            meta = conv_metadata.get(f.parent.parent.parent.name) or {}
        if not meta and f.name == "events.jsonl":
            # Every Copilot CLI session's transcript is named events.jsonl,
            # so f.stem ("events") collides across sessions; use the parent
            # directory (the session uuid) instead, matching
            # source.scan_copilot_sessions().
            meta = conv_metadata.get(f.parent.name) or {}
        prompts.extend(
            parse_session_file(
                f,
                default_branch=meta.get("branch", ""),
                session_id=meta.get("id", f.stem),
            )
        )
    return prompts


def build_prompts(project_dir: Path) -> list[Prompt]:
    """Merge every session file under project_dir into one time-ordered list."""
    files = source.list_session_files(project_dir)
    return build_prompts_from_files(files)


def _find_cwd_field(project_dir: Path) -> str:
    for f in source.list_session_files(project_dir):
        for rec in _iter_records(f):
            cwd = rec.get("cwd")
            if cwd:
                return cwd
    return ""


def load_project(target: Path, source_filter: str = "all") -> Project:
    """Load a Project from either a session directory or a workspace path."""
    # Check if target is directly a session directory (e.g. ~/.claude/projects/... or backup dir)
    files = source.list_session_files(target)
    if files:
        cwd = _find_cwd_field(target)
        display_name = Path(cwd).name if cwd else target.name
        last_activity = max((_session_start_timestamp(f) for f in files), default="")
        prompt_count = len(build_prompts_from_files(files))
        return Project(
            dir_path=target,
            display_name=display_name,
            cwd=cwd,
            last_activity=last_activity,
            prompt_count=prompt_count,
            source=source_filter,
            session_files=files,
        )

    # Otherwise target is a workspace path
    info = source.find_direct_project_info(target, source_filter=source_filter)
    all_files: list[Path] = []
    metadata: dict[str, dict] = {}
    sources_found: set[str] = set()

    for f in info.get("claude_files", []):
        all_files.append(f)
        sources_found.add("claude")

    for c in info.get("agy_convs", []):
        t = c.get("transcript")
        if t and t.exists():
            all_files.append(t)
            metadata[c["id"]] = c
            metadata[t.stem] = c
            sources_found.add("agy")

    for gf in info.get("gemini_files", []):
        all_files.append(gf)
        sources_found.add("gemini")

    for c in info.get("opencode_convs", []):
        t = c.get("transcript")
        if t and t.exists():
            all_files.append(t)
            metadata[c["id"]] = c
            metadata[t.stem] = c
            sources_found.add("opencode")

    for c in info.get("copilot_convs", []):
        t = c.get("transcript")
        if t and t.exists():
            all_files.append(t)
            # Every session's transcript is named events.jsonl, so unlike
            # agy/opencode above we deliberately do NOT key metadata by
            # t.stem here (it would collide across every Copilot session in
            # the workspace); build_prompts_from_files() instead falls back
            # to the parent directory name (the session uuid).
            metadata[c["id"]] = c
            metadata[t.parent.name] = c
            sources_found.add("copilot")

    src_label = (
        "+".join(sorted(sources_found))
        if sources_found
        else (source_filter if source_filter in ("claude", "opencode", "copilot") else "agy")
    )
    last_activity = max((_session_start_timestamp(f) for f in all_files), default="")
    prompts = build_prompts_from_files(all_files, metadata)

    return Project(
        dir_path=target,
        display_name=info.get("display_name", target.name),
        cwd=info.get("cwd", str(target)),
        last_activity=last_activity,
        prompt_count=len(prompts),
        source=src_label,
        session_files=all_files,
        conv_metadata=metadata,
    )


def load_projects(
    aggregate_root: Path | None = None, source_filter: str = "all"
) -> list[Project]:
    """Load all projects across sources or from a specific root (backup dir)."""
    # 1. Custom aggregate root (e.g. backup directory)
    if aggregate_root is not None and aggregate_root not in (
        source.CLAUDE_PROJECTS_DIR,
        Path.home(),
    ):
        return [
            load_project(d, source_filter=source_filter)
            for d in source.list_project_dirs(aggregate_root)
        ]

    # 2. Claude-only aggregate
    if source_filter == "claude":
        return [
            load_project(d, source_filter="claude")
            for d in source.list_project_dirs(source.CLAUDE_PROJECTS_DIR)
        ]

    # 3. Global multi-source aggregate (Claude + AGY + Gemini + OpenCode)
    include_claude = source_filter in ("all", "claude")
    include_agy = source_filter in ("all", "agy", "gemini")
    include_opencode = source_filter in ("all", "opencode")
    include_copilot = source_filter in ("all", "copilot")

    projects_by_ws: dict[str, dict] = {}

    if include_claude and source.CLAUDE_PROJECTS_DIR.is_dir():
        for pdir in source.list_project_dirs(source.CLAUDE_PROJECTS_DIR):
            c_files = source.list_session_files(pdir)
            if not c_files:
                continue
            cwd = _find_cwd_field(pdir) or str(pdir)
            entry = projects_by_ws.setdefault(
                cwd,
                {
                    "cwd": cwd,
                    "display_name": Path(cwd).name,
                    "claude_files": [],
                    "agy_convs": [],
                    "gemini_files": [],
                    "opencode_convs": [],
                    "copilot_convs": [],
                    "conv_metadata": {},
                },
            )
            entry["claude_files"].extend(c_files)

    if include_agy:
        for c in source.scan_agy_conversations():
            ws = c.get("workspace", "")
            if not ws:
                ws = f"agy-{c['id'][:8]}"
            entry = projects_by_ws.setdefault(
                ws,
                {
                    "cwd": ws,
                    "display_name": Path(ws).name,
                    "claude_files": [],
                    "agy_convs": [],
                    "gemini_files": [],
                    "opencode_convs": [],
                    "copilot_convs": [],
                    "conv_metadata": {},
                },
            )
            entry["agy_convs"].append(c)
            entry["conv_metadata"][c["id"]] = c

        for g in source.scan_gemini_tmp_projects():
            ws = g.get("workspace", "")
            if not ws:
                ws = g.get("dir_name", "")
            entry = projects_by_ws.setdefault(
                ws,
                {
                    "cwd": ws,
                    "display_name": Path(ws).name,
                    "claude_files": [],
                    "agy_convs": [],
                    "gemini_files": [],
                    "opencode_convs": [],
                    "copilot_convs": [],
                    "conv_metadata": {},
                },
            )
            entry["gemini_files"].extend(g.get("chat_files", []))

    if include_opencode:
        for c in source.scan_opencode_conversations():
            ws = c.get("workspace", "")
            if not ws:
                ws = f"opencode-{c['id'][:8]}"
            entry = projects_by_ws.setdefault(
                ws,
                {
                    "cwd": ws,
                    "display_name": Path(ws).name,
                    "claude_files": [],
                    "agy_convs": [],
                    "gemini_files": [],
                    "opencode_convs": [],
                    "copilot_convs": [],
                    "conv_metadata": {},
                },
            )
            entry["opencode_convs"].append(c)
            entry["conv_metadata"][c["id"]] = c

    if include_copilot:
        for c in source.scan_copilot_sessions():
            ws = c.get("workspace", "")
            if not ws:
                ws = f"copilot-{c['id'][:8]}"
            entry = projects_by_ws.setdefault(
                ws,
                {
                    "cwd": ws,
                    "display_name": Path(ws).name,
                    "claude_files": [],
                    "agy_convs": [],
                    "gemini_files": [],
                    "opencode_convs": [],
                    "copilot_convs": [],
                    "conv_metadata": {},
                },
            )
            entry["copilot_convs"].append(c)
            entry["conv_metadata"][c["id"]] = c
            t = c.get("transcript")
            if t:
                # events.jsonl is the same filename for every session, so
                # (unlike agy/opencode above) key by the parent directory
                # (session uuid), not by t.stem -- see build_prompts_from_files().
                entry["conv_metadata"][t.parent.name] = c

    project_list: list[Project] = []
    for ws, data in projects_by_ws.items():
        all_files: list[Path] = []
        sources_found: set[str] = set()

        for f in data["claude_files"]:
            all_files.append(f)
            sources_found.add("claude")
        for c in data["agy_convs"]:
            t = c.get("transcript")
            if t and t.exists():
                all_files.append(t)
                sources_found.add("agy")
        for gf in data["gemini_files"]:
            all_files.append(gf)
            sources_found.add("gemini")
        for c in data["opencode_convs"]:
            t = c.get("transcript")
            if t and t.exists():
                all_files.append(t)
                sources_found.add("opencode")
        for c in data["copilot_convs"]:
            t = c.get("transcript")
            if t and t.exists():
                all_files.append(t)
                sources_found.add("copilot")

        if not all_files:
            continue

        src_label = "+".join(sorted(sources_found)) if sources_found else "all"
        last_activity = max((_session_start_timestamp(f) for f in all_files), default="")
        prompts = build_prompts_from_files(all_files, data["conv_metadata"])

        project_list.append(
            Project(
                dir_path=Path(ws),
                display_name=data["display_name"],
                cwd=data["cwd"],
                last_activity=last_activity,
                prompt_count=len(prompts),
                source=src_label,
                session_files=all_files,
                conv_metadata=data["conv_metadata"],
            )
        )

    # Sort projects by last activity descending (most recent first)
    project_list.sort(key=lambda p: p.last_activity, reverse=True)
    return project_list



# --save: try each layout in order, mirroring the ISO 8601-ish shapes
# produced by the supported sources (RFC3339 with "Z", RFC3339 with a
# numeric offset, or no offset at all -- assumed UTC in that case).
_TIMESTAMP_FORMATS = (
    "%Y-%m-%dT%H:%M:%S.%f%z",
    "%Y-%m-%dT%H:%M:%S%z",
    "%Y-%m-%dT%H:%M:%S.%f",
    "%Y-%m-%dT%H:%M:%S",
)


def parse_timestamp_loose(ts: str):
    """Parse a Prompt timestamp string in any of the ISO 8601-ish shapes
    produced by the supported sources. Returns None if ts is empty or
    doesn't match any known format."""
    if not ts:
        return None
    from datetime import datetime

    normalized = ts.replace("Z", "+0000") if ts.endswith("Z") else ts
    for fmt in _TIMESTAMP_FORMATS:
        try:
            return datetime.strptime(normalized, fmt)
        except ValueError:
            continue
    return None


def build_save_document(
    projects: list[Project], source_filter: str, since=None
) -> dict:
    """Convert loaded Projects (and their Prompts) into the --save JSON
    shape. If since (a timezone-aware or naive datetime) is given, only
    prompts whose start time is on/after it are included; prompts with an
    unparseable/missing timestamp are always kept, to avoid silently
    dropping data. since (if given) is also echoed back in the document for
    traceability."""
    from datetime import datetime, timezone

    doc = {
        "generated_at": datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "source_filter": source_filter,
        "projects": [],
    }
    since_cmp = since
    if since is not None:
        doc["since"] = since.strftime("%Y-%m-%dT%H:%M:%SZ")
        if since.tzinfo is None:
            since_cmp = since.replace(tzinfo=timezone.utc)

    for proj in projects:
        prompts = proj.load_prompts()
        sp = {
            "display_name": proj.display_name,
            "cwd": proj.cwd,
            "source": proj.source,
            "prompt_count": 0,
            "prompts": [],
        }
        for p in prompts:
            if since_cmp is not None:
                t = parse_timestamp_loose(p.timestamp)
                if t is not None:
                    if t.tzinfo is None:
                        t = t.replace(tzinfo=timezone.utc)
                    if t < since_cmp:
                        continue
            sp["prompts"].append(
                {
                    "session_id": p.session_id,
                    "source": p.source,
                    "branch": p.branch,
                    "start_time": p.timestamp,
                    "end_time": p.end_timestamp,
                    "user_prompt": p.user_text,
                    "final_result": p.final_result,
                    "modified_files": [
                        {"path": path, "action": action}
                        for path, action in p.file_changes
                    ],
                }
            )
        sp["prompt_count"] = len(sp["prompts"])
        doc["projects"].append(sp)
    return doc
