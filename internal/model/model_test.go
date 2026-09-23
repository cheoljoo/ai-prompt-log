package model

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectFileFormat(t *testing.T) {
	dir := t.TempDir()

	// 1. Claude
	claudeF := filepath.Join(dir, "claude.jsonl")
	_ = os.WriteFile(claudeF, []byte(`{"type":"user","message":{"content":"hello world"},"sessionId":"s1"}`+"\n"), 0o644)
	if got := DetectFileFormat(claudeF); got != "claude" {
		t.Fatalf("detect claude: expected 'claude', got %q", got)
	}

	// 2. AGY
	agyF := filepath.Join(dir, "agy.jsonl")
	_ = os.WriteFile(agyF, []byte(`{"step_index":0,"type":"USER_INPUT","source":"USER_EXPLICIT","content":"<USER_REQUEST>hi</USER_REQUEST>"}`+"\n"), 0o644)
	if got := DetectFileFormat(agyF); got != "agy" {
		t.Fatalf("detect agy: expected 'agy', got %q", got)
	}

	// 3. AGY metadata
	agyMetaF := filepath.Join(dir, "agy_meta.jsonl")
	_ = os.WriteFile(agyMetaF, []byte(`{"type":"agy_metadata","cwd":"/test/path","sessionId":"abc"}`+"\n"), 0o644)
	if got := DetectFileFormat(agyMetaF); got != "agy" {
		t.Fatalf("detect agy_metadata: expected 'agy', got %q", got)
	}

	// 4. Gemini JSON
	geminiJSON := filepath.Join(dir, "gemini.json")
	_ = os.WriteFile(geminiJSON, []byte(`{"sessionId":"g1","messages":[{"type":"user","content":[{"text":"hi"}]}]}`), 0o644)
	if got := DetectFileFormat(geminiJSON); got != "gemini_json" {
		t.Fatalf("detect gemini_json: expected 'gemini_json', got %q", got)
	}

	// 5. Gemini JSONL
	geminiJSONL := filepath.Join(dir, "gemini.jsonl")
	_ = os.WriteFile(geminiJSONL, []byte(`{"type":"gemini_metadata","cwd":"/test/path"}`+"\n"), 0o644)
	if got := DetectFileFormat(geminiJSONL); got != "gemini_jsonl" {
		t.Fatalf("detect gemini_jsonl: expected 'gemini_jsonl', got %q", got)
	}

	// 6. OpenCode
	opencodeF := filepath.Join(dir, "opencode.jsonl")
	_ = os.WriteFile(opencodeF, []byte(`{"type":"opencode_metadata","cwd":"/test/path","sessionId":"ses_1"}`+"\n"), 0o644)
	if got := DetectFileFormat(opencodeF); got != "opencode" {
		t.Fatalf("detect opencode: expected 'opencode', got %q", got)
	}
}

func TestParseClaude(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "claude.jsonl")
	content := `{"type":"user","sessionId":"c1","timestamp":"2026-09-01T10:00:00Z","gitBranch":"main","message":{"content":"build feature X"}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}},{"type":"text","text":"Feature done"}],"usage":{"input_tokens":100,"output_tokens":50}}}` + "\n"
	_ = os.WriteFile(f, []byte(content), 0o644)

	prompts := ParseClaudeSessionFile(f)
	if len(prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(prompts))
	}
	p := prompts[0]
	if p.Source != "claude" {
		t.Fatalf("expected source 'claude', got %q", p.Source)
	}
	if p.UserText != "build feature X" {
		t.Fatalf("expected user text 'build feature X', got %q", p.UserText)
	}
	if p.Branch != "main" {
		t.Fatalf("expected branch 'main', got %q", p.Branch)
	}
	if p.TotalTokens != 150 {
		t.Fatalf("expected total tokens 150, got %d", p.TotalTokens)
	}
	if len(p.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(p.Blocks))
	}
	if p.Blocks[0].Kind != "tool_use" || p.Blocks[0].ToolName != "Bash" {
		t.Fatalf("unexpected block 0: %+v", p.Blocks[0])
	}
	if p.Blocks[1].Kind != "text" || p.Blocks[1].Text != "Feature done" {
		t.Fatalf("unexpected block 1: %+v", p.Blocks[1])
	}
}

func TestParseAgy(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "agy.jsonl")
	content := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-09-19T04:46:48Z","content":"<USER_REQUEST>\ncreate branch 260919-agy\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\ntime=now\n</ADDITIONAL_METADATA>"}` + "\n" +
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","tool_calls":[{"name":"run_command","args":{"CommandLine":"\"git status\""}}]}` + "\n" +
		`{"step_index":2,"source":"MODEL","type":"PLANNER_RESPONSE","content":"All done successfully."}` + "\n"
	_ = os.WriteFile(f, []byte(content), 0o644)

	prompts := ParseAgyTranscriptFile(f, "test-conv", "260919-agy")
	if len(prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(prompts))
	}
	p := prompts[0]
	if p.Source != "agy" {
		t.Fatalf("expected source 'agy', got %q", p.Source)
	}
	if p.UserText != "create branch 260919-agy" {
		t.Fatalf("expected user text 'create branch 260919-agy', got %q", p.UserText)
	}
	if p.Branch != "260919-agy" {
		t.Fatalf("expected branch '260919-agy', got %q", p.Branch)
	}
	if p.SessionID != "test-conv" {
		t.Fatalf("expected session ID 'test-conv', got %q", p.SessionID)
	}
	if len(p.Blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(p.Blocks))
	}
	if p.Blocks[0].Kind != "tool_use" || p.Blocks[0].ToolName != "run_command" {
		t.Fatalf("unexpected block 0: %+v", p.Blocks[0])
	}
	// ToolInput should be unquoted ("git status" rather than "\"git status\"")
	if len(p.Blocks[0].ToolInput) != 1 || p.Blocks[0].ToolInput[0].Key != "CommandLine" || p.Blocks[0].ToolInput[0].Value != "git status" {
		t.Fatalf("unexpected tool input: %+v", p.Blocks[0].ToolInput)
	}
	if p.Blocks[1].Kind != "text" || p.Blocks[1].Text != "All done successfully." {
		t.Fatalf("unexpected block 1: %+v", p.Blocks[1])
	}
}

func TestParseGeminiJson(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "session.json")
	content := `{"sessionId":"gemini-1","messages":[{"type":"user","timestamp":"2026-03-31T04:19:17Z","content":[{"text":"explain w3m"}]},{"type":"gemini","content":"w3m is a terminal web browser.","tokens":{"total":500}}]}`
	_ = os.WriteFile(f, []byte(content), 0o644)

	prompts := ParseGeminiJsonFile(f)
	if len(prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(prompts))
	}
	p := prompts[0]
	if p.Source != "gemini" {
		t.Fatalf("expected source 'gemini', got %q", p.Source)
	}
	if p.UserText != "explain w3m" {
		t.Fatalf("expected user text 'explain w3m', got %q", p.UserText)
	}
	if p.TotalTokens != 500 {
		t.Fatalf("expected total tokens 500, got %d", p.TotalTokens)
	}
	if len(p.Blocks) != 1 || p.Blocks[0].Text != "w3m is a terminal web browser." {
		t.Fatalf("unexpected block: %+v", p.Blocks)
	}
}

func TestParseOpenCode(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "opencode.jsonl")
	content := `{"type":"opencode_metadata","cwd":"/test/path","sessionId":"ses_1"}` + "\n" +
		`{"type":"user","sessionId":"ses_1","timestamp":"2026-09-23T00:00:00Z","gitBranch":"","message":{"content":"hello opencode"}}` + "\n" +
		`{"type":"assistant","sessionId":"ses_1","timestamp":"2026-09-23T00:00:05Z","gitBranch":"","message":{"content":[{"type":"text","text":"hi there"}],"usage":{"input_tokens":10,"output_tokens":20}}}` + "\n"
	_ = os.WriteFile(f, []byte(content), 0o644)

	prompts := ParseOpenCodeSessionFile(f)
	if len(prompts) != 1 {
		t.Fatalf("expected 1 prompt, got %d", len(prompts))
	}
	p := prompts[0]
	if p.Source != "opencode" {
		t.Fatalf("expected source 'opencode', got %q", p.Source)
	}
	if p.UserText != "hello opencode" {
		t.Fatalf("expected user text 'hello opencode', got %q", p.UserText)
	}
	if p.TotalTokens != 30 {
		t.Fatalf("expected total tokens 30, got %d", p.TotalTokens)
	}
	if len(p.Blocks) != 1 || p.Blocks[0].Text != "hi there" {
		t.Fatalf("unexpected blocks: %+v", p.Blocks)
	}
}

func TestFileChanges(t *testing.T) {
	// 1. Claude write and edit
	p1 := Prompt{
		Blocks: []AssistantBlock{
			{Kind: "tool_use", ToolName: "Write", ToolInput: []KV{{Key: "file_path", Value: "/a/new.py"}, {Key: "content", Value: "x"}}},
			{Kind: "tool_use", ToolName: "Edit", ToolInput: []KV{{Key: "file_path", Value: "/a/existing.py"}, {Key: "old_string", Value: "x"}, {Key: "new_string", Value: "y"}}},
		},
	}
	fc1 := p1.FileChanges()
	if len(fc1) != 2 || fc1[0] != (FileChange{Path: "/a/new.py", Action: "created"}) || fc1[1] != (FileChange{Path: "/a/existing.py", Action: "modified"}) {
		t.Fatalf("unexpected fc1: %+v", fc1)
	}

	// 2. Edit after write stays created
	p2 := Prompt{
		Blocks: []AssistantBlock{
			{Kind: "tool_use", ToolName: "Write", ToolInput: []KV{{Key: "file_path", Value: "/a/new.py"}, {Key: "content", Value: "x"}}},
			{Kind: "tool_use", ToolName: "Edit", ToolInput: []KV{{Key: "file_path", Value: "/a/new.py"}, {Key: "old_string", Value: "x"}, {Key: "new_string", Value: "y"}}},
		},
	}
	fc2 := p2.FileChanges()
	if len(fc2) != 1 || fc2[0] != (FileChange{Path: "/a/new.py", Action: "created"}) {
		t.Fatalf("unexpected fc2: %+v", fc2)
	}

	// 3. Bash rm and mv
	p3 := Prompt{
		Blocks: []AssistantBlock{
			{Kind: "tool_use", ToolName: "Bash", ToolInput: []KV{{Key: "command", Value: "rm -rf /tmp/foo /tmp/bar"}}},
			{Kind: "tool_use", ToolName: "Bash", ToolInput: []KV{{Key: "command", Value: "mv /tmp/old.txt /tmp/new.txt"}}},
		},
	}
	fc3 := p3.FileChanges()
	expected3 := []FileChange{
		{Path: "/tmp/foo", Action: "deleted"},
		{Path: "/tmp/bar", Action: "deleted"},
		{Path: "/tmp/old.txt", Action: "deleted"},
		{Path: "/tmp/new.txt", Action: "created"},
	}
	if len(fc3) != len(expected3) {
		t.Fatalf("expected len %d, got %d: %+v", len(expected3), len(fc3), fc3)
	}
	for i, e := range expected3 {
		if fc3[i] != e {
			t.Fatalf("idx %d: expected %+v, got %+v", i, e, fc3[i])
		}
	}

	// 4. AGY tools
	p4 := Prompt{
		Blocks: []AssistantBlock{
			{Kind: "tool_use", ToolName: "write_to_file", ToolInput: []KV{{Key: "TargetFile", Value: "/a/x.py"}}},
			{Kind: "tool_use", ToolName: "replace_file_content", ToolInput: []KV{{Key: "TargetFile", Value: "/a/y.py"}}},
			{Kind: "tool_use", ToolName: "run_command", ToolInput: []KV{{Key: "CommandLine", Value: "rm -f /tmp/z.txt"}}},
		},
	}
	fc4 := p4.FileChanges()
	expected4 := []FileChange{
		{Path: "/a/x.py", Action: "created"},
		{Path: "/a/y.py", Action: "modified"},
		{Path: "/tmp/z.txt", Action: "deleted"},
	}
	if len(fc4) != len(expected4) {
		t.Fatalf("expected len %d, got %d: %+v", len(expected4), len(fc4), fc4)
	}
	for i, e := range expected4 {
		if fc4[i] != e {
			t.Fatalf("idx %d: expected %+v, got %+v", i, e, fc4[i])
		}
	}

	// 5. OpenCode edit tool
	p5 := Prompt{
		Blocks: []AssistantBlock{
			{Kind: "tool_use", ToolName: "edit", ToolInput: []KV{{Key: "filePath", Value: "/a/new.py"}, {Key: "newString", Value: "x"}}},
			{Kind: "tool_use", ToolName: "edit", ToolInput: []KV{{Key: "filePath", Value: "/a/old.py"}, {Key: "oldString", Value: "x"}, {Key: "newString", Value: "y"}}},
			{Kind: "tool_use", ToolName: "bash", ToolInput: []KV{{Key: "command", Value: "rm /tmp/gone.py"}}},
		},
	}
	fc5 := p5.FileChanges()
	expected5 := []FileChange{
		{Path: "/a/new.py", Action: "created"},
		{Path: "/a/old.py", Action: "modified"},
		{Path: "/tmp/gone.py", Action: "deleted"},
	}
	if len(fc5) != len(expected5) {
		t.Fatalf("expected len %d, got %d: %+v", len(expected5), len(fc5), fc5)
	}
	for i, e := range expected5 {
		if fc5[i] != e {
			t.Fatalf("idx %d: expected %+v, got %+v", i, e, fc5[i])
		}
	}

	// 6. Gemini write_file
	p6 := Prompt{
		Blocks: []AssistantBlock{
			{Kind: "tool_use", ToolName: "write_file", ToolInput: []KV{{Key: "file_path", Value: "out.py"}}},
		},
	}
	fc6 := p6.FileChanges()
	if len(fc6) != 1 || fc6[0] != (FileChange{Path: "out.py", Action: "created"}) {
		t.Fatalf("unexpected fc6: %+v", fc6)
	}
}
