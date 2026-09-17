package wekafs

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The purge has to actually empty the tree, and leave the directory itself behind - the caller
// removes that afterwards, and a locar pass that took the directory with it would break the
// distinction between "emptied" and "gone".
func TestEmptyDirectoryRemovesContentsButNotTheDirectory(t *testing.T) {
	trash := t.TempDir()

	// Nested, because the trash holds whole volume subtrees rather than a flat list, and a walker
	// that only handled one level would look like it worked on an empty-ish fixture.
	for _, dir := range []string{"vol-a/inner/deeper", "vol-b"} {
		if err := os.MkdirAll(filepath.Join(trash, dir), 0o750); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	for _, f := range []string{"vol-a/one", "vol-a/inner/two", "vol-a/inner/deeper/three", "vol-b/four", "five"} {
		if err := os.WriteFile(filepath.Join(trash, f), []byte("x"), 0o640); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}

	entries, err := emptyDirectory(context.Background(), trash)
	if err != nil {
		t.Fatalf("expected the purge to succeed, got %v", err)
	}
	if entries == 0 {
		t.Error("expected the purge to account for the entries it deleted, got 0")
	}

	left, err := os.ReadDir(trash)
	if err != nil {
		t.Fatalf("expected the trash directory itself to survive the purge: %v", err)
	}
	if len(left) != 0 {
		names := make([]string, 0, len(left))
		for _, e := range left {
			names = append(names, e.Name())
		}
		t.Errorf("expected the trash to be empty, still holds %v", names)
	}
}

// An already-empty trash is the normal case on most runs, and must not be reported as a failure.
func TestEmptyDirectoryOnAnEmptyDirectory(t *testing.T) {
	if _, err := emptyDirectory(context.Background(), t.TempDir()); err != nil {
		t.Errorf("expected an empty directory to purge cleanly, got %v", err)
	}
}

// A missing trash path is an error rather than a silent success, which is why the caller checks the
// path exists first. Worth pinning, since the old binary behaved the same way and the guard in gc.go
// depends on it.
func TestEmptyDirectoryRejectsAMissingPath(t *testing.T) {
	if _, err := emptyDirectory(context.Background(), filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("expected a missing directory to be reported, got nil")
	}
}

// A file where a directory is expected must not be deleted as though it were trash.
func TestEmptyDirectoryRejectsAFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(f, []byte("x"), 0o640); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if _, err := emptyDirectory(context.Background(), f); err == nil {
		t.Error("expected a file to be rejected, got nil")
	}
	if _, err := os.Stat(f); err != nil {
		t.Errorf("expected the file to be left alone, got %v", err)
	}
}

// A cancelled purge has to say so, or a run cut short by the GC timeout would be recorded as a
// success and the trash left behind without a retry.
func TestEmptyDirectoryReportsCancellation(t *testing.T) {
	trash := t.TempDir()
	if err := os.WriteFile(filepath.Join(trash, "one"), []byte("x"), 0o640); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := emptyDirectory(ctx, trash); err == nil {
		t.Error("expected a cancelled purge to be reported, got nil")
	}
}

func TestEntryCounterCountsLines(t *testing.T) {
	c := &entryCounter{}
	// Split across writes, since the engine writes buffered chunks rather than whole lines.
	for _, chunk := range []string{"a\nb", "\nc\n", "no-newline-yet"} {
		n, err := c.Write([]byte(chunk))
		if err != nil || n != len(chunk) {
			t.Fatalf("expected the writer to consume everything, got n=%d err=%v", n, err)
		}
	}
	if got := c.count(); got != 3 {
		t.Errorf("expected 3 counted entries, got %d", got)
	}
}

// A special-type entry sitting directly in the trash root is the observable half of why the type
// list is "all" rather than locar's CLI default. Anything deeper is collateral of its parent
// directory - a delete-all deletes with RemoveAll - but the root's own children are the set locar
// actually enumerates, and one it does not recognise is simply logged as skipped.
//
// A FIFO is the portable way to prove it: character and block devices need root to create, mkfifo
// does not. The other half, DT_UNKNOWN, cannot be produced from a test - it depends on the
// filesystem not populating d_type - which is exactly why the type list must not rely on d_type
// being meaningful.
func TestEmptyDirectoryRemovesEntriesOutsideTheDefaultTypes(t *testing.T) {
	trash := t.TempDir()

	fifo := filepath.Join(trash, "a-fifo")
	if err := syscall.Mkfifo(fifo, 0o640); err != nil {
		t.Skipf("cannot create a FIFO here, skipping: %v", err)
	}
	// An ordinary file too, so a pass that handles only the default types still has something to do
	// and cannot pass by doing nothing at all.
	if err := os.WriteFile(filepath.Join(trash, "a-file"), []byte("x"), 0o640); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	if _, err := emptyDirectory(context.Background(), trash); err != nil {
		t.Fatalf("expected the purge to succeed, got %v", err)
	}

	if _, err := os.Lstat(fifo); err == nil {
		t.Error("expected the FIFO to be deleted; locar's default type list skips it entirely")
	}
	left, err := os.ReadDir(trash)
	if err != nil {
		t.Fatalf("expected the trash directory itself to survive: %v", err)
	}
	if len(left) != 0 {
		names := make([]string, 0, len(left))
		for _, e := range left {
			names = append(names, e.Name())
		}
		t.Errorf("expected the trash to be empty, still holds %v", names)
	}
}
