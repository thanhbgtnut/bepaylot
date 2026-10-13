package storage

import (
	"context"
	"os"
	"strings"
	"testing"
)

// A source file is on disk only while a task uses it: tasks working on the
// same file share one copy, and the last release removes it.
func TestTempFilesRemovedAfterUse(t *testing.T) {
	ctx := context.Background()
	mem := NewMemory()
	if _, err := mem.Put(ctx, "kb/doc/source.pdf", strings.NewReader("%PDF"), 4, "application/pdf"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files, err := NewTempFiles(dir, mem)
	if err != nil {
		t.Fatal(err)
	}
	p1, rel1, err := files.Acquire(ctx, "kb/doc/source.pdf")
	if err != nil {
		t.Fatal(err)
	}
	p2, rel2, err := files.Acquire(ctx, "kb/doc/source.pdf")
	if err != nil || p1 != p2 {
		t.Fatalf("second task: %q %q %v", p1, p2, err)
	}
	if b, err := os.ReadFile(p1); err != nil || string(b) != "%PDF" {
		t.Fatalf("content %q %v", b, err)
	}
	rel1()
	rel1() // releasing twice counts once
	if _, err := os.Stat(p1); err != nil {
		t.Fatalf("removed while a task still uses it: %v", err)
	}
	rel2()
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("%d files left on disk", len(left))
	}
	if _, _, err := files.Acquire(ctx, "kb/doc/missing.pdf"); err == nil {
		t.Fatal("missing object acquired")
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("%d files left after a failed download", len(left))
	}
}
