package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecord(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	entry := Entry{
		Timestamp:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ToolName:       "Bash",
		PermissionMode: "default",
		Decision:       "allow",
		ElapsedMS:      1234,
	}

	Record(path, 1024*1024, entry)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var decoded Entry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("invalid JSONL: %v", err)
	}
	if decoded.ToolName != "Bash" {
		t.Fatalf("got tool %q, want Bash", decoded.ToolName)
	}
	if decoded.Decision != "allow" {
		t.Fatalf("got decision %q, want allow", decoded.Decision)
	}
}

func TestRecordEmptyPath(t *testing.T) {
	t.Parallel()
	// Should not panic or create any file.
	Record("", 1024, Entry{ToolName: "test"})
}

func TestRotateIfNeeded(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	// Write more than maxSize.
	if err := os.WriteFile(path, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}

	RotateIfNeeded(path, 50)

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatal("expected rotated file .1 to exist")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("expected original file to be renamed")
	}
}

func TestRotateIfNeededUnderSize(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	if err := os.WriteFile(path, make([]byte, 10), 0o644); err != nil {
		t.Fatal(err)
	}

	RotateIfNeeded(path, 100)

	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Fatal("should not rotate when under size")
	}
}
