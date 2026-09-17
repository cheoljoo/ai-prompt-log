package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cheoljoo/ai-prompt-log/internal/source"
)

func TestSelectProjectsNoDepth(t *testing.T) {
	dirs := SelectProjects("/data01/cheoljoo.lee/code/ai-prompt-log", nil)
	if len(dirs) != 1 {
		t.Fatalf("expected exactly 1 project with no --depth, got %d: %v", len(dirs), dirs)
	}
	t.Logf("depth=nil -> %s", dirs[0])
}

func TestSelectProjectsWithDepth(t *testing.T) {
	depth := 1
	dirs := SelectProjects("/data01/cheoljoo.lee/code/ai-prompt-log", &depth)
	realDirs := source.ListProjectDirs(source.ClaudeProjectsDir())
	if len(dirs) != len(realDirs) {
		t.Fatalf("expected all %d real projects under ~/code (depth=1), got %d", len(realDirs), len(dirs))
	}
	t.Logf("depth=1 -> %d projects (matches all real ~/.claude/projects entries)", len(dirs))
}

func TestCopyProjectIncremental(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	f1 := filepath.Join(src, "session1.jsonl")
	if err := os.WriteFile(f1, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	copied, updated, unchanged := CopyProject(src, dst)
	if copied != 1 || updated != 0 || unchanged != 0 {
		t.Fatalf("first copy: expected (1,0,0), got (%d,%d,%d)", copied, updated, unchanged)
	}

	copied, updated, unchanged = CopyProject(src, dst)
	if copied != 0 || updated != 0 || unchanged != 1 {
		t.Fatalf("rerun unchanged: expected (0,0,1), got (%d,%d,%d)", copied, updated, unchanged)
	}

	if err := os.WriteFile(f1, []byte(`{"type":"user"}`+"\nmore\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(5 * time.Second)
	if err := os.Chtimes(f1, future, future); err != nil {
		t.Fatal(err)
	}
	copied, updated, unchanged = CopyProject(src, dst)
	if copied != 0 || updated != 1 || unchanged != 0 {
		t.Fatalf("after content change: expected (0,1,0), got (%d,%d,%d)", copied, updated, unchanged)
	}

	// source deletion must not remove anything from the backup
	if err := os.Remove(f1); err != nil {
		t.Fatal(err)
	}
	destFile := filepath.Join(dst, filepath.Base(src), "session1.jsonl")
	if _, err := os.Stat(destFile); err != nil {
		t.Fatalf("expected backup copy to survive source deletion: %v", err)
	}
}
