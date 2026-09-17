# apl — AI Prompt Log viewer for Claude Code

A `tig`-style terminal viewer for [**Claude Code**](https://claude.com/claude-code) (Anthropic's CLI coding agent) session logs. `apl` reads the JSONL session files Claude Code already writes locally to `~/.claude/projects/` — no separate logging, no database, no API keys, nothing to configure.

> **Compatibility**: built specifically for Claude Code's local session-log format (`~/.claude/projects/**/*.jsonl`), verified against real logs from Claude Code CLI versions in the 2.x line (see [`docs/data-model.md`](docs/data-model.md)). It does not read logs from claude.ai (the web/desktop chat app), the Claude API, or other AI coding tools. This is an independent, unofficial tool — not built or endorsed by Anthropic.

```
apl        # this project's own prompts only   -> 2 panes: Prompts | Detail
apl --all  # every project you've used         -> 3 panes: Projects | Prompts | Detail
```

Panes are shown side by side and update live as you move the cursor — no screen transitions, no need to press Enter just to preview something.

```
┌ Projects ───────┐┌ Prompts ────────────────────┐┌ Detail ─────────────────────────┐
│ ai-prompt-log    ││ 2026-09-16 11:53  main  ...  ││ USER  2026-09-16 11:53:07        │
│ llm_wiki         ││ 2026-09-15 18:20  main  ...  ││ apl tool을 만들어주세요...        │
│ hermes           ││ 2026-09-14 09:02 [subagent]  ││                                  │
│ ...              ││ ...                          ││ ASSISTANT                        │
│                  ││                              ││   ▸ TOOL  Bash                   │
│                  ││                              ││     command: ls -la ...          │
└──────────────────┘└──────────────────────────────┘└──────────────────────────────────┘
```

## Install

Requires Python 3.9+ and [`uv`](https://docs.astral.sh/uv/).

```sh
git clone https://github.com/cheoljoo/ai-prompt-log.git
cd ai-prompt-log
uv tool install --editable poc/
```

This puts `apl` on your `PATH` (via `uv`'s tool bin directory). Since it's an editable install, pulling the latest source (`git pull`) is enough to pick up updates — no reinstall needed.

## Usage

```sh
cd ~/code/some-project
apl              # browse just this project's prompts (2 panes)

apl --all        # browse every project under ~/.claude/projects (3 panes)
apl --help       # usage, keybindings, this repo's URL
```

`apl` (no flag) walks up from the current directory to find the nearest ancestor that actually has logged sessions — so it also works from a subdirectory of a project, not just its root.

### Keybindings (vi-style)

| Key | Action |
|---|---|
| `j` / `k` (or ↑ / ↓) | move within the focused pane |
| `g` / `G` | jump to top / bottom |
| `Ctrl+F` / `Ctrl+B` | page down / up |
| `Ctrl+D` / `Ctrl+U` | half page down / up |
| `l`, `Tab`, `Enter` | focus the next pane to the right (drill in) |
| `h`, `Shift+Tab`, `Esc` | focus the previous pane (back) |
| `F1` | command palette (theme, screenshot, ...) |
| `q` | quit |

## How it works

Claude Code already logs every session as JSONL at `~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl`. `apl` is a **read-only viewer** over that data — it never writes to it, never uploads it anywhere, and (as of the current design) never copies it elsewhere either: `apl --all` reads `~/.claude/projects` directly, live.

A "prompt" is one user turn plus the assistant text/tool-call blocks that follow it, up to the next user turn. This also means `/clear` and context-compaction boundaries stay visible — see [`docs/data-model.md`](docs/data-model.md) for the exact parsing rules, verified against real session logs.

## Project status

This is a Python proof-of-concept (`poc/`, built with [Textual](https://textual.textualize.io/)) validating the data model and UX before a planned Go rewrite for distribution as a single static binary. See [`plan.md`](plan.md) for the full design and roadmap.

## License

Apache License, Version 2.0 — see [`LICENSE`](LICENSE).
