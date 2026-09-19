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

// FindProjectsByCwdAncestry returns every project whose real cwd is root itself or a descendant of it.
func FindProjectsByCwdAncestry(root, sourceFilter string) []model.Project {
	var matches []model.Project
	for _, proj := range model.LoadProjectsWithFilter("", sourceFilter) {
		if proj.Cwd != "" && isRootOrDescendant(proj.Cwd, root) {
			matches = append(matches, proj)
		}
	}
	return matches
}

// SelectProjectsWithFilter decides which projects apl --backup should copy.
func SelectProjectsWithFilter(cwd string, depth *int, sourceFilter string) []model.Project {
	if depth == nil {
		p := model.LoadProjectWithFilter(cwd, sourceFilter)
		if len(p.GetSessionFiles()) > 0 {
			return []model.Project{p}
		}
		return nil
	}
	root := ResolveDepthRoot(cwd, *depth)
	return FindProjectsByCwdAncestry(root, sourceFilter)
}

// SelectProjects decides which projects apl --backup should copy (defaults to all sources).
func SelectProjects(cwd string, depth *int) []model.Project {
	return SelectProjectsWithFilter(cwd, depth, "all")
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

// CopyProject incrementally copies one project's session files into destRoot/<encoded-cwd>/.
func CopyProject(proj model.Project, destRoot string) (copied, updated, unchanged int) {
	cwdStr := proj.Cwd
	if cwdStr == "" {
		cwdStr = proj.DirPath
	}
	destDir := filepath.Join(destRoot, source.EncodePath(cwdStr))

	for _, f := range proj.GetSessionFiles() {
		fmtType := model.DetectFileFormat(f)
		stem := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))

		if fmtType == "agy" {
			convID := stem
			if meta, ok := proj.ConvMetadata[stem]; ok && meta["id"] != "" {
				convID = meta["id"]
			} else {
				dir := filepath.Dir(f)
				if filepath.Base(dir) == "logs" {
					pdir := filepath.Dir(dir)
					if filepath.Base(pdir) == ".system_generated" {
						convID = filepath.Base(filepath.Dir(pdir))
					}
				}
			}
			if strings.HasPrefix(convID, "agy-") {
				convID = convID[4:]
			}
			destF := filepath.Join(destDir, fmt.Sprintf("agy-%s.jsonl", convID))
			srcInfo, err := os.Stat(f)
			if err != nil {
				continue
			}
			destInfo, destErr := os.Stat(destF)
			existed := destErr == nil
			if existed && !destInfo.ModTime().Before(srcInfo.ModTime()) {
				unchanged++
				continue
			}
			if err := os.MkdirAll(destDir, 0o755); err != nil {
				continue
			}

			branch := ""
			if meta, ok := proj.ConvMetadata[convID]; ok && meta["branch"] != "" {
				branch = meta["branch"]
			} else if meta, ok := proj.ConvMetadata[stem]; ok && meta["branch"] != "" {
				branch = meta["branch"]
			}
			metaHeader := fmt.Sprintf(
				`{"type":"agy_metadata","cwd":%q,"sessionId":%q,"gitBranch":%q}`+"\n",
				proj.Cwd, convID, branch,
			)

			in, err := os.Open(f)
			if err != nil {
				continue
			}
			out, err := os.Create(destF)
			if err != nil {
				in.Close()
				continue
			}
			_, _ = out.WriteString(metaHeader)
			_, _ = io.Copy(out, in)
			in.Close()
			out.Close()
			_ = os.Chtimes(destF, srcInfo.ModTime(), srcInfo.ModTime())

			if existed {
				updated++
			} else {
				copied++
			}
		} else if fmtType == "gemini_json" || fmtType == "gemini_jsonl" {
			destF := filepath.Join(destDir, fmt.Sprintf("gemini-%s.jsonl", stem))
			srcInfo, err := os.Stat(f)
			if err != nil {
				continue
			}
			destInfo, destErr := os.Stat(destF)
			existed := destErr == nil
			if existed && !destInfo.ModTime().Before(srcInfo.ModTime()) {
				unchanged++
				continue
			}
			if err := os.MkdirAll(destDir, 0o755); err != nil {
				continue
			}

			metaHeader := fmt.Sprintf(
				`{"type":"gemini_metadata","cwd":%q,"sessionId":%q}`+"\n",
				proj.Cwd, stem,
			)

			in, err := os.Open(f)
			if err != nil {
				continue
			}
			out, err := os.Create(destF)
			if err != nil {
				in.Close()
				continue
			}
			_, _ = out.WriteString(metaHeader)
			_, _ = io.Copy(out, in)
			in.Close()
			out.Close()
			_ = os.Chtimes(destF, srcInfo.ModTime(), srcInfo.ModTime())

			if existed {
				updated++
			} else {
				copied++
			}
		} else {
			// Standard Claude session file
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
	}
	return
}

// CopyProjectDir is a convenience wrapper for copying a project by directory path.
func CopyProjectDir(srcDir, destRoot string) (copied, updated, unchanged int) {
	p := model.LoadProject(srcDir)
	return CopyProject(p, destRoot)
}

// RunWithFilter performs a full apl --backup invocation respecting the source filter.
func RunWithFilter(cwd string, depth *int, backupDir, sourceFilter string) Stats {
	_ = os.MkdirAll(backupDir, 0o755)
	projects := SelectProjectsWithFilter(cwd, depth, sourceFilter)
	stats := Stats{Projects: len(projects)}
	for _, p := range projects {
		c, u, un := CopyProject(p, backupDir)
		stats.Copied += c
		stats.Updated += u
		stats.Unchanged += un
	}
	return stats
}

// Run performs a full apl --backup invocation across all sources.
func Run(cwd string, depth *int, backupDir string) Stats {
	return RunWithFilter(cwd, depth, backupDir, "all")
}

func FormatSummary(stats Stats, backupDir string) string {
	return fmt.Sprintf(
		"apl backup: %d project(s), %d copied, %d updated, %d unchanged -> %s",
		stats.Projects, stats.Copied, stats.Updated, stats.Unchanged, backupDir,
	)
}
