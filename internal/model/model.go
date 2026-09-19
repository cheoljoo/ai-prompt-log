// Package model parses JSONL records into Project / Prompt.
//
// Boundary and ordering rules follow docs/data-model.md.
package model

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	json "github.com/goccy/go-json"

	"github.com/cheoljoo/ai-prompt-log/internal/source"
)

var commandRE = regexp.MustCompile(`(?s)<command-name>\s*(.*?)\s*</command-name>`)

// taskNotificationRE matches the <task-notification> wrapper that background/
// async completion notices (forked subagents incl. /btw, background Bash
// commands, Monitor watches, scheduled wakeups, ...) are delivered in -- its
// <summary> is a human-readable one-liner of what actually happened.
var taskNotificationRE = regexp.MustCompile(`(?s)<task-notification>.*?<summary>\s*(.*?)\s*</summary>`)

// KV is one ordered key/value pair from a JSON object's top level (Go maps
// don't preserve key order, but tool_use arguments should render in the
// order Claude Code emitted them).
//
// Only the top level is order-preserving: we use encoding/json.Decoder's
// Token() exactly once per key to learn the key order, then decode each
// value in one shot with the standard (fast) Unmarshal path. An earlier
// version called Token() recursively at every nesting level, which was ~5x
// slower than the Python implementation on real data -- Token() re-validates
// the whole remaining stream on every call, which is fine for a handful of
// top-level keys but very expensive once applied to every nested list/dict
// inside a large tool argument (see agents/D-go-release.md). Nested object
// keys below the top level fall back to Go's normal (unordered, alphabetized
// for determinism in PyRepr) map handling -- a deliberate, minor fidelity
// tradeoff for a large, real performance win.
type KV struct {
	Key   string
	Value interface{}
}

// scanTopLevelKeyOrder returns a top-level JSON object's key order by
// scanning raw bytes directly, with no encoding/json involved. Not a
// general JSON validator -- raw is assumed already-valid JSON, which
// decodeOrderedTopLevel separately confirms via json.Unmarshal.
//
// This exists because encoding/json's streaming Decoder (Token()/Decode())
// re-validates the whole remaining buffer on every single call, which is
// fine for a few dozen calls but ~5x slower than Python's json.loads() once
// applied per key across every tool_use argument in a real session log (see
// agents/D-go-release.md). A plain byte scan just to learn key order, paired
// with json.Unmarshal's fast non-streaming path for the actual values,
// avoids that entirely.
func scanTopLevelKeyOrder(raw []byte) []string {
	var keys []string
	depth := 0
	expectKey := false
	for i := 0; i < len(raw); i++ {
		switch c := raw[i]; c {
		case '{', '[':
			depth++
			if c == '{' && depth == 1 {
				expectKey = true
			}
		case '}', ']':
			depth--
		case ',':
			if depth == 1 {
				expectKey = true
			}
		case '"':
			start := i
			i++
			for i < len(raw) && raw[i] != '"' {
				if raw[i] == '\\' {
					i++
				}
				i++
			}
			if i < len(raw) {
				i++ // include closing quote
			}
			if depth == 1 && expectKey {
				var key string
				if err := json.Unmarshal(raw[start:i], &key); err == nil {
					keys = append(keys, key)
				}
				expectKey = false
			}
			i-- // outer loop will i++
		}
	}
	return keys
}

// decodeOrderedTopLevel decodes a JSON object, preserving the order of its
// top-level keys.
func decodeOrderedTopLevel(raw json.RawMessage) []KV {
	if len(raw) == 0 {
		return nil
	}
	var byKey map[string]json.RawMessage
	if err := json.Unmarshal(raw, &byKey); err != nil {
		return nil
	}
	keys := scanTopLevelKeyOrder(raw)
	kv := make([]KV, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, k := range keys {
		if seen[k] {
			continue // duplicate key: keep first occurrence, like Python dict literals
		}
		seen[k] = true
		rawVal, ok := byKey[k]
		if !ok {
			continue
		}
		var val interface{}
		// A fresh Decoder scoped to just this one (typically small) value
		// keeps numbers as json.Number (exact source formatting, matching
		// Python's repr more closely than float64 would) without the
		// pathological slowdown of Decode() on a long-lived shared stream.
		dec := json.NewDecoder(bytes.NewReader(rawVal))
		dec.UseNumber()
		if err := dec.Decode(&val); err != nil {
			continue
		}
		kv = append(kv, KV{Key: k, Value: val})
	}
	return kv
}

// PyRepr renders v (a value produced by encoding/json's standard decoder:
// string, json.Number, bool, nil, []interface{}, or map[string]interface{})
// the way Python's repr() would for a dict/list value.
func PyRepr(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case string:
		return "'" + strings.ReplaceAll(t, "'", "\\'") + "'"
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "True"
		}
		return "False"
	case []interface{}:
		parts := make([]string, len(t))
		for i, e := range t {
			parts[i] = PyRepr(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case map[string]interface{}:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = "'" + k + "': " + PyRepr(t[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		return fmt.Sprintf("%v", t)
	}
}

// FormatTopLevel renders v the way Python's str() would: unquoted for a
// plain string, repr-style for anything else.
func FormatTopLevel(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return PyRepr(v)
}

// AssistantBlock is one rendered piece of an assistant turn.
type AssistantBlock struct {
	Kind      string // "text" | "tool_use"
	Text      string
	ToolName  string
	ToolInput []KV
}

// Prompt is one user turn plus the assistant blocks that follow it, up to
// the next user turn.
type Prompt struct {
	SessionID   string
	Timestamp   string
	Branch      string
	Sidechain   bool
	UserText    string
	Blocks      []AssistantBlock
	TotalTokens int64
}

func (p *Prompt) IsCommand() bool {
	return commandRE.MatchString(p.UserText)
}

func (p *Prompt) IsTaskNotification() bool {
	return taskNotificationRE.MatchString(p.UserText)
}

func (p *Prompt) Summary() string {
	if m := commandRE.FindStringSubmatch(p.UserText); m != nil {
		return "[cmd] " + m[1]
	}
	if m := taskNotificationRE.FindStringSubmatch(p.UserText); m != nil {
		text := m[1]
		runes := []rune(text)
		if len(runes) > 100 {
			text = string(runes[:97]) + "..."
		}
		return "[bg] " + text
	}
	firstLine := ""
	if p.UserText != "" {
		lines := strings.SplitN(strings.TrimSpace(p.UserText), "\n", 2)
		firstLine = strings.TrimSpace(lines[0])
	}
	runes := []rune(firstLine)
	if len(runes) > 100 {
		firstLine = string(runes[:97]) + "..."
	}
	if firstLine == "" {
		return "(empty)"
	}
	return firstLine
}

// Project is one directory under ~/.claude/projects (or a backup mirroring
// that layout).
type Project struct {
	DirPath      string
	DisplayName  string
	Cwd          string
	LastActivity string
	PromptCount  int
}

func (p *Project) LoadPrompts() []Prompt {
	return BuildPrompts(p.DirPath)
}

type rawMessage struct {
	Content json.RawMessage `json:"content"`
	Usage   *rawUsage       `json:"usage"`
}

type rawUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
}

type rawRecord struct {
	Type        string      `json:"type"`
	IsMeta      bool        `json:"isMeta"`
	IsSidechain bool        `json:"isSidechain"`
	Timestamp   string      `json:"timestamp"`
	SessionID   string      `json:"sessionId"`
	GitBranch   string      `json:"gitBranch"`
	Cwd         string      `json:"cwd"`
	Message     *rawMessage `json:"message"`
}

type rawBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// iterRecords calls yield for each record in path, in file order. yield
// returns false to stop early (mirroring Python's generator-based
// _iter_records, where a `for ... return` inside the loop closes the file
// and stops reading immediately) -- without this, callers that only need
// e.g. the first timestamp would otherwise scan an entire multi-megabyte
// session file for nothing.
func iterRecords(path string, yield func(rawRecord) bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec rawRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if !yield(rec) {
			return
		}
	}
}

func contentAsString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

func isPromptBoundary(rec rawRecord) (string, bool) {
	if rec.Type != "user" || rec.IsMeta || rec.Message == nil {
		return "", false
	}
	return contentAsString(rec.Message.Content)
}

func extractAssistantBlocks(rec rawRecord) []AssistantBlock {
	if rec.Message == nil {
		return nil
	}
	var blocks []rawBlock
	if err := json.Unmarshal(rec.Message.Content, &blocks); err != nil {
		return nil
	}
	var out []AssistantBlock
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				out = append(out, AssistantBlock{Kind: "text", Text: b.Text})
			}
		case "tool_use":
			kv := decodeOrderedTopLevel(b.Input)
			out = append(out, AssistantBlock{Kind: "tool_use", ToolName: b.Name, ToolInput: kv})
		}
	}
	return out
}

func usageTokens(rec rawRecord) int64 {
	if rec.Message == nil || rec.Message.Usage == nil {
		return 0
	}
	u := rec.Message.Usage
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens + u.OutputTokens
}

// ParseSessionFile returns the Prompts found in a single session jsonl, in
// file order.
func ParseSessionFile(path string) []Prompt {
	var prompts []Prompt
	var current *Prompt
	sessionID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	iterRecords(path, func(rec rawRecord) bool {
		if userText, ok := isPromptBoundary(rec); ok {
			sid := rec.SessionID
			if sid == "" {
				sid = sessionID
			}
			prompts = append(prompts, Prompt{
				SessionID: sid,
				Timestamp: rec.Timestamp,
				Branch:    rec.GitBranch,
				Sidechain: rec.IsSidechain,
				UserText:  userText,
			})
			current = &prompts[len(prompts)-1]
		} else if rec.Type == "assistant" && current != nil {
			current.Blocks = append(current.Blocks, extractAssistantBlocks(rec)...)
			current.TotalTokens += usageTokens(rec)
		}
		return true
	})
	return prompts
}

func sessionStartTimestamp(path string) string {
	ts := ""
	iterRecords(path, func(rec rawRecord) bool {
		if rec.Timestamp != "" {
			ts = rec.Timestamp
			return false
		}
		return true
	})
	return ts
}

// BuildPrompts merges every session file under projectDir into one
// time-ordered list (oldest session file first; file line order is
// trusted within a session, see docs/data-model.md).
func BuildPrompts(projectDir string) []Prompt {
	files := source.ListSessionFiles(projectDir)
	// Compute each file's start timestamp once up front -- sort.Slice's
	// comparator can invoke itself far more than len(files) times during an
	// O(n log n) sort, and recomputing sessionStartTimestamp (a file read)
	// inside it turns one file scan into many.
	timestamps := make([]string, len(files))
	for i, f := range files {
		timestamps[i] = sessionStartTimestamp(f)
	}
	order := make([]int, len(files))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		return timestamps[order[i]] < timestamps[order[j]]
	})
	var prompts []Prompt
	for _, idx := range order {
		prompts = append(prompts, ParseSessionFile(files[idx])...)
	}
	return prompts
}

func FindCwdField(projectDir string) string {
	for _, f := range source.ListSessionFiles(projectDir) {
		cwd := ""
		iterRecords(f, func(rec rawRecord) bool {
			if rec.Cwd != "" {
				cwd = rec.Cwd
				return false
			}
			return true
		})
		if cwd != "" {
			return cwd
		}
	}
	return ""
}

// LoadProject builds a Project summary (display name, last activity, prompt
// count) for projectDir without keeping the full prompt list in memory.
func LoadProject(projectDir string) Project {
	cwd := FindCwdField(projectDir)
	displayName := filepath.Base(projectDir)
	if cwd != "" {
		displayName = filepath.Base(cwd)
	}
	files := source.ListSessionFiles(projectDir)
	lastActivity := ""
	for _, f := range files {
		if ts := sessionStartTimestamp(f); ts > lastActivity {
			lastActivity = ts
		}
	}
	return Project{
		DirPath:      projectDir,
		DisplayName:  displayName,
		Cwd:          cwd,
		LastActivity: lastActivity,
		PromptCount:  len(BuildPrompts(projectDir)),
	}
}

// LoadProjects returns one Project per subdirectory of aggregateRoot.
func LoadProjects(aggregateRoot string) []Project {
	dirs := source.ListProjectDirs(aggregateRoot)
	out := make([]Project, 0, len(dirs))
	for _, d := range dirs {
		out = append(out, LoadProject(d))
	}
	return out
}
