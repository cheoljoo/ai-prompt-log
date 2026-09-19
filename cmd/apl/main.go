// Command apl is a tig-style terminal viewer for Claude Code session logs.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cheoljoo/ai-prompt-log/internal/backup"
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
	sourceFlag := flag.String("source", "all", "AI assistant log source to display (all, claude, agy, gemini)")
	flag.StringVar(sourceFlag, "s", "all", "shorthand for --source")
	onlyAgy := flag.Bool("agy", false, "Display only Antigravity CLI (agy) logs")
	onlyClaude := flag.Bool("claude", false, "Display only Claude Code logs")
	onlyGemini := flag.Bool("gemini", false, "Display only Antigravity / Gemini CLI logs (alias for --agy)")
	showVersion := flag.Bool("version", false, "Print the apl version and exit")
	flag.BoolVar(showVersion, "v", false, "shorthand for --version")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "apl (AI Prompt Log viewer) - a tig-style TUI for browsing Claude Code and Antigravity CLI (agy / gemini) session logs,\n")
		fmt.Fprintf(os.Stderr, "read directly from ~/.claude/projects and ~/.gemini/antigravity-cli (no copying).\n\n")
		fmt.Fprintf(os.Stderr, "Plain `apl` shows just the current project's own prompts (2 panes: Prompts | Detail).\n")
		fmt.Fprintf(os.Stderr, "`apl --all` shows every project at once (3 panes: Projects | Prompts | Detail).\n\n")
		fmt.Fprintf(os.Stderr, "`apl --backup` copies session logs into a durable backup directory (outside\n")
		fmt.Fprintf(os.Stderr, "live directories, so it survives a project directory being deleted).\n")
		fmt.Fprintf(os.Stderr, "`apl --view-backup` browses that backup with the same 3-pane view.\n\n")
		fmt.Fprintf(os.Stderr, "Usage: apl [-a|--all | --backup | --view-backup] [--depth N] [--backup-dir PATH] [-s|--source {all,claude,agy,gemini} | --agy | --claude | --gemini]\n\n")
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
	}
	switch sourceFilter {
	case "all", "claude", "agy", "gemini":
	default:
		fail("invalid source %q: choose from all, claude, agy, gemini", sourceFilter)
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
	if depthGiven && !*doBackup {
		fail("--depth only makes sense with --backup")
	}
	if *backupDir != "" && !(*doBackup || *viewBackup) {
		fail("--backup-dir only makes sense with --backup or --view-backup")
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
