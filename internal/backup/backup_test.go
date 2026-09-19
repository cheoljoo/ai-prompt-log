package backup

import (
	"os"
	"path/filepath"
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
	realDirs := source.ListProjectDirs(source.ClaudeProjectsDir())
	if len(projs) != len(realDirs) {
		t.Fatalf("expected all %d real projects under ~/code (depth=1), got %d", len(realDirs), len(projs))
	}
	t.Logf("depth=1 (claude) -> %d projects (matches all real ~/.claude/projects entries)", len(projs))
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
