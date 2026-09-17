"""TUI layer: tig-style split-pane navigation (no full-screen transitions).

  apl        (direct mode):    [ Prompts | Detail ]             2 panes,
             this project's own prompts only
  apl --all  (aggregate mode): [ Projects | Prompts | Detail ]  3 panes,
             every project under ~/.claude/projects, read directly (no copy)

Moving the cursor (j/k) in a list pane immediately updates the pane(s) to
its right, mirroring tig's split "main + diff" view rather than requiring
Enter to drill in. Enter/l still exist for moving keyboard focus rightward.
"""
from __future__ import annotations

from datetime import datetime, timezone
from pathlib import Path

from rich.text import Text
from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Horizontal, VerticalScroll
from textual.screen import Screen
from textual.widgets import DataTable, Footer, Header, Static

from . import model, source

MAX_TOOL_ARGS_SHOWN = 4
MAX_TOOL_VALUE_LEN = 160


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


def format_prompt_detail(prompt: model.Prompt) -> str:
    """Human-readable, indented rendering of one prompt + its AI result.

    Ordered USER -> FINAL-RESULT -> ASSISTANT (not USER -> ASSISTANT) so the
    prompt and its conclusion are visible immediately, with the full
    tool-by-tool trace available below only if needed -- the final result
    text is deliberately repeated at the end of ASSISTANT too, in its
    original place in the trace.

    Returns a Textual/Rich markup *string* (not a Text/renderable object) —
    passing raw Rich renderables to Static crashes on this Textual version
    (see agents/B-poc.md).
    """
    meta = [f"[dim]{escape(prompt.timestamp)}[/dim]"]
    if prompt.branch:
        meta.append(f"[magenta]{escape(prompt.branch)}[/magenta]")
    if prompt.total_tokens:
        meta.append(f"[blue]{escape(_format_tokens(prompt.total_tokens))} tokens[/blue]")
    if prompt.sidechain:
        meta.append(f"[yellow]{escape('[subagent]')}[/yellow]")

    lines = [
        f"[reverse bold cyan] USER [/reverse bold cyan] {'  '.join(meta)}",
        escape(prompt.user_text or ""),
        "",
    ]

    final_result = _final_result_text(prompt)
    if final_result:
        lines.append("[reverse bold blue] FINAL-RESULT [/reverse bold blue]")
        lines.append("")
        lines.append(escape(final_result))
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

    def show(self, prompt: model.Prompt | None) -> None:
        static = self.query_one("#detail-static", Static)
        if prompt is None:
            static.update("[dim](no prompt selected)[/dim]")
        else:
            static.update(format_prompt_detail(prompt))
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
    ]

    def __init__(self, mode: str, root_dir: Path):
        super().__init__()
        self.mode = mode
        self.root_dir = root_dir
        self._projects: list = []
        self._current_prompts: list = []

    def compose(self) -> ComposeResult:
        yield Header()
        with Horizontal():
            if self.mode == "aggregate":
                yield DataTable(id="projects-table", classes="pane")
            yield DataTable(id="prompts-table", classes="pane")
            yield DetailPane(id="detail-pane", classes="pane")
        yield Footer()

    def on_mount(self) -> None:
        prompts_table = self.query_one("#prompts-table", DataTable)
        prompts_table.cursor_type = "row"
        prompts_table.add_column("Date")
        prompts_table.add_column("Branch")
        prompts_table.add_column("Tokens")
        prompts_table.add_column("Tag")
        prompts_table.add_column("Summary")
        prompts_table.border_title = "Prompts"
        self.query_one("#detail-pane", DetailPane).border_title = "Detail"

        if self.mode == "aggregate":
            projects_table = self.query_one("#projects-table", DataTable)
            projects_table.cursor_type = "row"
            projects_table.add_column("Project")
            projects_table.add_column("Prompts")
            projects_table.add_column("Last activity")
            projects_table.border_title = "Projects"
            self._projects = model.load_projects(self.root_dir)
            for idx, p in enumerate(self._projects):
                projects_table.add_row(
                    Text(p.display_name, style="bold cyan"),
                    Text(str(p.prompt_count), style="dim"),
                    Text(p.last_activity[:19] or "-", style=_recency_style(p.last_activity)),
                    key=str(idx),
                )
            projects_table.focus()
            if self._projects:
                self._load_prompts_for(self._projects[0])
        else:
            project = model.load_project(self.root_dir)
            self._load_prompts_for(project)
            prompts_table.focus()

    def _load_prompts_for(self, project: model.Project) -> None:
        table = self.query_one("#prompts-table", DataTable)
        table.clear()
        self._current_prompts = list(reversed(project.load_prompts()))
        if not self._current_prompts:
            table.add_row(Text("(no prompts found)", style="dim"))
            self.query_one("#detail-pane", DetailPane).show(None)
            return
        for idx, p in enumerate(self._current_prompts):
            tag = "[cmd]" if p.is_command else ("[subagent]" if p.sidechain else "")
            table.add_row(
                Text(p.timestamp[:19] or "-", style="dim"),
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
            self.query_one("#detail-pane", DetailPane).show(self._current_prompts[int(row_key)])

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
        self.focus_previous()


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
        width: 28;
    }
    #prompts-table {
        width: 45%;
    }
    #detail-pane {
        width: 1fr;
    }
    """

    def __init__(self, aggregate: bool = False, root_override: Path | None = None):
        super().__init__()
        self.mode, self.root_dir = source.detect_mode(
            Path.cwd(), aggregate=aggregate, root_override=root_override
        )

    def on_mount(self) -> None:
        self.push_screen(AplScreen(self.mode, self.root_dir))
