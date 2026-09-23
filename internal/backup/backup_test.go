package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cheoljoo/ai-prompt-log/internal/model"
	"github.com/cheoljoo/ai-prompt-log/internal/source"
)

func TestSelectProjectsNoDepth(t *testing.T) {
	projs := SelectProjects("/data01/cheoljoo.lee/code/ai-prompt-log", nil)
	if len(projs) != 1 {
		t.Fatalf("expected exactly 1 project with no --depth, got %d: %v", len(projs), projs)
	}
	t.Logf("depth=nil -> %s (%s)", projs[0].DisplayName, projs[0].Source)
}

func TestSelectProjectsWithDepth(t *testing.T) {
	depth := 1
	projs := SelectProjectsWithFilter("/data01/cheoljoo.lee/code/ai-prompt-log", &depth, "claude")
	if len(projs) == 0 {
		t.Fatalf("expected projects under ~/code (depth=1), got 0")
	}
	for _, p := range projs {
		if !strings.HasPrefix(p.Cwd, "/data01/cheoljoo.lee/code") {
			t.Fatalf("project %s has cwd %s not under ~/code", p.DisplayName, p.Cwd)
		}
	}
	t.Logf("depth=1 (claude) -> %d projects (under /data01/cheoljoo.lee/code)", len(projs))
}

func TestCopyProjectIncremental(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	f1 := filepath.Join(src, "session1.jsonl")
	if err := os.WriteFile(f1, []byte(`{"type":"user","message":{"content":"hi"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	copied, updated, unchanged := CopyProjectDir(src, dst)
	if copied != 1 || updated != 0 || unchanged != 0 {
		t.Fatalf("first copy: expected (1,0,0), got (%d,%d,%d)", copied, updated, unchanged)
	}

	copied, updated, unchanged = CopyProjectDir(src, dst)
	if copied != 0 || updated != 0 || unchanged != 1 {
		t.Fatalf("rerun unchanged: expected (0,0,1), got (%d,%d,%d)", copied, updated, unchanged)
	}

	if err := os.WriteFile(f1, []byte(`{"type":"user","message":{"content":"hi"}}`+"\nmore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(f1, future, future); err != nil {
		t.Fatal(err)
	}
	copied, updated, unchanged = CopyProjectDir(src, dst)
	if copied != 0 || updated != 1 || unchanged != 0 {
		t.Fatalf("after content change: expected (0,1,0), got (%d,%d,%d)", copied, updated, unchanged)
	}

	// source deletion must not remove anything from the backup
	if err := os.Remove(f1); err != nil {
		t.Fatal(err)
	}
	destFile := filepath.Join(dst, source.EncodePath(src), "session1.jsonl")
	if _, err := os.Stat(destFile); err != nil {
		t.Fatalf("expected backup copy to survive source deletion: %v", err)
	}
}

func TestCopyAgyRoundTrip(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	agyFile := filepath.Join(src, "transcript.jsonl")
	agyContent := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-09-19T05:00:00Z","content":"<USER_REQUEST>agy backup test</USER_REQUEST>"}` + "\n" +
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","content":"agy backup done"}` + "\n"
	if err := os.WriteFile(agyFile, []byte(agyContent), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := model.Project{
		DirPath:      src,
		DisplayName:  "test-agy",
		Cwd:          src,
		SessionFiles: []string{agyFile},
		ConvMetadata: map[string]map[string]string{
			"transcript": {"id": "conv-123", "branch": "feat-agy"},
		},
	}

	copied, updated, unchanged := CopyProject(proj, dst)
	if copied != 1 || updated != 0 || unchanged != 0 {
		t.Fatalf("expected (1,0,0), got (%d,%d,%d)", copied, updated, unchanged)
	}

	// Verify backed up file format and contents
	destFile := filepath.Join(dst, source.EncodePath(src), "agy-conv-123.jsonl")
	if _, err := os.Stat(destFile); err != nil {
		t.Fatalf("expected backup file %s to exist: %v", destFile, err)
	}

	// Reload project from backup directory
	backupProj := model.LoadProject(filepath.Join(dst, source.EncodePath(src)))
	prompts := backupProj.LoadPrompts()
	if len(prompts) != 1 {
		t.Fatalf("expected 1 prompt from backup, got %d", len(prompts))
	}
	p := prompts[0]
	if p.Source != "agy" {
		t.Fatalf("expected source 'agy', got %q", p.Source)
	}
	if p.SessionID != "conv-123" {
		t.Fatalf("expected session ID 'conv-123', got %q", p.SessionID)
	}
	if p.Branch != "feat-agy" {
		t.Fatalf("expected branch 'feat-agy', got %q", p.Branch)
	}
	if p.UserText != "agy backup test" {
		t.Fatalf("expected user text 'agy backup test', got %q", p.UserText)
	}
	if len(p.Blocks) != 1 || p.Blocks[0].Text != "agy backup done" {
		t.Fatalf("unexpected blocks: %+v", p.Blocks)
	}
}

func TestCopyOpenCodeRoundTrip(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	opencodeFile := filepath.Join(src, "session1.jsonl")
	opencodeContent := `{"type":"opencode_metadata","cwd":"` + src + `","sessionId":"ses-999"}` + "\n" +
		`{"type":"user","sessionId":"ses-999","timestamp":"2026-09-23T00:00:00Z","message":{"content":"opencode backup test"}}` + "\n" +
		`{"type":"assistant","sessionId":"ses-999","timestamp":"2026-09-23T00:00:05Z","message":{"content":[{"type":"text","text":"opencode backup done"}]}}` + "\n"
	if err := os.WriteFile(opencodeFile, []byte(opencodeContent), 0o644); err != nil {
		t.Fatal(err)
	}

	proj := model.Project{
		DirPath:      src,
		DisplayName:  "test-opencode",
		Cwd:          src,
		SessionFiles: []string{opencodeFile},
		ConvMetadata: map[string]map[string]string{
			"session1": {"id": "ses-999"},
		},
	}

	copied, updated, unchanged := CopyProject(proj, dst)
	if copied != 1 || updated != 0 || unchanged != 0 {
		t.Fatalf("expected (1,0,0), got (%d,%d,%d)", copied, updated, unchanged)
	}

	destFile := filepath.Join(dst, source.EncodePath(src), "opencode-ses-999.jsonl")
	if _, err := os.Stat(destFile); err != nil {
		t.Fatalf("expected backup file %s to exist: %v", destFile, err)
	}

	backupProj := model.LoadProject(filepath.Join(dst, source.EncodePath(src)))
	prompts := backupProj.LoadPrompts()
	if len(prompts) != 1 {
		t.Fatalf("expected 1 prompt from backup, got %d", len(prompts))
	}
	p := prompts[0]
	if p.Source != "opencode" {
		t.Fatalf("expected source 'opencode', got %q", p.Source)
	}
	if p.UserText != "opencode backup test" {
		t.Fatalf("expected user text 'opencode backup test', got %q", p.UserText)
	}
	if len(p.Blocks) != 1 || p.Blocks[0].Text != "opencode backup done" {
		t.Fatalf("unexpected blocks: %+v", p.Blocks)
	}
}
