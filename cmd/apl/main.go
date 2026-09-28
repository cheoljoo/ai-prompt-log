// Command apl is a tig-style terminal viewer for Claude Code session logs.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cheoljoo/ai-prompt-log/internal/backup"
	"github.com/cheoljoo/ai-prompt-log/internal/model"
	"github.com/cheoljoo/ai-prompt-log/internal/source"
	"github.com/cheoljoo/ai-prompt-log/internal/tui"
)

const gitURL = "https://github.com/cheoljoo/ai-prompt-log"

// version is set at build time via -ldflags "-X main.version=...", which
// .goreleaser.yaml does for every release. For `go install .../cmd/apl@vX.Y.Z`
// or a plain `go build`, it falls back to the module version embedded by
// the Go toolchain (debug.ReadBuildInfo), then finally to "dev".
var version = "dev"

func resolvedVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

const keybindingsHelp = `Keybindings (vi-style):
  j/k, up/down     move within the focused pane
  g/G              jump to top / bottom
  Ctrl+F/Ctrl+B/Space  page down / up
  Ctrl+D/Ctrl+U    half page down / up
  l, Tab, Enter    focus next pane (drill in)
  h, Shift+Tab, Esc  focus previous pane (back)
  q                quit
`

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "apl: "+format+"\n", args...)
	flag.Usage()
	os.Exit(2)
}

func main() {
	all := flag.Bool("all", false, "Browse every project (3-pane aggregate view)")
	flag.BoolVar(all, "a", false, "shorthand for --all")
	doBackup := flag.Bool("backup", false, "Copy session jsonl files into --backup-dir (default ~/ai-prompt-log.backup/), incrementally, and exit. Never deletes anything already in the backup.")
	viewBackup := flag.Bool("view-backup", false, "Browse a previous --backup (3-pane aggregate view, rooted at --backup-dir)")
	depth := flag.Int("depth", 0, "With --backup: walk up N directories from cwd and back up every project whose real cwd is that directory or a descendant of it. Omit to back up only the current project.")
	backupDir := flag.String("backup-dir", "", "Backup directory for --backup / --view-backup (default: ~/ai-prompt-log.backup/)")
	doSave := flag.Bool("save", false, "Save the current view (respecting --all/--source filters, --since/--days) to a JSON file -- start/end time, user prompt, final result, and modified files per prompt -- then exit.")
	saveFile := flag.String("save-file", "", "Output path for --save (default: apl-save.json in the current directory, overwritten each time)")
	since := flag.String("since", "", "With --save: only include prompts on/after this date (YYYY-MM-DD). Default: no date filtering (include everything).")
	days := flag.Int("days", 0, "With --save: only include prompts from the last N days. Mutually exclusive with --since. Default: no date filtering (include everything).")
	sourceFlag := flag.String("source", "all", "AI assistant log source to display (all, claude, agy, gemini, opencode, copilot)")
	flag.StringVar(sourceFlag, "s", "all", "shorthand for --source")
	onlyAgy := flag.Bool("agy", false, "Display only Antigravity CLI (agy) logs")
	onlyClaude := flag.Bool("claude", false, "Display only Claude Code logs")
	onlyGemini := flag.Bool("gemini", false, "Display only Antigravity / Gemini CLI logs (alias for --agy)")
	onlyOpencode := flag.Bool("opencode", false, "Display only OpenCode logs")
	onlyCopilot := flag.Bool("copilot", false, "Display only GitHub Copilot CLI logs")
	showVersion := flag.Bool("version", false, "Print the apl version and exit")
	flag.BoolVar(showVersion, "v", false, "shorthand for --version")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "apl (AI Prompt Log viewer) - a tig-style TUI for browsing Claude Code, Antigravity CLI (agy / gemini),\n")
		fmt.Fprintf(os.Stderr, "OpenCode, and GitHub Copilot CLI session logs, read directly from ~/.claude/projects, ~/.gemini/antigravity-cli,\n")
		fmt.Fprintf(os.Stderr, "~/.local/share/opencode, and ~/.copilot/session-state (no copying).\n\n")
		fmt.Fprintf(os.Stderr, "Plain `apl` shows just the current project's own prompts (2 panes: Prompts | Detail).\n")
		fmt.Fprintf(os.Stderr, "`apl --all` shows every project at once (3 panes: Projects | Prompts | Detail).\n\n")
		fmt.Fprintf(os.Stderr, "`apl --backup` copies session logs into a durable backup directory (outside\n")
		fmt.Fprintf(os.Stderr, "live directories, so it survives a project directory being deleted).\n")
		fmt.Fprintf(os.Stderr, "`apl --view-backup` browses that backup with the same 3-pane view.\n\n")
		fmt.Fprintf(os.Stderr, "`apl --save` writes the current view (respecting --all/--source/--since/--days)\n")
		fmt.Fprintf(os.Stderr, "to a JSON file (start/end time, user prompt, final result, modified files per prompt) and exits.\n")
		fmt.Fprintf(os.Stderr, "Without --save-file, always writes to apl-save.json in the current directory (overwritten each run).\n\n")
		fmt.Fprintf(os.Stderr, "Usage: apl [-a|--all | --backup | --view-backup | --save] [--depth N] [--backup-dir PATH]\n")
		fmt.Fprintf(os.Stderr, "           [--save-file PATH] [--since YYYY-MM-DD | --days N]\n")
		fmt.Fprintf(os.Stderr, "           [-s|--source {all,claude,agy,gemini,opencode,copilot} | --agy | --claude | --gemini | --opencode | --copilot]\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n%s\napl %s\nSource: %s\n", keybindingsHelp, resolvedVersion(), gitURL)
	}
	flag.Parse()

	if *showVersion {
		fmt.Printf("apl %s\n", resolvedVersion())
		return
	}

	depthGiven := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "depth" {
			depthGiven = true
		}
	})

	sourceFilter := *sourceFlag
	if *onlyAgy || *onlyGemini {
		sourceFilter = "agy"
	} else if *onlyClaude {
		sourceFilter = "claude"
	} else if *onlyOpencode {
		sourceFilter = "opencode"
	} else if *onlyCopilot {
		sourceFilter = "copilot"
	}
	switch sourceFilter {
	case "all", "claude", "agy", "gemini", "opencode", "copilot":
	default:
		fail("invalid source %q: choose from all, claude, agy, gemini, opencode, copilot", sourceFilter)
	}

	modeCount := 0
	for _, v := range []bool{*all, *doBackup, *viewBackup} {
		if v {
			modeCount++
		}
	}
	if modeCount > 1 {
		fail("--all, --backup, and --view-backup are mutually exclusive")
	}
	if *doSave && (*doBackup || *viewBackup) {
		fail("--save is mutually exclusive with --backup and --view-backup")
	}
	if depthGiven && !*doBackup {
		fail("--depth only makes sense with --backup")
	}
	if *backupDir != "" && !(*doBackup || *viewBackup) {
		fail("--backup-dir only makes sense with --backup or --view-backup")
	}
	if *saveFile != "" && !*doSave {
		fail("--save-file only makes sense with --save")
	}
	if *since != "" && *days != 0 {
		fail("--since and --days are mutually exclusive")
	}
	if (*since != "" || *days != 0) && !*doSave {
		fail("--since/--days only make sense with --save")
	}

	dir := backup.DefaultDir()
	if *backupDir != "" {
		dir = *backupDir
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "apl:", err)
		os.Exit(1)
	}

	if *doBackup {
		var depthPtr *int
		if depthGiven {
			depthPtr = depth
		}
		stats := backup.RunWithFilter(cwd, depthPtr, dir, sourceFilter)
		fmt.Println(backup.FormatSummary(stats, dir))
		if stats.Projects == 0 {
			fmt.Fprintln(os.Stderr, "no matching project found for this directory")
		}
		return
	}

	if *doSave {
		var cutoff *time.Time
		if *since != "" {
			t, err := time.Parse("2006-01-02", *since)
			if err != nil {
				fail("invalid --since date %q: expected YYYY-MM-DD", *since)
			}
			cutoff = &t
		} else if *days > 0 {
			t := time.Now().AddDate(0, 0, -*days)
			cutoff = &t
		}

		mode, root := source.DetectModeWithFilter(cwd, *all, sourceFilter)
		var projects []model.Project
		if mode == "aggregate" {
			projects = model.LoadProjectsWithFilter(root, sourceFilter)
		} else {
			projects = []model.Project{model.LoadProjectWithFilter(root, sourceFilter)}
		}

		doc := model.BuildSaveDocument(projects, sourceFilter, cutoff)
		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			fmt.Fprintln(os.Stderr, "apl:", err)
			os.Exit(1)
		}

		outPath := *saveFile
		if outPath == "" {
			outPath = "apl-save.json"
		}
		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "apl:", err)
			os.Exit(1)
		}

		promptTotal := 0
		for _, p := range doc.Projects {
			promptTotal += p.PromptCount
		}
		fmt.Printf("apl save: %d project(s), %d prompt(s) -> %s\n", len(doc.Projects), promptTotal, outPath)
		if promptTotal == 0 {
			fmt.Fprintln(os.Stderr, "no matching prompts found for this directory/filter")
		}
		return
	}

	var m tui.Model
	if *viewBackup {
		m = tui.DetectAndNewWithFilter(cwd, false, dir, sourceFilter)
	} else {
		m = tui.DetectAndNewWithFilter(cwd, *all, "", sourceFilter)
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "apl:", err)
		os.Exit(1)
	}
}
