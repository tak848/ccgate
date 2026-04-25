package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tak848/ccgate/internal/metrics"
)

// TestMetricsReadsLegacyPath asserts that 'ccgate claude metrics'
// keeps showing entries that pre-v0.6 ccgate wrote to the legacy
// $XDG_STATE_HOME/ccgate/metrics.jsonl path. The Metrics function
// passes both the per-target path and the legacy path to
// metrics.PrintReport; this test exercises the wiring end-to-end so
// a future refactor that drops the fallback path will fail loudly.
func TestMetricsReadsLegacyPath(t *testing.T) {
	// XDG_STATE_HOME redirection is process-global, so this test
	// cannot run in parallel with anything else that touches
	// $XDG_STATE_HOME or $HOME.
	stateRoot := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateRoot)

	legacyDir := filepath.Join(stateRoot, "ccgate")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	entry := metrics.Entry{
		Timestamp:      time.Now(),
		SessionID:      "legacy-sess",
		ToolName:       "Bash",
		PermissionMode: "default",
		Decision:       "allow",
		ElapsedMS:      12,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDir, "metrics.jsonl")
	if err := os.WriteFile(legacyPath, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	// Drive Metrics with --json so the assertion is structural rather
	// than scraping the TTY table.
	var stdout, stderr bytes.Buffer
	exit := Metrics(&stdout, &stderr, t.TempDir(), MetricsOptions{
		Days:       7,
		AsJSON:     true,
		DetailsTop: 10,
	})
	if exit != 0 {
		t.Fatalf("Metrics exit = %d, stderr=%s", exit, stderr.String())
	}

	// PrintReport --json emits aggregates rather than raw entries, so
	// inspect the aggregate that would only be non-zero if our legacy
	// entry was actually picked up by the reader.
	var report struct {
		Tools []struct {
			ToolName string `json:"tool"`
			Total    int    `json:"total"`
			Allow    int    `json:"allow"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal report: %v\noutput:\n%s", err, stdout.String())
	}
	var bashTotal, bashAllow int
	for _, ts := range report.Tools {
		if ts.ToolName == "Bash" {
			bashTotal += ts.Total
			bashAllow += ts.Allow
		}
	}
	if bashTotal != 1 || bashAllow != 1 {
		t.Fatalf("expected legacy entry to count toward Bash totals (got total=%d allow=%d), output:\n%s", bashTotal, bashAllow, stdout.String())
	}
}
