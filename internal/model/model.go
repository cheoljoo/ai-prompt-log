// Package model parses JSONL records into Project / Prompt.
//
// Boundary and ordering rules follow docs/data-model.md.
package model

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/cheoljoo/ai-prompt-log/internal/source"
)

var commandRE = regexp.MustCompile(`(?s)<command-name>\s*(.*?)\s*</command-name>`)

// KV is one ordered key/value pair from a JSON object (Go maps don't
// preserve key order, but tool_use arguments should render in the order
// Claude Code emitted them).
type KV struct {
	Key   string
	Value OrderedValue
}

// OrderedValue is a JSON value decoded while preserving object key order at
// every nesting level.
type OrderedValue struct {
	Kind   string // "string" | "number" | "bool" | "null" | "array" | "object"
	Str    string
	Num    json.Number
	Bool   bool
	Array  []OrderedValue
	Object []KV
}

func decodeOrderedValue(dec *json.Decoder) (OrderedValue, error) {
	tok, err := dec.Token()
	if err != nil {
		return OrderedValue{}, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			var obj []KV
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return OrderedValue{}, err
				}
				key, _ := keyTok.(string)
				val, err := decodeOrderedValue(dec)
				if err != nil {
					return OrderedValue{}, err
				}
				obj = append(obj, KV{Key: key, Value: val})
			}
			if _, err := dec.Token(); err != nil { // consume '}'
				return OrderedValue{}, err
			}
			return OrderedValue{Kind: "object", Object: obj}, nil
		case '[':
			var arr []OrderedValue
			for dec.More() {
				val, err := decodeOrderedValue(dec)
				if err != nil {
					return OrderedValue{}, err
				}
				arr = append(arr, val)
			}
			if _, err := dec.Token(); err != nil { // consume ']'
				return OrderedValue{}, err
			}
			return OrderedValue{Kind: "array", Array: arr}, nil
		}
	case string:
		return OrderedValue{Kind: "string", Str: t}, nil
	case json.Number:
		return OrderedValue{Kind: "number", Num: t}, nil
	case bool:
		return OrderedValue{Kind: "bool", Bool: t}, nil
	case nil:
		return OrderedValue{Kind: "null"}, nil
	}
	return OrderedValue{Kind: "null"}, nil
}

// PyRepr renders v the way Python's repr() would for a dict/list value.
func PyRepr(v OrderedValue) string {
	switch v.Kind {
	case "string":
		return "'" + strings.ReplaceAll(v.Str, "'", "\\'") + "'"
	case "number":
		return v.Num.String()
	case "bool":
		if v.Bool {
			return "True"
		}
		return "False"
	case "null":
		return "None"
	case "array":
		parts := make([]string, len(v.Array))
		for i, e := range v.Array {
			parts[i] = PyRepr(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case "object":
		parts := make([]string, len(v.Object))
		for i, kv := range v.Object {
			parts[i] = "'" + kv.Key + "': " + PyRepr(kv.Value)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return ""
}

// FormatTopLevel renders v the way Python's str() would: unquoted for a
// plain string, repr-style for anything else.
func FormatTopLevel(v OrderedValue) string {
	if v.Kind == "string" {
		return v.Str
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

func (p *Prompt) Summary() string {
	if m := commandRE.FindStringSubmatch(p.UserText); m != nil {
		return "[cmd] " + m[1]
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
	InputTokens             int64 `json:"input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens    int64 `json:"cache_read_input_tokens"`
	OutputTokens            int64 `json:"output_tokens"`
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

func iterRecords(path string, yield func(rawRecord)) {
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
		yield(rec)
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
			var kv []KV
			if len(b.Input) > 0 {
				dec := json.NewDecoder(bytes.NewReader(b.Input))
				dec.UseNumber()
				if ov, err := decodeOrderedValue(dec); err == nil && ov.Kind == "object" {
					kv = ov.Object
				}
			}
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

	iterRecords(path, func(rec rawRecord) {
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
	})
	return prompts
}

func sessionStartTimestamp(path string) string {
	ts := ""
	iterRecords(path, func(rec rawRecord) {
		if ts == "" && rec.Timestamp != "" {
			ts = rec.Timestamp
		}
	})
	return ts
}

// BuildPrompts merges every session file under projectDir into one
// time-ordered list (oldest session file first; file line order is
// trusted within a session, see docs/data-model.md).
func BuildPrompts(projectDir string) []Prompt {
	files := source.ListSessionFiles(projectDir)
	sort.Slice(files, func(i, j int) bool {
		return sessionStartTimestamp(files[i]) < sessionStartTimestamp(files[j])
	})
	var prompts []Prompt
	for _, f := range files {
		prompts = append(prompts, ParseSessionFile(f)...)
	}
	return prompts
}

func findCwdField(projectDir string) string {
	for _, f := range source.ListSessionFiles(projectDir) {
		cwd := ""
		iterRecords(f, func(rec rawRecord) {
			if cwd == "" && rec.Cwd != "" {
				cwd = rec.Cwd
			}
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
	cwd := findCwdField(projectDir)
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
