// Package backup copies session jsonl files out of ~/.claude/projects into
// a location independent of any one project's lifetime, so deleting a
// project directory doesn't lose its prompt history.
//
// Mirrors the same <encoded-cwd>/*.jsonl layout as source.ClaudeProjectsDir
// so model.go's existing loading code works against the backup unchanged.
// Ported from poc/apl/backup.py — see that file for the original Python
// reference implementation and its own history (poc/apl/sync.py).
package backup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cheoljoo/ai-prompt-log/internal/model"
	"github.com/cheoljoo/ai-prompt-log/internal/source"
)

// DefaultDir returns ~/ai-prompt-log.backup.
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "ai-prompt-log.backup"
	}
	return filepath.Join(home, "ai-prompt-log.backup")
}

type Stats struct {
	Projects  int
	Copied    int
	Updated   int
	Unchanged int
}

// ResolveDepthRoot walks up `depth` directories from cwd. depth=0 -> cwd itself.
func ResolveDepthRoot(cwd string, depth int) string {
	cur, err := filepath.Abs(cwd)
	if err != nil {
		cur = cwd
	}
	for i := 0; i < depth; i++ {
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return cur
}

func isRootOrDescendant(cwdField, root string) bool {
	p, err := filepath.Abs(cwdField)
	if err != nil {
		return false
	}
	p = filepath.Clean(p)
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}

// FindProjectsByCwdAncestry returns every project dir under aggregateRoot
// whose real (jsonl) cwd field is root itself or a descendant of it.
// Deliberately does not decode the encoded directory name (lossy, see
// docs/data-model.md) -- it reads each project's own recorded cwd.
func FindProjectsByCwdAncestry(root, aggregateRoot string) []string {
	var matches []string
	for _, projDir := range source.ListProjectDirs(aggregateRoot) {
		cwdField := model.FindCwdField(projDir)
		if cwdField != "" && isRootOrDescendant(cwdField, root) {
			matches = append(matches, projDir)
		}
	}
	return matches
}

// SelectProjects decides which ~/.claude/projects subdirectories
// `apl --backup` should copy. depth == nil (no --depth given) means just
// the current project, via the same nearest-ancestor resolution as direct
// mode viewing. depth != nil means every project whose real cwd is at or
// below the directory `*depth` levels above cwd.
func SelectProjects(cwd string, depth *int) []string {
	if depth == nil {
		d := source.FindDirectProjectDir(cwd)
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			return []string{d}
		}
		return nil
	}
	root := ResolveDepthRoot(cwd, *depth)
	return FindProjectsByCwdAncestry(root, source.ClaudeProjectsDir())
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if info, err := os.Stat(src); err == nil {
		_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
	}
	return nil
}

// CopyProject incrementally copies one project's *.jsonl files into
// destRoot/<same-name>/. Never deletes anything from the destination, even
// if it no longer matches the source directory.
func CopyProject(srcDir, destRoot string) (copied, updated, unchanged int) {
	destDir := filepath.Join(destRoot, filepath.Base(srcDir))
	for _, f := range source.ListSessionFiles(srcDir) {
		destF := filepath.Join(destDir, filepath.Base(f))
		srcInfo, err := os.Stat(f)
		if err != nil {
			continue
		}
		if destInfo, err := os.Stat(destF); err == nil {
			if !destInfo.ModTime().Before(srcInfo.ModTime()) && destInfo.Size() == srcInfo.Size() {
				unchanged++
				continue
			}
			if err := copyFile(f, destF); err == nil {
				updated++
			}
			continue
		}
		if err := os.MkdirAll(destDir, 0o755); err != nil {
			continue
		}
		if err := copyFile(f, destF); err == nil {
			copied++
		}
	}
	return
}

// Run performs a full `apl --backup` invocation.
func Run(cwd string, depth *int, backupDir string) Stats {
	_ = os.MkdirAll(backupDir, 0o755)
	projectDirs := SelectProjects(cwd, depth)
	stats := Stats{Projects: len(projectDirs)}
	for _, p := range projectDirs {
		c, u, un := CopyProject(p, backupDir)
		stats.Copied += c
		stats.Updated += u
		stats.Unchanged += un
	}
	return stats
}

func FormatSummary(stats Stats, backupDir string) string {
	return fmt.Sprintf(
		"apl backup: %d project(s), %d copied, %d updated, %d unchanged -> %s",
		stats.Projects, stats.Copied, stats.Updated, stats.Unchanged, backupDir,
	)
}
