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
