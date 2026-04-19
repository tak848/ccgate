package metrics

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

// TestRecordToolInputOmitZero locks in the omitzero behavior: an Entry with
// a zero-value ToolInputFields must produce a JSONL line that does NOT
// contain the tool_input key at all (so old-file shape is preserved and
// size overhead is zero).
func TestRecordToolInputOmitZero(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	Record(path, 1024*1024, Entry{
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ToolName:  "Bash",
		Decision:  "allow",
		ElapsedMS: 10,
		// ToolInput left as zero value intentionally.
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("tool_input")) {
		t.Errorf("zero-value ToolInput must be omitted, got line: %s", data)
	}
}

// TestRecordToolInputPopulated locks in the nested omitempty behavior:
// populated fields appear in JSONL, unset sibling fields are dropped.
func TestRecordToolInputPopulated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	Record(path, 1024*1024, Entry{
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ToolName:  "Bash",
		Decision:  "fallthrough",
		ElapsedMS: 10,
		ToolInput: ToolInputFields{Command: "gh pr list"},
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"tool_input":{"command":"gh pr list"}`) {
		t.Errorf("expected populated tool_input substring, got: %s", s)
	}
	// Unset fields must be omitted from the nested object.
	for _, f := range []string{"file_path", "path", "pattern"} {
		if strings.Contains(s, `"`+f+`"`) {
			t.Errorf("unset field %q should be omitted from JSONL, got: %s", f, s)
		}
	}

	// Round-trip: decoding must restore the value.
	var decoded Entry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ToolInput.Command != "gh pr list" {
		t.Errorf("round-trip Command = %q, want %q", decoded.ToolInput.Command, "gh pr list")
	}
}

// TestRecordToolInputMultiline verifies that the JSONL representation
// escapes literal LF as \n (two bytes 0x5C 0x6E) and that the Go value
// restored via json.Unmarshal contains a literal LF. This proves the
// display-layer \n→space collapse has not leaked into the write path.
func TestRecordToolInputMultiline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	Record(path, 1024*1024, Entry{
		Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ToolName:  "Bash",
		Decision:  "fallthrough",
		ElapsedMS: 10,
		ToolInput: ToolInputFields{Command: "line1\nline2"},
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Expect exactly one record line (one trailing LF for the line itself).
	trimmed := bytes.TrimRight(data, "\n")
	if bytes.Count(trimmed, []byte{'\n'}) != 0 {
		t.Errorf("record must occupy a single JSONL line, got: %q", data)
	}
	// Literal LF inside the JSON string must be escaped on disk.
	if !bytes.Contains(data, []byte(`line1\nline2`)) {
		t.Errorf("expected escaped \\n in JSONL, got: %s", data)
	}

	var decoded Entry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ToolInput.Command != "line1\nline2" {
		t.Errorf("Unmarshal should restore literal LF, got: %q", decoded.ToolInput.Command)
	}
	// And the display-layer collapse must NOT have leaked in.
	if strings.Contains(decoded.ToolInput.Command, "line1 line2") &&
		!strings.Contains(decoded.ToolInput.Command, "\n") {
		t.Errorf("display-layer \\n→space leaked into write path: %q", decoded.ToolInput.Command)
	}
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
