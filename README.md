# apl — AI Prompt Log viewer for Claude Code

A `tig`-style terminal viewer for [**Claude Code**](https://claude.com/claude-code) (Anthropic's CLI coding agent) session logs. `apl` reads the JSONL session files Claude Code already writes locally to `~/.claude/projects/` — no separate logging, no database, no API keys, nothing to configure.

> **Compatibility**: built specifically for Claude Code's local session-log format (`~/.claude/projects/**/*.jsonl`), verified against real logs from Claude Code CLI versions in the 2.x line (see [`docs/data-model.md`](docs/data-model.md)). It does not read logs from claude.ai (the web/desktop chat app), the Claude API, or other AI coding tools. This is an independent, unofficial tool — not built or endorsed by Anthropic.

```
apl               # this project's own prompts only   -> 2 panes: Prompts | Detail
apl --all         # every project you've used         -> 3 panes: Projects | Prompts | Detail
apl --backup      # copy session logs to a durable backup dir (survives deleting the project)
apl --view-backup # browse that backup                -> 3 panes, same as --all
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

**Homebrew** (macOS and Linux, no Python/uv needed — recommended):

```sh
brew install cheoljoo/apl/apl
```

**Manually**: download a prebuilt binary (Linux/macOS/Windows, amd64/arm64) or a `.deb`/`.rpm` from the [latest release](https://github.com/cheoljoo/ai-prompt-log/releases/latest).

**From source (Go)**:

```sh
go install github.com/cheoljoo/ai-prompt-log/cmd/apl@latest
```

**Python POC** (the original prototype implementation, `poc/` — requires Python 3.9+ and [`uv`](https://docs.astral.sh/uv/); see [Project status](#project-status)):

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

apl --backup                  # back up just the current project
apl --backup --depth 1        # + every sibling project under the parent directory
apl --backup --backup-dir PATH  # use a custom backup location (default: ~/ai-prompt-log.backup/)
apl --view-backup              # browse a previous --backup (3 panes, like --all)

apl --version    # or -v
```

`apl` (no flag) walks up from the current directory to find the nearest ancestor that actually has logged sessions — so it also works from a subdirectory of a project, not just its root.

### Backing up session logs

Claude Code only keeps session logs under `~/.claude/projects/<encoded-cwd>/` — delete that project directory and the logs are effectively gone. `apl --backup` copies the relevant `*.jsonl` files into a durable location (`~/ai-prompt-log.backup/` by default, independent of any one project), incrementally (unchanged files are skipped, changed files are updated, **nothing already backed up is ever deleted**).

- `apl --backup` with no `--depth` backs up only the current project (same nearest-ancestor resolution as plain `apl`).
- `apl --backup --depth N` walks up `N` directories from cwd and backs up *every* project whose real `cwd` (read from the logs themselves, not guessed from the encoded directory name) is that directory or a descendant of it — handy for backing up a whole `~/code/` tree of sibling projects in one go.
- `apl --view-backup` opens the same 3-pane aggregate view as `apl --all`, but rooted at the backup directory instead of the live `~/.claude/projects`.

### Keybindings (vi-style)

| Key | Action |
|---|---|
| `j` / `k` (or ↑ / ↓) | move within the focused pane |
| `g` / `G` | jump to top / bottom |
| `Ctrl+F` / `Ctrl+B` / `Space` | page down / up (`Space` = page down) |
| `Ctrl+D` / `Ctrl+U` | half page down / up |
| `l`, `Tab`, `Enter` | focus the next pane to the right (drill in) |
| `h`, `Shift+Tab`, `Esc` | focus the previous pane (back) |
| `Ctrl+L` | reload the current project's prompts if its session files changed since the last load (no-op otherwise) |
| `F1` | command palette (theme, screenshot, ...) — Python build only |
| `q` | quit |

apl checks the current project's session files every 30 seconds; if they changed, it shows a notice that `Ctrl+L` will pick up the new data — it never reloads automatically.

## How it works

Claude Code already logs every session as JSONL at `~/.claude/projects/<encoded-cwd>/<session-uuid>.jsonl`. `apl` is a **read-only viewer** over that data — it never writes to it, never uploads it anywhere, and (as of the current design) never copies it elsewhere either: `apl --all` reads `~/.claude/projects` directly, live.

A "prompt" is one user turn plus the assistant text/tool-call blocks that follow it, up to the next user turn. This also means `/clear` and context-compaction boundaries stay visible — see [`docs/data-model.md`](docs/data-model.md) for the exact parsing rules, verified against real session logs.

The Detail pane shows, in order: **USER** (the prompt), **FINAL-RESULT** (the assistant's concluding text, so you can see what happened at a glance), then **ASSISTANT** (the full trace — every tool call in order, ending with that same final text again). The repetition is deliberate: skim the top two sections first, and only scroll into the full trace when you need to know *how* it got there.

## Project status

There are two implementations, sharing the same data model and keybindings:

- **`cmd/apl` + `internal/` (Go)** — the recommended one for end users. A single static binary (`bubbletea`/`lipgloss`/`bubbles`), distributed via Homebrew/`.deb`/`.rpm`/prebuilt binaries above. Feature set: `apl`, `apl --all`, `apl --help`.
- **`poc/` (Python)** — the original prototype ([Textual](https://textual.textualize.io/)), used to validate the data model and UX before the Go port. Same feature set as the Go build, plus a Textual command palette on `F1` (framework-provided chrome, not ported — see the keybindings table).

See [`plan.md`](plan.md) for the full design, decision history, and roadmap.

## License

Apache License, Version 2.0 — see [`LICENSE`](LICENSE).
