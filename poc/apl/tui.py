"""TUI layer: tig-style split-pane navigation (no full-screen transitions).

  apl        (direct mode):    [ Prompts | Detail ]             2 panes,
             this project's own prompts only
  apl --all  (aggregate mode): [ Projects | Prompts | Detail ]  3 panes,
             every project under ~/.claude/projects, read directly (no copy)

Moving the cursor (j/k) in a list pane immediately updates the pane(s) to
its right, mirroring tig's split "main + diff" view rather than requiring
Enter to drill in. Enter/l still exist for moving keyboard focus rightward.

Search (vi-style, over the Prompts list):
  /keyword   search User Prompt OR Final Result text
  <keyword   search User Prompt text only
  >keyword   search Final Result text only
  n / p      jump to next / previous match (wraps around)
"""
from __future__ import annotations

from datetime import datetime, timezone
from pathlib import Path

from rich.text import Text
from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Horizontal, VerticalScroll
from textual.screen import Screen
from textual.widgets import DataTable, Footer, Header, Input, Static

from . import model, source

MAX_TOOL_ARGS_SHOWN = 4
MAX_TOOL_VALUE_LEN = 160

SEARCH_PREFIX = {"both": "/", "user": "<", "final": ">"}


def escape(text: str) -> str:
    """Unconditionally neutralize every literal '[' for Textual markup.

    Both rich.markup.escape() and textual.markup.escape() are "smart" and
    skip escaping brackets that don't *look* like a tag (e.g. "[{'a': 1}]").
    That heuristic doesn't match Textual 8.2.8's actual Content.from_markup
    parser closely enough — real tool_use arguments (dict/list reprs full of
    brackets) reliably desync the tag stack and crash the whole detail pane.
    Escaping every '[' unconditionally is slightly more verbose but never
    wrong, since a backslash-escaped bracket always renders as a literal
    character in both parsers.
    """
    return text.replace("\\", "\\\\").replace("[", "\\[")


def _source_style(src: str) -> str:
    """Color for a given prompt/project 'source' label (may be a
    '+'-joined combo like 'agy+claude')."""
    if "agy" in src:
        return "green"
    if "opencode" in src:
        return "magenta"
    if "copilot" in src:
        return "bright_magenta"
    if "gemini" in src:
        return "blue"
    if "claude" in src:
        return "cyan"
    return "dim"


def _recency_style(ts: str) -> str:
    if not ts:
        return "dim"
    try:
        dt = datetime.fromisoformat(ts.replace("Z", "+00:00"))
    except ValueError:
        return "dim"
    delta = datetime.now(timezone.utc) - dt
    if delta.days < 1:
        return "green"
    if delta.days < 7:
        return "yellow"
    return "dim"


def _format_tokens(n: int) -> str:
    if n <= 0:
        return "-"
    if n >= 1_000_000:
        return f"{n / 1_000_000:.1f}M"
    if n >= 1_000:
        return f"{n / 1_000:.1f}k"
    return str(n)


def _final_result_text(prompt: model.Prompt) -> str:
    """The trailing run of "text" blocks at the end of the assistant's
    response -- i.e. its concluding remarks, after any tool calls. Empty if
    the response ends on a tool_use with no closing text."""
    texts = []
    for block in reversed(prompt.blocks):
        if block.kind != "text":
            break
        texts.append(block.text)
    return "\n\n".join(reversed(texts))


def _highlight(text: str, term: str) -> str:
    """Escape `text` for Textual markup, wrapping every case-insensitive
    occurrence of `term` in reverse-video so search matches stand out."""
    if not term:
        return escape(text)
    lower_text = text.lower()
    lower_term = term.lower()
    tlen = len(term)
    parts = []
    start = 0
    idx = lower_text.find(lower_term, start)
    if idx == -1:
        return escape(text)
    while idx != -1:
        parts.append(escape(text[start:idx]))
        parts.append(
            f"[reverse bold yellow]{escape(text[idx : idx + tlen])}[/reverse bold yellow]"
        )
        start = idx + tlen
        idx = lower_text.find(lower_term, start)
    parts.append(escape(text[start:]))
    return "".join(parts)


def prompt_matches_search(prompt: model.Prompt, term: str, mode: str) -> bool:
    """True if `prompt` contains `term` (case-insensitive) in the field(s)
    selected by `mode`: "user" (User Prompt text only), "final" (Final
    Result text only), or "both" (either field)."""
    if not term:
        return False
    term_l = term.lower()
    if mode in ("both", "user") and term_l in (prompt.user_text or "").lower():
        return True
    if mode in ("both", "final") and term_l in _final_result_text(prompt).lower():
        return True
    return False


def format_prompt_detail(
    prompt: model.Prompt, search_term: str = "", search_mode: str = "both"
) -> str:
    """Human-readable, indented rendering of one prompt + its AI result.

    Ordered USER -> FINAL-RESULT -> ASSISTANT (not USER -> ASSISTANT) so the
    prompt and its conclusion are visible immediately, with the full
    tool-by-tool trace available below only if needed -- the final result
    text is deliberately repeated at the end of ASSISTANT too, in its
    original place in the trace.

    If `search_term` is set, occurrences of it are highlighted within the
    User Prompt and/or Final Result text according to `search_mode`.

    Returns a Textual/Rich markup *string* (not a Text/renderable object) —
    passing raw Rich renderables to Static crashes on this Textual version
    (see agents/B-poc.md).
    """
    meta = [f"[dim]{escape(prompt.timestamp)}[/dim]"]
    if prompt.source:
        src_color = _source_style(prompt.source)
        meta.append(f"[{src_color}]{escape(prompt.source)}[/{src_color}]")
    if prompt.branch:
        meta.append(f"[magenta]{escape(prompt.branch)}[/magenta]")
    if prompt.total_tokens:
        meta.append(f"[blue]{escape(_format_tokens(prompt.total_tokens))} tokens[/blue]")
    if prompt.sidechain:
        meta.append(f"[yellow]{escape('[subagent]')}[/yellow]")

    user_text = prompt.user_text or ""
    user_rendered = (
        _highlight(user_text, search_term)
        if search_mode in ("both", "user")
        else escape(user_text)
    )

    lines = [
        f"[reverse bold cyan] USER [/reverse bold cyan] {'  '.join(meta)}",
        user_rendered,
        "",
    ]

    final_result = _final_result_text(prompt)
    if final_result:
        final_rendered = (
            _highlight(final_result, search_term)
            if search_mode in ("both", "final")
            else escape(final_result)
        )
        lines.append("[reverse bold blue] FINAL-RESULT [/reverse bold blue]")
        lines.append("")
        lines.append(final_rendered)
        lines.append("")

    file_changes = prompt.file_changes
    if file_changes:
        lines.append("[reverse bold magenta] MODIFIED FILES [/reverse bold magenta]")
        lines.append("")
        symbols = {
            "created": "[green]+[/green]",
            "modified": "[yellow]~[/yellow]",
            "deleted": "[red]-[/red]",
        }
        for path, action in file_changes:
            symbol = symbols.get(action, "[dim]?[/dim]")
            lines.append(f"  {symbol} {escape(path)}")
        lines.append("")

    if prompt.blocks:
        lines.append("[reverse bold green] ASSISTANT [/reverse bold green]")
        lines.append("")
        for block in prompt.blocks:
            if block.kind == "text":
                lines.append(escape(block.text))
                lines.append("")
            elif block.kind == "tool_use":
                lines.append(f"  [yellow]▸ TOOL[/yellow]  [bold]{escape(block.tool_name)}[/bold]")
                items = list(block.tool_input.items())
                for k, v in items[:MAX_TOOL_ARGS_SHOWN]:
                    s = str(v).replace("\n", " ⏎ ")
                    if len(s) > MAX_TOOL_VALUE_LEN:
                        s = s[:MAX_TOOL_VALUE_LEN] + f"… (전체 {len(str(v))}자)"
                    lines.append(f"      [dim]{escape(str(k))}:[/dim] {escape(s)}")
                if len(items) > MAX_TOOL_ARGS_SHOWN:
                    lines.append(f"      [dim]… 외 {len(items) - MAX_TOOL_ARGS_SHOWN}개 인자 생략[/dim]")
                lines.append("")
    else:
        lines.append("[dim](no assistant response captured)[/dim]")
    return "\n".join(lines)


class DetailPane(VerticalScroll):
    can_focus = True

    def compose(self) -> ComposeResult:
        yield Static("", id="detail-static")

    def show(
        self,
        prompt: model.Prompt | None,
        search_term: str = "",
        search_mode: str = "both",
    ) -> None:
        static = self.query_one("#detail-static", Static)
        if prompt is None:
            static.update("[dim](no prompt selected)[/dim]")
        else:
            static.update(
                format_prompt_detail(
                    prompt, search_term=search_term, search_mode=search_mode
                )
            )
        self.scroll_home(animate=False)


class AplScreen(Screen):
    BINDINGS = [
        Binding("j,down", "cursor_down", "Down", key_display="j"),
        Binding("k,up", "cursor_up", "Up", key_display="k"),
        Binding("g", "cursor_top", "Top", show=False),
        Binding("G", "cursor_bottom", "Bottom", show=False),
        Binding("l,tab", "focus_next_pane", "Next pane", key_display="l"),
        Binding("h,shift+tab,escape", "focus_prev_pane", "Prev pane", key_display="h"),
        Binding("ctrl+f,space", "page_down", "Page down", key_display="^F/Space"),
        Binding("ctrl+b", "page_up", "Page up", key_display="^B"),
        Binding("ctrl+d", "half_page_down", "½ page down", key_display="^D"),
        Binding("ctrl+u", "half_page_up", "½ page up", key_display="^U"),
        Binding("ctrl+l", "reload", "Reload", key_display="^L"),
        Binding("/", "start_search_both", "Search", key_display="/"),
        Binding("<", "start_search_user", "Search USER", show=False),
        Binding(">", "start_search_final", "Search RESULT", show=False),
        Binding("n", "search_next", "Next match", key_display="n"),
        Binding("p", "search_prev", "Prev match", key_display="p"),
    ]

    CHANGE_CHECK_INTERVAL_SECONDS = 30

    def __init__(self, mode: str, root_dir: Path, source_filter: str = "all"):
        super().__init__()
        self.mode = mode
        self.root_dir = root_dir
        self.source_filter = source_filter
        self._projects: list = []
        self._current_prompts: list = []
        self._current_project: model.Project | None = None
        self._current_mtimes: dict = {}
        self._changes_pending = False
        self._search_term = ""
        self._search_mode = "both"
        self._search_matches: list[int] = []
        self._pending_search_mode = "both"

    def compose(self) -> ComposeResult:
        yield Header()
        with Horizontal():
            if self.mode == "aggregate":
                yield DataTable(id="projects-table", classes="pane")
            yield DataTable(id="prompts-table", classes="pane")
            yield DetailPane(id="detail-pane", classes="pane")
        with Horizontal(id="search-bar"):
            yield Static("/", id="search-prefix")
            yield Input(id="search-input", placeholder="search keyword…")
        yield Footer()

    def on_mount(self) -> None:
        prompts_table = self.query_one("#prompts-table", DataTable)
        prompts_table.cursor_type = "row"
        prompts_table.add_column("Date")
        prompts_table.add_column("Source")
        prompts_table.add_column("Branch")
        prompts_table.add_column("Tokens")
        prompts_table.add_column("Tag")
        prompts_table.add_column("Summary")
        prompts_table.border_title = "Prompts"
        self.query_one("#detail-pane", DetailPane).border_title = "Detail"
        self.query_one("#search-bar").display = False

        if self.mode == "aggregate":
            projects_table = self.query_one("#projects-table", DataTable)
            projects_table.cursor_type = "row"
            projects_table.add_column("Project")
            projects_table.add_column("Source")
            projects_table.add_column("Prompts")
            projects_table.add_column("Last activity")
            projects_table.border_title = "Projects"
            self._projects = model.load_projects(self.root_dir, source_filter=self.source_filter)
            for idx, p in enumerate(self._projects):
                src_style = _source_style(p.source)
                projects_table.add_row(
                    Text(p.display_name, style="bold cyan"),
                    Text(p.source, style=src_style),
                    Text(str(p.prompt_count), style="dim"),
                    Text(p.last_activity[:19] or "-", style=_recency_style(p.last_activity)),
                    key=str(idx),
                )
            projects_table.focus()
            if self._projects:
                self._load_prompts_for(self._projects[0])
        else:
            project = model.load_project(self.root_dir, source_filter=self.source_filter)
            self._load_prompts_for(project)
            prompts_table.focus()

        self.set_interval(self.CHANGE_CHECK_INTERVAL_SECONDS, self._check_for_changes)

    @staticmethod
    def _snapshot_mtimes(project: model.Project) -> dict:
        mtimes = {}
        for f in project.get_session_files():
            try:
                mtimes[str(f)] = f.stat().st_mtime
            except OSError:
                pass
        return mtimes

    def _has_changes(self) -> bool:
        if self._current_project is None:
            return False
        return self._snapshot_mtimes(self._current_project) != self._current_mtimes

    def _check_for_changes(self) -> None:
        if self._changes_pending or self._current_project is None:
            return
        if self._has_changes():
            self._changes_pending = True
            self.query_one("#prompts-table", DataTable).border_subtitle = "⚠ changes available — Ctrl+L to reload"
            self.notify("변경 사항이 있습니다 — Ctrl+L로 새로고침하세요", title="apl", timeout=5)

    def action_reload(self) -> None:
        if self._current_project is None:
            return
        if not self._has_changes():
            self.notify("변경 없음", title="apl", timeout=2)
            return
        # re-derive the project from its own directory so prompt_count/
        # last_activity are recomputed too, not just the prompt list
        refreshed = model.load_project(
            self._current_project.dir_path, source_filter=self.source_filter
        )
        self._load_prompts_for(refreshed)
        if self.mode == "aggregate":
            for idx, p in enumerate(self._projects):
                if p.dir_path == refreshed.dir_path:
                    self._projects[idx] = refreshed
                    break
        self.notify("다시 불러왔습니다", title="apl", timeout=2)

    def _load_prompts_for(self, project: model.Project) -> None:
        table = self.query_one("#prompts-table", DataTable)
        table.border_subtitle = ""
        self._changes_pending = False
        self._search_term = ""
        self._search_matches = []
        self._current_project = project
        self._current_mtimes = self._snapshot_mtimes(project)
        table.clear()
        self._current_prompts = list(reversed(project.load_prompts()))
        if not self._current_prompts:
            table.add_row(Text("(no prompts found)", style="dim"))
            self.query_one("#detail-pane", DetailPane).show(None)
            return
        for idx, p in enumerate(self._current_prompts):
            if p.is_command:
                tag = "[cmd]"
            elif p.is_task_notification:
                tag = "[bg]"
            elif p.sidechain:
                tag = "[subagent]"
            else:
                tag = ""
            src_style = _source_style(p.source or "claude")
            table.add_row(
                Text(p.timestamp[:19] or "-", style="dim"),
                Text(p.source or "claude", style=src_style),
                Text(p.branch or "-", style="magenta"),
                Text(_format_tokens(p.total_tokens), style="blue"),
                Text(tag, style="yellow"),
                Text(p.summary),
                key=str(idx),
            )
        table.move_cursor(row=0)
        self.query_one("#detail-pane", DetailPane).show(self._current_prompts[0])

    def on_data_table_row_highlighted(self, event: DataTable.RowHighlighted) -> None:
        row_key = event.row_key.value
        if row_key is None:
            return
        if self.mode == "aggregate" and event.data_table.id == "projects-table":
            self._load_prompts_for(self._projects[int(row_key)])
        elif event.data_table.id == "prompts-table" and self._current_prompts:
            self.query_one("#detail-pane", DetailPane).show(
                self._current_prompts[int(row_key)],
                search_term=self._search_term,
                search_mode=self._search_mode,
            )

    def on_data_table_row_selected(self, event: DataTable.RowSelected) -> None:
        # DataTable claims "enter" for itself (action_select_cursor) before it
        # can bubble to our Screen BINDINGS, so we move focus here instead,
        # reacting to the RowSelected message it posts.
        self.action_focus_next_pane()

    # -- vi-style navigation -------------------------------------------------

    def action_cursor_down(self) -> None:
        focused = self.focused
        if isinstance(focused, DataTable):
            focused.action_cursor_down()
        elif isinstance(focused, VerticalScroll):
            focused.action_scroll_down()

    def action_cursor_up(self) -> None:
        focused = self.focused
        if isinstance(focused, DataTable):
            focused.action_cursor_up()
        elif isinstance(focused, VerticalScroll):
            focused.action_scroll_up()

    def action_cursor_top(self) -> None:
        focused = self.focused
        if isinstance(focused, DataTable):
            focused.move_cursor(row=0)
        elif isinstance(focused, VerticalScroll):
            focused.action_scroll_home()

    def action_cursor_bottom(self) -> None:
        focused = self.focused
        if isinstance(focused, DataTable):
            focused.move_cursor(row=focused.row_count - 1)
        elif isinstance(focused, VerticalScroll):
            focused.action_scroll_end()

    def action_page_down(self) -> None:
        focused = self.focused
        if isinstance(focused, (DataTable, VerticalScroll)):
            focused.action_page_down()

    def action_page_up(self) -> None:
        focused = self.focused
        if isinstance(focused, (DataTable, VerticalScroll)):
            focused.action_page_up()

    def action_half_page_down(self) -> None:
        focused = self.focused
        half = max(1, focused.size.height // 2)
        if isinstance(focused, DataTable):
            focused.move_cursor(row=min(focused.row_count - 1, focused.cursor_row + half))
        elif isinstance(focused, VerticalScroll):
            focused.scroll_relative(y=half, animate=False)

    def action_half_page_up(self) -> None:
        focused = self.focused
        half = max(1, focused.size.height // 2)
        if isinstance(focused, DataTable):
            focused.move_cursor(row=max(0, focused.cursor_row - half))
        elif isinstance(focused, VerticalScroll):
            focused.scroll_relative(y=-half, animate=False)

    def action_focus_next_pane(self) -> None:
        self.focus_next()

    def action_focus_prev_pane(self) -> None:
        if self.query_one("#search-bar").display:
            self._cancel_search()
            return
        self.focus_previous()

    # -- search ---------------------------------------------------------

    def _begin_search(self, mode: str) -> None:
        self._pending_search_mode = mode
        bar = self.query_one("#search-bar")
        bar.display = True
        self.query_one("#search-prefix", Static).update(SEARCH_PREFIX[mode])
        inp = self.query_one("#search-input", Input)
        inp.value = ""
        inp.focus()

    def action_start_search_both(self) -> None:
        self._begin_search("both")

    def action_start_search_user(self) -> None:
        self._begin_search("user")

    def action_start_search_final(self) -> None:
        self._begin_search("final")

    def _cancel_search(self) -> None:
        self.query_one("#search-bar").display = False
        table = self.query_one("#prompts-table", DataTable)
        table.focus()

    def on_input_submitted(self, event: Input.Submitted) -> None:
        if event.input.id != "search-input":
            return
        self._cancel_search()
        term = event.value.strip()
        if not term:
            # Empty search clears any active search/highlighting.
            self._search_term = ""
            self._search_matches = []
            self.query_one("#prompts-table", DataTable).border_subtitle = ""
            if self._current_prompts:
                table = self.query_one("#prompts-table", DataTable)
                self.query_one("#detail-pane", DetailPane).show(
                    self._current_prompts[table.cursor_row]
                )
            return

        self._search_term = term
        self._search_mode = self._pending_search_mode
        self._search_matches = [
            i
            for i, p in enumerate(self._current_prompts)
            if prompt_matches_search(p, self._search_term, self._search_mode)
        ]
        if self._search_matches:
            self.action_search_next()
        else:
            prefix = SEARCH_PREFIX[self._search_mode]
            self.query_one("#prompts-table", DataTable).border_subtitle = (
                f"{prefix}{term}  (no matches)"
            )
            self.notify(f"'{term}': 검색 결과 없음", title="apl", timeout=2)

    def _jump_to_search_match(self, target: int) -> None:
        table = self.query_one("#prompts-table", DataTable)
        table.move_cursor(row=target)
        pos = self._search_matches.index(target) + 1
        prefix = SEARCH_PREFIX[self._search_mode]
        table.border_subtitle = (
            f"{prefix}{self._search_term}  ({pos}/{len(self._search_matches)})"
        )

    def action_search_next(self) -> None:
        if not self._search_term or not self._search_matches:
            return
        table = self.query_one("#prompts-table", DataTable)
        cur = table.cursor_row
        later = [i for i in self._search_matches if i > cur]
        target = later[0] if later else self._search_matches[0]
        self._jump_to_search_match(target)

    def action_search_prev(self) -> None:
        if not self._search_term or not self._search_matches:
            return
        table = self.query_one("#prompts-table", DataTable)
        cur = table.cursor_row
        earlier = [i for i in self._search_matches if i < cur]
        target = earlier[-1] if earlier else self._search_matches[-1]
        self._jump_to_search_match(target)


class AplApp(App):
    # q is a priority binding so it always quits, regardless of which pane
    # (DataTable / DetailPane) currently holds focus.
    BINDINGS = [Binding("q", "quit", "Quit", priority=True)]

    # Textual's built-in command palette (fuzzy-searchable menu of commands
    # like theme toggling, screenshots, quitting) defaults to ctrl+p, which
    # collides with Termius's own shortcut. Moved to F1.
    COMMAND_PALETTE_BINDING = "f1"

    CSS = """
    Screen {
        background: $surface;
    }
    .pane {
        border: round $panel;
    }
    .pane:focus {
        border: heavy $accent;
    }
    #projects-table {
        width: 38;
    }
    #prompts-table {
        width: 45%;
    }
    #detail-pane {
        width: 1fr;
    }
    #search-bar {
        height: 3;
        background: $panel;
        border: heavy $accent;
    }
    #search-prefix {
        width: 3;
        content-align: center middle;
        text-style: bold;
        color: $accent;
    }
    #search-input {
        width: 1fr;
    }
    """

    def __init__(
        self,
        aggregate: bool = False,
        root_override: Path | None = None,
        source_filter: str = "all",
    ):
        super().__init__()
        self.source_filter = source_filter
        self.mode, self.root_dir = source.detect_mode(
            Path.cwd(),
            aggregate=aggregate,
            root_override=root_override,
            source_filter=source_filter,
        )

    def on_mount(self) -> None:
        self.push_screen(
            AplScreen(self.mode, self.root_dir, source_filter=self.source_filter)
        )
