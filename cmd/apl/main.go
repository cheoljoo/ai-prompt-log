// Command apl is a tig-style terminal viewer for Claude Code session logs.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cheoljoo/ai-prompt-log/internal/tui"
)

const gitURL = "https://github.com/cheoljoo/ai-prompt-log"

const keybindingsHelp = `Keybindings (vi-style):
  j/k, up/down     move within the focused pane
  g/G              jump to top / bottom
  Ctrl+F/Ctrl+B    page down / up
  Ctrl+D/Ctrl+U    half page down / up
  l, Tab, Enter    focus next pane (drill in)
  h, Shift+Tab, Esc  focus previous pane (back)
  q                quit
`

func main() {
	all := flag.Bool("all", false, "Browse every project under ~/.claude/projects (3-pane aggregate view)")
	flag.BoolVar(all, "a", false, "shorthand for --all")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "apl (AI Prompt Log viewer) - a tig-style TUI for browsing Claude Code session logs,\n")
		fmt.Fprintf(os.Stderr, "read directly from ~/.claude/projects (no copying).\n\n")
		fmt.Fprintf(os.Stderr, "Plain `apl` shows just the current project's own prompts (2 panes: Prompts | Detail).\n")
		fmt.Fprintf(os.Stderr, "`apl --all` shows every project at once (3 panes: Projects | Prompts | Detail).\n\n")
		fmt.Fprintf(os.Stderr, "Usage: apl [-a|--all]\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\n%s\nSource: %s\n", keybindingsHelp, gitURL)
	}
	flag.Parse()

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "apl:", err)
		os.Exit(1)
	}

	m := tui.DetectAndNew(cwd, *all, "")
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "apl:", err)
		os.Exit(1)
	}
}
