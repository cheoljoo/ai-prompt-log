# apl — AI Prompt Log viewer for Claude Code, Antigravity CLI (agy), OpenCode & GitHub Copilot CLI

A `tig`-style terminal viewer for [**Claude Code**](https://claude.com/claude-code), [**Antigravity CLI**](https://github.com/google-deepmind) (`agy` / Gemini), [**OpenCode**](https://opencode.ai), and [**GitHub Copilot CLI**](https://github.com/github/copilot-cli) session logs. `apl` reads local session logs directly (`~/.claude/projects/`, `~/.gemini/antigravity-cli/`, `~/.local/share/opencode/`, and `~/.copilot/session-state/`) — no separate logging daemon, no manual database setup, no API keys, nothing to configure.

> **Compatibility**: built for Claude Code's local session logs (`~/.claude/projects/**/*.jsonl`), Antigravity CLI (`~/.gemini/antigravity-cli/brain/**/transcript.jsonl` + `~/.gemini/tmp/`), OpenCode (`~/.local/share/opencode/opencode.db`), and GitHub Copilot CLI (`~/.copilot/session-state/**/events.jsonl`), verified against real logs (see [`docs/data-model.md`](docs/data-model.md)). This is an independent, unofficial tool.

```
apl               # this project's own prompts only   -> 2 panes: Prompts | Detail
apl --all         # every project you've used         -> 3 panes: Projects | Prompts | Detail
apl --opencode    # view only OpenCode prompts
apl --agy         # view only Antigravity CLI (agy) prompts
apl --claude      # view only Claude Code prompts
apl --copilot     # view only GitHub Copilot CLI prompts
apl --backup      # copy session logs to a durable backup dir (survives deleting the project)
apl --view-backup # browse that backup                -> 3 panes, same as --all
apl --save        # save current prompts to a JSON file, then exit
```

Panes are shown side by side and update live as you move the cursor — no screen transitions, no need to press Enter just to preview something.

```
┌ Projects ───────┐┌ Prompts ────────────────────┐┌ Detail ─────────────────────────┐
│ ai-prompt-log    ││ 2026-09-16 11:53  main  ...  ││ USER  2026-09-16 11:53:07        │
│ llm_wiki         ││ 2026-09-15 18:20  main  ...  ││ apl tool을 만들어주세요...        │
│ hermes           ││ 2026-09-14 09:02 [subagent]  ││                                  │
│                  ││ 2026-09-14 08:47 [bg]        ││                                  │
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

See [`docs/release.md`](docs/release.md) for how these packages are built and how a new version gets released (CI pipeline, versioning policy, troubleshooting).

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
apl              # browse this project's prompts (Claude Code, AGY, OpenCode, Copilot CLI)
apl --opencode   # browse only OpenCode prompts
apl --agy        # browse only Antigravity CLI (agy) prompts
apl --claude     # browse only Claude Code prompts
apl --copilot    # browse only GitHub Copilot CLI prompts

apl --all        # browse every project across sources (3 panes)
apl --all --opencode # browse all projects with OpenCode sessions only
apl --help       # usage, keybindings, this repo's URL

apl --backup                  # back up current project (Claude + AGY + OpenCode + Copilot CLI)
apl --backup --depth 1        # + every sibling project under the parent directory
apl --backup --all            # back up every project apl knows about (all sources)
apl --backup --all --copilot  # back up every project, Copilot CLI logs only
apl --backup --backup-dir PATH  # use a custom backup location (default: ~/ai-prompt-log.backup/)
apl --view-backup              # browse a previous --backup (3 panes, like --all)

apl --save                    # save current project's prompts to a JSON file
apl --save --all              # save every project's prompts
apl --save --copilot          # save only GitHub Copilot CLI prompts
apl --save --save-file out.json  # custom output path (default: apl-save.json, overwritten each time)
apl --save --days 7           # only include prompts from the last 7 days
apl --save --since 2026-09-01 # only include prompts on/after this date

apl --version    # or -v
```

`apl` (no flag) walks up from the current directory to find the nearest ancestor that actually has logged sessions — so it also works from a subdirectory of a project, not just its root.

### Backing up session logs

Claude Code only keeps session logs under `~/.claude/projects/<encoded-cwd>/` — delete that project directory and the logs are effectively gone. `apl --backup` copies the relevant `*.jsonl` files into a durable location (`~/ai-prompt-log.backup/` by default, independent of any one project), incrementally (unchanged files are skipped, changed files are updated, **nothing already backed up is ever deleted**).

- `apl --backup` with no `--depth` backs up only the current project (same nearest-ancestor resolution as plain `apl`).
- `apl --backup --depth N` walks up `N` directories from cwd and backs up *every* project whose real `cwd` (read from the logs themselves, not guessed from the encoded directory name) is that directory or a descendant of it — handy for backing up a whole `~/code/` tree of sibling projects in one go.
- `apl --backup --all` backs up *every* project apl knows about across all sources, regardless of cwd (like `apl --all`'s aggregate view) — mutually exclusive with `--depth`. Add a source flag (`--copilot`, `--claude`, etc.) to restrict which source gets backed up.
- `apl --view-backup` opens the same 3-pane aggregate view as `apl --all`, but rooted at the backup directory instead of the live `~/.claude/projects`.

### Saving prompts to a JSON file

`apl --save` exports the same prompts you'd see in the TUI to a JSON file, then exits — handy for scripting, archiving, or feeding into other tools. It respects the same scope/source flags as the interactive view:

- No `--all`: only the current project's prompts (same nearest-ancestor resolution as plain `apl`).
- `apl --save --all`: every project, like `apl --all`.
- Add a source flag (`--copilot`, `--claude`, `--agy`, `--opencode`, `-s/--source`) to restrict to one source.
- `--save-file PATH`: output path (default: `apl-save.json` in the current directory, overwritten on each run).
- `--since YYYY-MM-DD` or `--days N`: only include prompts starting on/after that date (mutually exclusive with each other; omit both to include everything).

Each prompt is saved with its start/end time, the user prompt, the final assistant response, and the list of modified files:

```json
{
  "generated_at": "2026-09-28T04:00:00Z",
  "source_filter": "copilot",
  "since": "2026-09-21T04:00:00Z",
  "projects": [
    {
      "display_name": "ai-prompt-log",
      "cwd": "/home/user/code/ai-prompt-log",
      "source": "copilot",
      "prompt_count": 1,
      "prompts": [
        {
          "session_id": "...",
          "source": "copilot",
          "branch": "main",
          "start_time": "2026-09-28T03:55:49.761Z",
          "end_time": "2026-09-28T04:07:05.858Z",
          "user_prompt": "...",
          "final_result": "...",
          "modified_files": [{ "path": "...", "action": "modified" }]
        }
      ]
    }
  ]
}
```

`"since"` is only present when `--since`/`--days` was given.

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

The Detail pane shows, in order: **USER** (the prompt), **FINAL-RESULT** (the assistant's concluding text, so you can see what happened at a glance), **MODIFIED FILES** (files created, modified, or deleted across the prompt's tool calls), then **ASSISTANT** (the full trace — every tool call in order, ending with that same final text again). The repetition is deliberate: skim the top sections first, and only scroll into the full trace when you need to know *how* it got there.

## Project status

There are two implementations, sharing the same data model and keybindings:

- **`cmd/apl` + `internal/` (Go)** — the recommended one for end users. A single static binary (`bubbletea`/`lipgloss`/`bubbles`), distributed via Homebrew/`.deb`/`.rpm`/prebuilt binaries above. Feature set: `apl`, `apl --all`, `apl --help`.
- **`poc/` (Python)** — the original prototype ([Textual](https://textual.textualize.io/)), used to validate the data model and UX before the Go port. Same feature set as the Go build, plus a Textual command palette on `F1` (framework-provided chrome, not ported — see the keybindings table).

See [`plan.md`](plan.md) for the full design, decision history, and roadmap.

## License

Apache License, Version 2.0 — see [`LICENSE`](LICENSE).
