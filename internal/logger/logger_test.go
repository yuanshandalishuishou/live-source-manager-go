package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRotatingWriter verifies that the writer rolls over once it exceeds
// maxBytes and keeps the configured number of backups.
func TestRotatingWriter(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")

	w, err := newRotatingWriter(logPath, 100, 2) // 100 bytes, keep 2 backups
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	defer w.Close()

	// Write 3 chunks of ~60 bytes each -> forces at least one rotation.
	for i := 0; i < 3; i++ {
		chunk := strings.Repeat("x", 60) + "\n"
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	// Current log should exist and be small (rolled, not unbounded).
	fi, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("current log missing: %v", err)
	}
	if fi.Size() > 100 {
		t.Fatalf("current log not rotated, size=%d", fi.Size())
	}

	// At least one backup should exist.
	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatalf("backup .1 missing: %v", err)
	}

	// backup_count is 2, so .3 must NOT exist.
	if _, err := os.Stat(logPath + ".3"); err == nil {
		t.Fatalf("backup .3 should not exist (backup_count=2)")
	}
}

// TestRotatingDisabled keeps growing when maxBytes <= 0.
func TestRotatingDisabled(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "app.log")
	w, err := newRotatingWriter(logPath, 0, 3)
	if err != nil {
		t.Fatalf("newRotatingWriter: %v", err)
	}
	defer w.Close()

	for i := 0; i < 5; i++ {
		if _, err := w.Write([]byte(strings.Repeat("y", 200) + "\n")); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	fi, _ := os.Stat(logPath)
	if fi.Size() < 1000 {
		t.Fatalf("expected unbounded growth, size=%d", fi.Size())
	}
	if _, err := os.Stat(logPath + ".1"); err == nil {
		t.Fatalf("rotation should be disabled, but backup exists")
	}
}
