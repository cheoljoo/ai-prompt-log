// Package source locates Claude Code session JSONL files.
//
// Reference: docs/data-model.md
package source

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

var nonAlnum = regexp.MustCompile(`[^A-Za-z0-9]`)

// ClaudeProjectsDir returns ~/.claude/projects.
func ClaudeProjectsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// EncodePath mirrors Claude Code's lossy cwd -> directory name encoding.
func EncodePath(path string) string {
	return nonAlnum.ReplaceAllString(path, "-")
}

// FindDirectProjectDir walks up from cwd to the nearest ancestor with
// recorded sessions. Claude Code logs sessions under the exact directory it
// was launched from - usually a project's root - not every subdirectory you
// `cd` into afterwards.
func FindDirectProjectDir(cwd string) string {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	root := ClaudeProjectsDir()
	cur := abs
	for {
		candidate := filepath.Join(root, EncodePath(cur))
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return filepath.Join(root, EncodePath(abs))
}

// DetectMode returns (mode, projectRootDir).
//
// mode == "aggregate": projectRootDir holds one subdir per project
// (~/.claude/projects itself) -> 3-level view, read directly, no copy.
// mode == "direct": projectRootDir is the nearest ancestor's session
// directory -> 2-level view.
func DetectMode(cwd string, aggregate bool) (string, string) {
	if aggregate {
		return "aggregate", ClaudeProjectsDir()
	}
	return "direct", FindDirectProjectDir(cwd)
}

// ListSessionFiles returns *.jsonl files under projectDir, sorted by name.
func ListSessionFiles(projectDir string) []string {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".jsonl" {
			out = append(out, filepath.Join(projectDir, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// ListProjectDirs returns immediate subdirectories of aggregateRoot, sorted.
func ListProjectDirs(aggregateRoot string) []string {
	entries, err := os.ReadDir(aggregateRoot)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(aggregateRoot, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}
