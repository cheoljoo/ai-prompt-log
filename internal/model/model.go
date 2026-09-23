// Package model parses JSONL records into Project / Prompt.
//
// Boundary and ordering rules follow docs/data-model.md.
package model

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	json "github.com/goccy/go-json"

	"github.com/cheoljoo/ai-prompt-log/internal/source"
)

var commandRE = regexp.MustCompile(`(?s)<command-name>\s*(.*?)\s*</command-name>`)

// taskNotificationRE matches the <task-notification> wrapper that background/
// async completion notices (forked subagents incl. /btw, background Bash
// commands, Monitor watches, scheduled wakeups, ...) are delivered in -- its
// <summary> is a human-readable one-liner of what actually happened.
var taskNotificationRE = regexp.MustCompile(`(?s)<task-notification>.*?<summary>\s*(.*?)\s*</summary>`)

var slashCmdRE = regexp.MustCompile(`^/[a-zA-Z0-9_-]+`)
var userRequestRE = regexp.MustCompile(`(?s)<USER_REQUEST>\s*(.*?)\s*</USER_REQUEST>`)
var additionalMetaRE = regexp.MustCompile(`(?s)<ADDITIONAL_METADATA>.*?</ADDITIONAL_METADATA>`)
var userSettingsRE = regexp.MustCompile(`(?s)<USER_SETTINGS_CHANGE>.*?</USER_SETTINGS_CHANGE>`)

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
	Source      string // "claude" | "agy" | "gemini" | "opencode"
}

// FileChange represents one created, modified, or deleted file derived from tool calls.
type FileChange struct {
	Path   string
	Action string // "created" | "modified" | "deleted"
}

func (p *Prompt) FileChanges() []FileChange {
	return ExtractFileChanges(p)
}

var bashStmtSepRE = regexp.MustCompile(`&&|\|\||;|\n`)
var (
	rmCmdRE    = regexp.MustCompile(`^(?:rm|rmdir)\b(.*)$`)
	touchCmdRE = regexp.MustCompile(`^touch\b(.*)$`)
	mkdirCmdRE = regexp.MustCompile(`^mkdir\b(.*)$`)
	mvCmdRE    = regexp.MustCompile(`^mv\b(.*)$`)
	cpCmdRE    = regexp.MustCompile(`^cp\b(.*)$`)
)

func parseShellArgs(raw string) []string {
	var args []string
	inQuote := rune(0)
	var cur strings.Builder
	for _, r := range raw {
		switch {
		case inQuote != 0:
			if r == inQuote {
				inQuote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			inQuote = r
		case r == ' ' || r == '\t':
			if cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
}

func bashFileOps(command string) []FileChange {
	if command == "" {
		return nil
	}
	var ops []FileChange
	stmts := bashStmtSepRE.Split(command, -1)
	for _, stmt := range stmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if m := rmCmdRE.FindStringSubmatch(stmt); m != nil {
			for _, a := range parseShellArgs(m[1]) {
				if !strings.HasPrefix(a, "-") {
					ops = append(ops, FileChange{Path: a, Action: "deleted"})
				}
			}
			continue
		}
		if m := touchCmdRE.FindStringSubmatch(stmt); m != nil {
			for _, a := range parseShellArgs(m[1]) {
				if !strings.HasPrefix(a, "-") {
					ops = append(ops, FileChange{Path: a, Action: "created"})
				}
			}
			continue
		}
		if m := mkdirCmdRE.FindStringSubmatch(stmt); m != nil {
			for _, a := range parseShellArgs(m[1]) {
				if !strings.HasPrefix(a, "-") {
					ops = append(ops, FileChange{Path: a, Action: "created"})
				}
			}
			continue
		}
		if m := mvCmdRE.FindStringSubmatch(stmt); m != nil {
			var nonFlags []string
			for _, a := range parseShellArgs(m[1]) {
				if !strings.HasPrefix(a, "-") {
					nonFlags = append(nonFlags, a)
				}
			}
			if len(nonFlags) >= 2 {
				ops = append(ops, FileChange{Path: nonFlags[0], Action: "deleted"})
				ops = append(ops, FileChange{Path: nonFlags[len(nonFlags)-1], Action: "created"})
			}
			continue
		}
		if m := cpCmdRE.FindStringSubmatch(stmt); m != nil {
			var nonFlags []string
			for _, a := range parseShellArgs(m[1]) {
				if !strings.HasPrefix(a, "-") {
					nonFlags = append(nonFlags, a)
				}
			}
			if len(nonFlags) >= 2 {
				ops = append(ops, FileChange{Path: nonFlags[len(nonFlags)-1], Action: "created"})
			}
			continue
		}
	}
	return ops
}

func getToolInputString(kvs []KV, key string) string {
	for _, kv := range kvs {
		if kv.Key == key {
			if s, ok := kv.Value.(string); ok {
				return s
			}
		}
	}
	return ""
}

func hasToolInputKey(kvs []KV, key string) bool {
	for _, kv := range kvs {
		if kv.Key == key {
			if s, ok := kv.Value.(string); ok {
				return s != ""
			}
			return kv.Value != nil
		}
	}
	return false
}

// ExtractFileChanges returns a best-effort list of (path, action) file changes
// derived from tool calls across all sources.
func ExtractFileChanges(p *Prompt) []FileChange {
	if p == nil {
		return nil
	}
	var raw []FileChange
	for _, block := range p.Blocks {
		if block.Kind != "tool_use" {
			continue
		}
		name := block.ToolName
		switch name {
		case "Write":
			if path := getToolInputString(block.ToolInput, "file_path"); path != "" {
				raw = append(raw, FileChange{Path: path, Action: "created"})
			}
		case "Edit", "MultiEdit":
			if path := getToolInputString(block.ToolInput, "file_path"); path != "" {
				raw = append(raw, FileChange{Path: path, Action: "modified"})
			}
		case "NotebookEdit":
			if path := getToolInputString(block.ToolInput, "notebook_path"); path != "" {
				raw = append(raw, FileChange{Path: path, Action: "modified"})
			}
		case "write_to_file":
			if path := getToolInputString(block.ToolInput, "TargetFile"); path != "" {
				raw = append(raw, FileChange{Path: path, Action: "created"})
			}
		case "replace_file_content":
			if path := getToolInputString(block.ToolInput, "TargetFile"); path != "" {
				raw = append(raw, FileChange{Path: path, Action: "modified"})
			}
		case "write_file":
			if path := getToolInputString(block.ToolInput, "file_path"); path != "" {
				raw = append(raw, FileChange{Path: path, Action: "created"})
			}
		case "edit": // OpenCode's edit tool
			if path := getToolInputString(block.ToolInput, "filePath"); path != "" {
				action := "created"
				if hasToolInputKey(block.ToolInput, "oldString") {
					action = "modified"
				}
				raw = append(raw, FileChange{Path: path, Action: action})
			}
		case "Bash", "run_shell_command", "bash":
			if cmd := getToolInputString(block.ToolInput, "command"); cmd != "" {
				raw = append(raw, bashFileOps(cmd)...)
			}
		case "run_command":
			if cmd := getToolInputString(block.ToolInput, "CommandLine"); cmd != "" {
				raw = append(raw, bashFileOps(cmd)...)
			}
		}
	}

	merged := make(map[string]string)
	var order []string
	for _, item := range raw {
		prev, exists := merged[item.Path]
		if exists && prev == "created" && item.Action == "modified" {
			continue
		}
		if !exists {
			order = append(order, item.Path)
		}
		merged[item.Path] = item.Action
	}

	out := make([]FileChange, 0, len(order))
	for _, path := range order {
		out = append(out, FileChange{Path: path, Action: merged[path]})
	}
	return out
}

func (p *Prompt) IsCommand() bool {
	if commandRE.MatchString(p.UserText) {
		return true
	}
	text := strings.TrimSpace(p.UserText)
	if text == "" {
		return false
	}
	firstLine := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	if slashCmdRE.MatchString(firstLine) {
		for _, prefix := range []string{"/data", "/home", "/usr", "/etc", "/tmp", "/var", "/opt"} {
			if strings.HasPrefix(firstLine, prefix) {
				return false
			}
		}
		return true
	}
	return false
}

func (p *Prompt) IsTaskNotification() bool {
	return taskNotificationRE.MatchString(p.UserText)
}

func (p *Prompt) Summary() string {
	if m := commandRE.FindStringSubmatch(p.UserText); m != nil {
		return "[cmd] " + m[1]
	}
	if m := taskNotificationRE.FindStringSubmatch(p.UserText); m != nil {
		text := strings.TrimSpace(m[1])
		runes := []rune(text)
		if len(runes) > 100 {
			text = string(runes[:97]) + "..."
		}
		return "[bg] " + text
	}
	text := strings.TrimSpace(p.UserText)
	firstLine := ""
	if text != "" {
		lines := strings.SplitN(text, "\n", 2)
		firstLine = strings.TrimSpace(lines[0])
	}
	if slashCmdRE.MatchString(firstLine) {
		isPath := false
		for _, prefix := range []string{"/data", "/home", "/usr", "/etc", "/tmp", "/var", "/opt"} {
			if strings.HasPrefix(firstLine, prefix) {
				isPath = true
				break
			}
		}
		if !isPath {
			runes := []rune(firstLine)
			if len(runes) > 100 {
				firstLine = string(runes[:97]) + "..."
			}
			return "[cmd] " + firstLine
		}
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

// Project is one project entity with session logs.
type Project struct {
	DirPath      string
	DisplayName  string
	Cwd          string
	LastActivity string
	PromptCount  int
	Source       string // "claude" | "agy" | "gemini" | "all" | "agy+claude" ...
	SessionFiles []string
	ConvMetadata map[string]map[string]string
}

func (p *Project) GetSessionFiles() []string {
	if len(p.SessionFiles) > 0 {
		return p.SessionFiles
	}
	return source.ListSessionFiles(p.DirPath)
}

func (p *Project) LoadPrompts() []Prompt {
	files := p.GetSessionFiles()
	if len(files) > 0 {
		return BuildPromptsForFiles(files, p.ConvMetadata)
	}
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

func cleanToolInput(raw json.RawMessage) []KV {
	kvs := decodeOrderedTopLevel(raw)
	for i, kv := range kvs {
		if s, ok := kv.Value.(string); ok {
			s = strings.TrimSpace(s)
			if len(s) >= 2 && strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") {
				var unquoted string
				if err := json.Unmarshal([]byte(s), &unquoted); err == nil {
					kvs[i].Value = unquoted
				}
			}
		}
	}
	return kvs
}

// DetectFileFormat examines the start of a session file to determine its format.
func DetectFileFormat(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "claude"
	}
	defer f.Close()

	buf := make([]byte, 4096)
	n, _ := io.ReadFull(f, buf)
	chunk := string(buf[:n])
	trimmed := strings.TrimSpace(chunk)
	if strings.HasPrefix(trimmed, "{") {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
			if _, ok := obj["messages"]; ok {
				return "gemini_json"
			}
		}
	}

	lines := strings.Split(chunk, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]interface{}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		rtype, _ := rec["type"].(string)
		if rtype == "agy_metadata" {
			return "agy"
		}
		if rtype == "opencode_metadata" {
			return "opencode"
		}
		if rtype == "gemini_metadata" {
			return "gemini_jsonl"
		}
		if rtype == "USER_INPUT" || rtype == "PLANNER_RESPONSE" || rtype == "LIST_DIRECTORY" || rtype == "VIEW_FILE" {
			return "agy"
		}
		src, _ := rec["source"].(string)
		if src == "USER_EXPLICIT" || src == "MODEL" {
			return "agy"
		}
		if _, hasMsg := rec["message"]; hasMsg && (rtype == "user" || rtype == "assistant") {
			return "claude"
		}
		if rtype == "gemini" {
			return "gemini_jsonl"
		}
		if rtype == "user" {
			if _, isList := rec["content"].([]interface{}); isList {
				return "gemini_jsonl"
			}
		}
		if _, hasHash := rec["projectHash"]; hasHash {
			return "gemini_jsonl"
		}
		if _, hasSet := rec["$set"]; hasSet {
			return "gemini_jsonl"
		}
	}
	return "claude"
}

// ParseClaudeSessionFile returns the Prompts found in a single Claude session jsonl.
func ParseClaudeSessionFile(path string) []Prompt {
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
				Source:    "claude",
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

type rawAgyRecord struct {
	StepIndex int              `json:"step_index"`
	Source    string           `json:"source"`
	Type      string           `json:"type"`
	Status    string           `json:"status"`
	CreatedAt string           `json:"created_at"`
	Timestamp string           `json:"timestamp"`
	Content   string           `json:"content"`
	ToolCalls []rawAgyToolCall `json:"tool_calls"`
	GitBranch string           `json:"gitBranch"`
	SessionID string           `json:"sessionId"`
	Cwd       string           `json:"cwd"`
}

type rawAgyToolCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// ParseAgyTranscriptFile returns Prompts found in an AGY transcript jsonl file.
func ParseAgyTranscriptFile(path string, sessionID, defaultBranch string) []Prompt {
	var prompts []Prompt
	var current *Prompt
	branch := defaultBranch
	sid := sessionID
	if sid == "" {
		parent := filepath.Dir(path)
		if filepath.Base(parent) == "logs" {
			pdir := filepath.Dir(parent)
			if filepath.Base(pdir) == ".system_generated" {
				sid = filepath.Base(filepath.Dir(pdir))
			}
		}
	}
	if sid == "" || sid == "brain" {
		sid = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if strings.HasPrefix(sid, "agy-") {
		sid = sid[4:]
	}

	f, err := os.Open(path)
	if err != nil {
		return prompts
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec rawAgyRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}

		if rec.Type == "agy_metadata" {
			if rec.GitBranch != "" {
				branch = rec.GitBranch
			}
			if rec.SessionID != "" {
				sid = rec.SessionID
			}
			continue
		}

		if rec.Type == "USER_INPUT" || (rec.Source == "USER_EXPLICIT" && rec.Content != "") {
			content := rec.Content
			userText := ""
			if m := userRequestRE.FindStringSubmatch(content); m != nil {
				userText = strings.TrimSpace(m[1])
			} else {
				clean := additionalMetaRE.ReplaceAllString(content, "")
				clean = userSettingsRE.ReplaceAllString(clean, "")
				userText = strings.TrimSpace(clean)
			}
			ts := rec.CreatedAt
			if ts == "" {
				ts = rec.Timestamp
			}
			prompts = append(prompts, Prompt{
				SessionID: sid,
				Timestamp: ts,
				Branch:    branch,
				Sidechain: false,
				UserText:  userText,
				Source:    "agy",
			})
			current = &prompts[len(prompts)-1]
		} else if rec.Type == "PLANNER_RESPONSE" && current != nil {
			for _, tc := range rec.ToolCalls {
				tname := tc.Name
				if tname == "" {
					tname = "?"
				}
				kvs := cleanToolInput(tc.Args)
				current.Blocks = append(current.Blocks, AssistantBlock{
					Kind:      "tool_use",
					ToolName:  tname,
					ToolInput: kvs,
				})
			}
			if rec.Content != "" {
				current.Blocks = append(current.Blocks, AssistantBlock{
					Kind: "text",
					Text: rec.Content,
				})
			}
		}
	}
	return prompts
}

// ParseGeminiJsonFile parses legacy Gemini CLI session from a JSON file.
func ParseGeminiJsonFile(path string) []Prompt {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc struct {
		SessionID string `json:"sessionId"`
		Messages  []struct {
			Type      string          `json:"type"`
			Timestamp string          `json:"timestamp"`
			Content   json.RawMessage `json:"content"`
			Tokens    struct {
				Total int64 `json:"total"`
			} `json:"tokens"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}

	sessionID := doc.SessionID
	if sessionID == "" {
		sessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	var prompts []Prompt
	var current *Prompt

	for _, msg := range doc.Messages {
		if msg.Type == "user" {
			var texts []string
			var list []interface{}
			if err := json.Unmarshal(msg.Content, &list); err == nil {
				for _, item := range list {
					if m, ok := item.(map[string]interface{}); ok {
						if t, ok := m["text"].(string); ok {
							texts = append(texts, t)
						}
					} else if s, ok := item.(string); ok {
						texts = append(texts, s)
					}
				}
			} else {
				var s string
				if err := json.Unmarshal(msg.Content, &s); err == nil {
					texts = append(texts, s)
				}
			}
			prompts = append(prompts, Prompt{
				SessionID: sessionID,
				Timestamp: msg.Timestamp,
				UserText:  strings.Join(texts, "\n"),
				Source:    "gemini",
			})
			current = &prompts[len(prompts)-1]
		} else if msg.Type == "gemini" && current != nil {
			var s string
			if err := json.Unmarshal(msg.Content, &s); err == nil && s != "" {
				current.Blocks = append(current.Blocks, AssistantBlock{Kind: "text", Text: s})
			}
			current.TotalTokens += msg.Tokens.Total
		}
	}
	return prompts
}

// ParseGeminiJsonlFile parses legacy Gemini CLI session from a JSONL file.
func ParseGeminiJsonlFile(path string) []Prompt {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	sessionID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	var prompts []Prompt
	var current *Prompt

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionId"`
			StartTime string `json:"startTime"`
			Timestamp string `json:"timestamp"`
			Set       *struct {
				Messages []struct {
					Type      string          `json:"type"`
					Timestamp string          `json:"timestamp"`
					Content   json.RawMessage `json:"content"`
					Tokens    struct {
						Total int64 `json:"total"`
					} `json:"tokens"`
				} `json:"messages"`
			} `json:"$set"`
			Content json.RawMessage `json:"content"`
			Tokens  *struct {
				Total int64 `json:"total"`
			} `json:"tokens"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}

		if rec.Type == "gemini_metadata" && rec.SessionID != "" {
			sessionID = rec.SessionID
			continue
		}
		if rec.SessionID != "" && rec.StartTime != "" {
			sessionID = rec.SessionID
			continue
		}
		if rec.Set != nil {
			for _, msg := range rec.Set.Messages {
				if msg.Type == "user" {
					var texts []string
					var list []interface{}
					if err := json.Unmarshal(msg.Content, &list); err == nil {
						for _, item := range list {
							if m, ok := item.(map[string]interface{}); ok {
								if t, ok := m["text"].(string); ok {
									texts = append(texts, t)
								}
							} else if s, ok := item.(string); ok {
								texts = append(texts, s)
							}
						}
					} else {
						var s string
						if err := json.Unmarshal(msg.Content, &s); err == nil {
							texts = append(texts, s)
						}
					}
					prompts = append(prompts, Prompt{
						SessionID: sessionID,
						Timestamp: msg.Timestamp,
						UserText:  strings.Join(texts, "\n"),
						Source:    "gemini",
					})
					current = &prompts[len(prompts)-1]
				} else if msg.Type == "gemini" && current != nil {
					var s string
					if err := json.Unmarshal(msg.Content, &s); err == nil && s != "" {
						current.Blocks = append(current.Blocks, AssistantBlock{Kind: "text", Text: s})
					}
					current.TotalTokens += msg.Tokens.Total
				}
			}
			continue
		}

		if rec.Type == "user" {
			var texts []string
			var list []interface{}
			if err := json.Unmarshal(rec.Content, &list); err == nil {
				for _, item := range list {
					if m, ok := item.(map[string]interface{}); ok {
						if t, ok := m["text"].(string); ok {
							texts = append(texts, t)
						}
					} else if s, ok := item.(string); ok {
						texts = append(texts, s)
					}
				}
			} else {
				var s string
				if err := json.Unmarshal(rec.Content, &s); err == nil {
					texts = append(texts, s)
				}
			}
			prompts = append(prompts, Prompt{
				SessionID: sessionID,
				Timestamp: rec.Timestamp,
				UserText:  strings.Join(texts, "\n"),
				Source:    "gemini",
			})
			current = &prompts[len(prompts)-1]
		} else if rec.Type == "gemini" && current != nil {
			var s string
			if err := json.Unmarshal(rec.Content, &s); err == nil && s != "" {
				current.Blocks = append(current.Blocks, AssistantBlock{Kind: "text", Text: s})
			}
			if rec.Tokens != nil {
				current.TotalTokens += rec.Tokens.Total
			}
		}
	}
	return prompts
}

// ParseOpenCodeSessionFile returns the Prompts found in a materialized OpenCode session jsonl.
func ParseOpenCodeSessionFile(path string) []Prompt {
	prompts := ParseClaudeSessionFile(path)
	for i := range prompts {
		prompts[i].Source = "opencode"
	}
	return prompts
}

// ParseSessionFile returns the Prompts found in a session file according to detected format.
func ParseSessionFile(path string, defaultBranch, sessionID string) []Prompt {
	fmtType := DetectFileFormat(path)
	switch fmtType {
	case "agy":
		return ParseAgyTranscriptFile(path, sessionID, defaultBranch)
	case "opencode":
		return ParseOpenCodeSessionFile(path)
	case "gemini_json":
		return ParseGeminiJsonFile(path)
	case "gemini_jsonl":
		return ParseGeminiJsonlFile(path)
	default:
		return ParseClaudeSessionFile(path)
	}
}

func formatTimestamp(val interface{}) string {
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return v
	case float64:
		if v > 100_000_000_000 {
			v /= 1000.0
		}
		sec := int64(v)
		nsec := int64((v - float64(sec)) * 1e9)
		return time.Unix(sec, nsec).UTC().Format(time.RFC3339)
	case json.Number:
		if f, err := v.Float64(); err == nil {
			return formatTimestamp(f)
		}
		return v.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

func sessionStartTimestamp(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	buf := make([]byte, 4096)
	n, _ := io.ReadFull(f, buf)
	chunk := string(buf[:n])
	trimmed := strings.TrimSpace(chunk)
	if strings.HasPrefix(trimmed, "{") {
		var doc struct {
			Messages []struct {
				Timestamp interface{} `json:"timestamp"`
			} `json:"messages"`
		}
		if err := json.Unmarshal([]byte(trimmed), &doc); err == nil && len(doc.Messages) > 0 {
			return formatTimestamp(doc.Messages[0].Timestamp)
		}
	}

	lines := strings.Split(chunk, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]interface{}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.UseNumber()
		if err := dec.Decode(&rec); err != nil {
			continue
		}
		if ts, ok := rec["timestamp"]; ok && ts != nil && ts != "" {
			return formatTimestamp(ts)
		}
		if ca, ok := rec["created_at"]; ok && ca != nil && ca != "" {
			return formatTimestamp(ca)
		}
		if st, ok := rec["startTime"]; ok && st != nil && st != "" {
			return formatTimestamp(st)
		}
	}
	return ""
}

// BuildPromptsForFiles merges the given session files into one time-ordered list.
func BuildPromptsForFiles(files []string, convMeta map[string]map[string]string) []Prompt {
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
		f := files[idx]
		stem := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		meta := convMeta[stem]
		if meta == nil {
			dir := filepath.Dir(f)
			if filepath.Base(dir) == "logs" {
				pdir := filepath.Dir(dir)
				if filepath.Base(pdir) == ".system_generated" {
					convID := filepath.Base(filepath.Dir(pdir))
					meta = convMeta[convID]
				}
			}
		}
		branch := ""
		sid := stem
		if meta != nil {
			if b := meta["branch"]; b != "" {
				branch = b
			}
			if id := meta["id"]; id != "" {
				sid = id
			}
		}
		prompts = append(prompts, ParseSessionFile(f, branch, sid)...)
	}
	return prompts
}

// BuildPrompts merges every session file under projectDir into one time-ordered list.
func BuildPrompts(projectDir string) []Prompt {
	files := source.ListSessionFiles(projectDir)
	return BuildPromptsForFiles(files, nil)
}

// FindCwdField finds the cwd recorded in session files under projectDir.
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

// LoadProjectWithFilter loads a Project respecting the source filter.
func LoadProjectWithFilter(target, sourceFilter string) Project {
	files := source.ListSessionFiles(target)
	if len(files) > 0 {
		cwd := FindCwdField(target)
		displayName := filepath.Base(target)
		if cwd != "" {
			displayName = filepath.Base(cwd)
		}
		lastActivity := ""
		for _, f := range files {
			if ts := sessionStartTimestamp(f); ts > lastActivity {
				lastActivity = ts
			}
		}
		prompts := BuildPromptsForFiles(files, nil)
		return Project{
			DirPath:      target,
			DisplayName:  displayName,
			Cwd:          cwd,
			LastActivity: lastActivity,
			PromptCount:  len(prompts),
			Source:       sourceFilter,
			SessionFiles: files,
		}
	}

	info := source.FindDirectProjectInfo(target, sourceFilter)
	var allFiles []string
	metadata := make(map[string]map[string]string)
	sourcesFound := make(map[string]bool)

	for _, f := range info.ClaudeFiles {
		allFiles = append(allFiles, f)
		sourcesFound["claude"] = true
	}

	for _, c := range info.AgyConvs {
		if c.TranscriptPath != "" {
			allFiles = append(allFiles, c.TranscriptPath)
			m := map[string]string{
				"id":        c.ID,
				"workspace": c.Workspace,
				"branch":    c.Branch,
				"title":     c.Title,
			}
			metadata[c.ID] = m
			tstem := strings.TrimSuffix(filepath.Base(c.TranscriptPath), filepath.Ext(c.TranscriptPath))
			metadata[tstem] = m
			sourcesFound["agy"] = true
		}
	}

	for _, gf := range info.GeminiFiles {
		allFiles = append(allFiles, gf)
		sourcesFound["gemini"] = true
	}

	for _, c := range info.OpenCodeConvs {
		if c.TranscriptPath != "" {
			if _, err := os.Stat(c.TranscriptPath); err == nil {
				allFiles = append(allFiles, c.TranscriptPath)
				m := map[string]string{
					"id":        c.ID,
					"workspace": c.Workspace,
					"branch":    c.Branch,
					"title":     c.Title,
				}
				metadata[c.ID] = m
				tstem := strings.TrimSuffix(filepath.Base(c.TranscriptPath), filepath.Ext(c.TranscriptPath))
				metadata[tstem] = m
				sourcesFound["opencode"] = true
			}
		}
	}

	var srcKeys []string
	for k := range sourcesFound {
		srcKeys = append(srcKeys, k)
	}
	sort.Strings(srcKeys)
	srcLabel := strings.Join(srcKeys, "+")
	if srcLabel == "" {
		if sourceFilter == "claude" || sourceFilter == "opencode" {
			srcLabel = sourceFilter
		} else {
			srcLabel = "agy"
		}
	}

	lastActivity := ""
	for _, f := range allFiles {
		if ts := sessionStartTimestamp(f); ts > lastActivity {
			lastActivity = ts
		}
	}
	prompts := BuildPromptsForFiles(allFiles, metadata)

	dispName := info.DisplayName
	if dispName == "" {
		dispName = filepath.Base(target)
	}
	cwdStr := info.Cwd
	if cwdStr == "" {
		cwdStr = target
	}

	return Project{
		DirPath:      target,
		DisplayName:  dispName,
		Cwd:          cwdStr,
		LastActivity: lastActivity,
		PromptCount:  len(prompts),
		Source:       srcLabel,
		SessionFiles: allFiles,
		ConvMetadata: metadata,
	}
}

// LoadProject builds a Project summary for projectDir.
func LoadProject(projectDir string) Project {
	return LoadProjectWithFilter(projectDir, "all")
}

// LoadProjectsWithFilter returns all projects across sources or from aggregateRoot.
func LoadProjectsWithFilter(aggregateRoot, sourceFilter string) []Project {
	home, _ := os.UserHomeDir()
	claudeDir := source.ClaudeProjectsDir()

	// 1. Custom aggregate root (e.g. backup directory)
	if aggregateRoot != "" && aggregateRoot != claudeDir && aggregateRoot != home {
		dirs := source.ListProjectDirs(aggregateRoot)
		out := make([]Project, 0, len(dirs))
		for _, d := range dirs {
			out = append(out, LoadProjectWithFilter(d, sourceFilter))
		}
		return out
	}

	// 2. Claude-only aggregate
	if sourceFilter == "claude" {
		dirs := source.ListProjectDirs(claudeDir)
		out := make([]Project, 0, len(dirs))
		for _, d := range dirs {
			out = append(out, LoadProjectWithFilter(d, "claude"))
		}
		return out
	}

	// 3. Global multi-source aggregate (Claude + AGY + Gemini + OpenCode)
	includeClaude := sourceFilter == "all" || sourceFilter == "claude"
	includeAgy := sourceFilter == "all" || sourceFilter == "agy" || sourceFilter == "gemini"
	includeOpenCode := sourceFilter == "all" || sourceFilter == "opencode"

	type wsEntry struct {
		cwd           string
		displayName   string
		claudeFiles   []string
		agyConvs      []source.AgyConvInfo
		geminiFiles   []string
		opencodeConvs []source.OpenCodeConvInfo
		metadata      map[string]map[string]string
	}
	projectsByWS := make(map[string]*wsEntry)
	getOrCreate := func(ws string) *wsEntry {
		e, ok := projectsByWS[ws]
		if !ok {
			e = &wsEntry{
				cwd:         ws,
				displayName: filepath.Base(ws),
				metadata:    make(map[string]map[string]string),
			}
			projectsByWS[ws] = e
		}
		return e
	}

	if includeClaude {
		if fi, err := os.Stat(claudeDir); err == nil && fi.IsDir() {
			for _, pdir := range source.ListProjectDirs(claudeDir) {
				cFiles := source.ListSessionFiles(pdir)
				if len(cFiles) == 0 {
					continue
				}
				cwd := FindCwdField(pdir)
				if cwd == "" {
					cwd = pdir
				}
				e := getOrCreate(cwd)
				e.claudeFiles = append(e.claudeFiles, cFiles...)
			}
		}
	}

	if includeAgy {
		for _, c := range source.ScanAgyConversations("") {
			ws := c.Workspace
			if ws == "" {
				ws = "agy-" + c.ID
				if len(ws) > 12 {
					ws = ws[:12]
				}
			}
			e := getOrCreate(ws)
			e.agyConvs = append(e.agyConvs, c)
			e.metadata[c.ID] = map[string]string{
				"id":        c.ID,
				"workspace": c.Workspace,
				"branch":    c.Branch,
				"title":     c.Title,
			}
		}

		for _, g := range source.ScanGeminiTmpProjects("") {
			ws := g.Workspace
			if ws == "" {
				ws = g.DirName
			}
			e := getOrCreate(ws)
			e.geminiFiles = append(e.geminiFiles, g.ChatFiles...)
		}
	}

	if includeOpenCode {
		for _, c := range source.ScanOpenCodeConversations("", "") {
			ws := c.Workspace
			if ws == "" {
				ws = "opencode-" + c.ID
				if len(ws) > 17 {
					ws = ws[:17]
				}
			}
			e := getOrCreate(ws)
			e.opencodeConvs = append(e.opencodeConvs, c)
			e.metadata[c.ID] = map[string]string{
				"id":        c.ID,
				"workspace": c.Workspace,
				"branch":    c.Branch,
				"title":     c.Title,
			}
		}
	}

	var sortedWS []string
	for ws := range projectsByWS {
		sortedWS = append(sortedWS, ws)
	}
	sort.Strings(sortedWS)

	var out []Project
	for _, ws := range sortedWS {
		entry := projectsByWS[ws]
		var allFiles []string
		sourcesFound := make(map[string]bool)

		for _, f := range entry.claudeFiles {
			allFiles = append(allFiles, f)
			sourcesFound["claude"] = true
		}
		for _, c := range entry.agyConvs {
			if c.TranscriptPath != "" {
				allFiles = append(allFiles, c.TranscriptPath)
				tstem := strings.TrimSuffix(filepath.Base(c.TranscriptPath), filepath.Ext(c.TranscriptPath))
				entry.metadata[tstem] = entry.metadata[c.ID]
				sourcesFound["agy"] = true
			}
		}
		for _, gf := range entry.geminiFiles {
			allFiles = append(allFiles, gf)
			sourcesFound["gemini"] = true
		}
		for _, c := range entry.opencodeConvs {
			if c.TranscriptPath != "" {
				if _, err := os.Stat(c.TranscriptPath); err == nil {
					allFiles = append(allFiles, c.TranscriptPath)
					tstem := strings.TrimSuffix(filepath.Base(c.TranscriptPath), filepath.Ext(c.TranscriptPath))
					entry.metadata[tstem] = entry.metadata[c.ID]
					sourcesFound["opencode"] = true
				}
			}
		}

		if len(allFiles) == 0 {
			continue
		}

		var srcKeys []string
		for k := range sourcesFound {
			srcKeys = append(srcKeys, k)
		}
		sort.Strings(srcKeys)
		srcLabel := strings.Join(srcKeys, "+")

		lastActivity := ""
		for _, f := range allFiles {
			if ts := sessionStartTimestamp(f); ts > lastActivity {
				lastActivity = ts
			}
		}
		prompts := BuildPromptsForFiles(allFiles, entry.metadata)

		pdir := ws
		if len(entry.claudeFiles) > 0 {
			pdir = filepath.Dir(entry.claudeFiles[0])
		}

		out = append(out, Project{
			DirPath:      pdir,
			DisplayName:  entry.displayName,
			Cwd:          entry.cwd,
			LastActivity: lastActivity,
			PromptCount:  len(prompts),
			Source:       srcLabel,
			SessionFiles: allFiles,
			ConvMetadata: entry.metadata,
		})
	}

	return out
}

// LoadProjects returns one Project per subdirectory of aggregateRoot.
func LoadProjects(aggregateRoot string) []Project {
	return LoadProjectsWithFilter(aggregateRoot, "all")
}
