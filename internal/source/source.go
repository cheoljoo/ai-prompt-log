// Package source locates Claude Code, Antigravity CLI (agy), and Gemini CLI session files.
//
// Reference: docs/data-model.md
package source

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
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

// AgyAppDataDir returns the root data directory for Antigravity CLI.
func AgyAppDataDir() string {
	if v := os.Getenv("ANTIGRAVITY_APP_DATA_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".gemini", "antigravity-cli")
}

// GeminiTmpDir returns ~/.gemini/tmp for legacy Gemini CLI sessions.
func GeminiTmpDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".gemini", "tmp")
}

// EncodePath mirrors Claude Code's lossy cwd -> directory name encoding.
func EncodePath(path string) string {
	return nonAlnum.ReplaceAllString(path, "-")
}

// AgyConvInfo holds metadata for one Antigravity CLI conversation.
type AgyConvInfo struct {
	ID             string
	Workspace      string
	Branch         string
	Title          string
	TranscriptPath string
}

func parseProtobufMeta(data []byte) (workspace, branch string) {
	idx := 0
	readVarint := func() uint64 {
		var val uint64
		var shift uint
		for idx < len(data) {
			b := data[idx]
			idx++
			val |= uint64(b&0x7f) << shift
			if (b & 0x80) == 0 {
				break
			}
			shift += 7
		}
		return val
	}

	for idx < len(data) {
		tag := readVarint()
		wire := tag & 7
		fnum := tag >> 3

		if wire == 2 {
			length := int(readVarint())
			if length < 0 || idx+length > len(data) {
				break
			}
			val := data[idx : idx+length]
			idx += length

			if fnum == 7 && workspace == "" {
				s := string(val)
				if strings.HasPrefix(s, "file://") {
					workspace = s[7:]
				}
			} else if fnum == 1 {
				// Submessage
				sidx := 0
				sreadVarint := func() uint64 {
					var sval uint64
					var sshift uint
					for sidx < len(val) {
						b := val[sidx]
						sidx++
						sval |= uint64(b&0x7f) << sshift
						if (b & 0x80) == 0 {
							break
						}
						sshift += 7
					}
					return sval
				}
				for sidx < len(val) {
					stag := sreadVarint()
					swire := stag & 7
					sfnum := stag >> 3
					if swire == 2 {
						slen := int(sreadVarint())
						if slen < 0 || sidx+slen > len(val) {
							break
						}
						sval := val[sidx : sidx+slen]
						sidx += slen
						if sfnum == 1 && workspace == "" {
							s := string(sval)
							if strings.HasPrefix(s, "file://") {
								workspace = s[7:]
							}
						} else if sfnum == 4 && branch == "" {
							branch = string(sval)
						}
					} else if swire == 0 {
						sreadVarint()
					} else {
						break
					}
				}
			}
		} else if wire == 0 {
			readVarint()
		} else {
			break
		}
	}
	return
}

// GetAgyConvInfo extracts workspace directory, git branch, and title for an AGY conversation.
func GetAgyConvInfo(convID string, baseDir string) AgyConvInfo {
	if baseDir == "" {
		baseDir = AgyAppDataDir()
	}
	info := AgyConvInfo{ID: convID}

	// 1. Try conversation_summaries.db
	summariesDB := filepath.Join(baseDir, "conversation_summaries.db")
	if fi, err := os.Stat(summariesDB); err == nil && !fi.IsDir() {
		if db, err := sql.Open("sqlite", summariesDB); err == nil {
			var preview, workspaceUris string
			err = db.QueryRow(
				"SELECT preview, workspace_uris FROM conversation_summaries WHERE conversation_id=?",
				convID,
			).Scan(&preview, &workspaceUris)
			if err == nil {
				info.Title = preview
				var uris []string
				if json.Unmarshal([]byte(workspaceUris), &uris) == nil && len(uris) > 0 {
					if strings.HasPrefix(uris[0], "file://") {
						info.Workspace = uris[0][7:]
					}
				}
			}
			db.Close()
		}
	}

	// 2. Try conversations/<convID>.db (protobuf trajectory_metadata_blob)
	convDB := filepath.Join(baseDir, "conversations", convID+".db")
	if fi, err := os.Stat(convDB); err == nil && !fi.IsDir() {
		if db, err := sql.Open("sqlite", convDB); err == nil {
			var blob []byte
			err = db.QueryRow("SELECT data FROM trajectory_metadata_blob WHERE id='main'").Scan(&blob)
			if err == nil && len(blob) > 0 {
				ws, br := parseProtobufMeta(blob)
				if info.Workspace == "" && ws != "" {
					info.Workspace = ws
				}
				if info.Branch == "" && br != "" {
					info.Branch = br
				}
			}
			db.Close()
		}
	}

	return info
}

// ScanAgyConversations returns all AGY conversations with their workspace, branch, and transcript file.
func ScanAgyConversations(baseDir string) []AgyConvInfo {
	if baseDir == "" {
		baseDir = AgyAppDataDir()
	}
	var convs []AgyConvInfo
	brainDir := filepath.Join(baseDir, "brain")
	entries, err := os.ReadDir(brainDir)
	if err != nil {
		return convs
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		convID := e.Name()
		convDir := filepath.Join(brainDir, convID)
		transcript := filepath.Join(convDir, ".system_generated", "logs", "transcript.jsonl")
		if _, err := os.Stat(transcript); err != nil {
			transcript = filepath.Join(convDir, ".system_generated", "logs", "transcript_full.jsonl")
		}
		if _, err := os.Stat(transcript); err == nil {
			info := GetAgyConvInfo(convID, baseDir)
			info.TranscriptPath = transcript
			convs = append(convs, info)
		}
	}
	return convs
}

// GeminiProjectInfo holds metadata for a legacy Gemini CLI tmp project.
type GeminiProjectInfo struct {
	DirName   string
	Workspace string
	ChatFiles []string
}

// ScanGeminiTmpProjects scans legacy Gemini CLI project directories in ~/.gemini/tmp.
func ScanGeminiTmpProjects(baseDir string) []GeminiProjectInfo {
	if baseDir == "" {
		baseDir = GeminiTmpDir()
	}
	var projects []GeminiProjectInfo
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return projects
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pdir := filepath.Join(baseDir, e.Name())
		rootF := filepath.Join(pdir, ".project_root")
		ws := ""
		if data, err := os.ReadFile(rootF); err == nil {
			ws = strings.TrimSpace(string(data))
		}
		chatsDir := filepath.Join(pdir, "chats")
		chatEntries, err := os.ReadDir(chatsDir)
		if err == nil {
			var chatFiles []string
			for _, ce := range chatEntries {
				if ce.IsDir() {
					continue
				}
				ext := filepath.Ext(ce.Name())
				if ext == ".json" || ext == ".jsonl" {
					chatFiles = append(chatFiles, filepath.Join(chatsDir, ce.Name()))
				}
			}
			sort.Strings(chatFiles)
			if len(chatFiles) > 0 {
				projects = append(projects, GeminiProjectInfo{
					DirName:   e.Name(),
					Workspace: ws,
					ChatFiles: chatFiles,
				})
			}
		}
	}
	return projects
}

// DirectProjectInfo holds all discovered session files across sources for a direct project.
type DirectProjectInfo struct {
	Cwd         string
	DisplayName string
	Path        string
	ClaudeFiles []string
	AgyConvs    []AgyConvInfo
	GeminiFiles []string
}

// FindDirectProjectInfo walks up from cwd to find the nearest ancestor with recorded sessions.
func FindDirectProjectInfo(cwd string, sourceFilter string) DirectProjectInfo {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	cur := filepath.Clean(abs)

	includeClaude := sourceFilter == "all" || sourceFilter == "claude"
	includeAgy := sourceFilter == "all" || sourceFilter == "agy" || sourceFilter == "gemini"

	var allAgy []AgyConvInfo
	var allGemini []GeminiProjectInfo
	if includeAgy {
		allAgy = ScanAgyConversations("")
		allGemini = ScanGeminiTmpProjects("")
	}

	for {
		var claudeFiles []string
		if includeClaude {
			d := filepath.Join(ClaudeProjectsDir(), EncodePath(cur))
			claudeFiles = ListSessionFiles(d)
		}

		var agyConvs []AgyConvInfo
		var geminiFiles []string
		if includeAgy {
			for _, c := range allAgy {
				if c.Workspace != "" {
					wsClean := filepath.Clean(c.Workspace)
					if wsClean == cur {
						agyConvs = append(agyConvs, c)
					}
				}
			}
			for _, g := range allGemini {
				if g.Workspace != "" {
					wsClean := filepath.Clean(g.Workspace)
					if wsClean == cur {
						geminiFiles = append(geminiFiles, g.ChatFiles...)
					}
				}
			}
		}

		if len(claudeFiles) > 0 || len(agyConvs) > 0 || len(geminiFiles) > 0 {
			return DirectProjectInfo{
				Cwd:         cur,
				DisplayName: filepath.Base(cur),
				Path:        cur,
				ClaudeFiles: claudeFiles,
				AgyConvs:    agyConvs,
				GeminiFiles: geminiFiles,
			}
		}

		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}

	// Fallback to abs
	var fallbackClaude []string
	if includeClaude {
		d := filepath.Join(ClaudeProjectsDir(), EncodePath(abs))
		fallbackClaude = ListSessionFiles(d)
	}
	return DirectProjectInfo{
		Cwd:         abs,
		DisplayName: filepath.Base(abs),
		Path:        abs,
		ClaudeFiles: fallbackClaude,
	}
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

// DetectModeWithFilter returns (mode, projectRootDir) respecting the source filter.
func DetectModeWithFilter(cwd string, aggregate bool, sourceFilter string) (string, string) {
	if aggregate {
		if sourceFilter == "claude" {
			return "aggregate", ClaudeProjectsDir()
		}
		home, _ := os.UserHomeDir()
		return "aggregate", home
	}
	info := FindDirectProjectInfo(cwd, sourceFilter)
	return "direct", info.Path
}

// DetectMode returns (mode, projectRootDir).
//
// mode == "aggregate": projectRootDir holds one subdir per project
// (~/.claude/projects itself) -> 3-level view, read directly, no copy.
// mode == "direct": projectRootDir is the nearest ancestor's session
// directory -> 2-level view.
func DetectMode(cwd string, aggregate bool) (string, string) {
	return DetectModeWithFilter(cwd, aggregate, "all")
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
