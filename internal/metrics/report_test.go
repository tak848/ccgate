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

func TestBuildReport(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	now := time.Now().UTC()
	entries := []Entry{
		{Timestamp: now, ToolName: "Bash", Decision: "allow", ElapsedMS: 1000, InputTokens: 100, OutputTokens: 50},
		{Timestamp: now, ToolName: "Bash", Decision: "deny", ElapsedMS: 2000, InputTokens: 200, OutputTokens: 100},
		{Timestamp: now, ToolName: "Write", Decision: "allow", ElapsedMS: 500, InputTokens: 50, OutputTokens: 25},
		{Timestamp: now, ToolName: "Bash", Decision: "fallthrough", ElapsedMS: 100},
	}

	writeEntries(t, path, entries)

	report, _, err := buildReport([]string{path}, ReportOptions{Days: 7})
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Daily) != 1 {
		t.Fatalf("expected 1 daily summary, got %d", len(report.Daily))
	}

	ds := report.Daily[0]
	if ds.Total != 4 {
		t.Fatalf("total = %d, want 4", ds.Total)
	}
	if ds.Allow != 2 {
		t.Fatalf("allow = %d, want 2", ds.Allow)
	}
	if ds.Deny != 1 {
		t.Fatalf("deny = %d, want 1", ds.Deny)
	}
	if ds.Fallthrough != 1 {
		t.Fatalf("fallthrough = %d, want 1", ds.Fallthrough)
	}
	if ds.TotalInputTokens != 350 {
		t.Fatalf("input_tokens = %d, want 350", ds.TotalInputTokens)
	}
	// (allow=2 + deny=1) / total=4 = 0.75
	if ds.AutomationRate != 0.75 {
		t.Fatalf("AutomationRate = %v, want 0.75", ds.AutomationRate)
	}
	if report.AutomationRate != 0.75 {
		t.Fatalf("report.AutomationRate = %v, want 0.75", report.AutomationRate)
	}

	if len(report.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(report.Tools))
	}
	// Bash should be first (most total).
	if report.Tools[0].ToolName != "Bash" {
		t.Fatalf("top tool = %q, want Bash", report.Tools[0].ToolName)
	}
}

func TestBuildReportFiltersOldEntries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	now := time.Now().UTC()
	old := now.AddDate(0, 0, -30)
	entries := []Entry{
		{Timestamp: now, ToolName: "Bash", Decision: "allow", ElapsedMS: 100},
		{Timestamp: old, ToolName: "Bash", Decision: "deny", ElapsedMS: 200},
	}

	writeEntries(t, path, entries)

	report, _, err := buildReport([]string{path}, ReportOptions{Days: 7})
	if err != nil {
		t.Fatal(err)
	}

	total := 0
	for _, ds := range report.Daily {
		total += ds.Total
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1 (old entry should be filtered)", total)
	}
}

func TestPrintReportJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	now := time.Now().UTC()
	writeEntries(t, path, []Entry{
		{Timestamp: now, ToolName: "Bash", Decision: "allow", ElapsedMS: 100},
	})

	var buf bytes.Buffer
	if err := PrintReport(&buf, []string{path}, ReportOptions{Days: 7, AsJSON: true}); err != nil {
		t.Fatal(err)
	}

	var report FullReport
	if err := json.Unmarshal(buf.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if len(report.Daily) != 1 {
		t.Fatalf("expected 1 daily, got %d", len(report.Daily))
	}
}

func TestPrintReportTable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	now := time.Now().UTC()
	// Mix allow/deny/fallthrough(llm) so every top section and every
	// automation-rate branch is exercised in the same rendering.
	writeEntries(t, path, []Entry{
		{Timestamp: now, ToolName: "Bash", Decision: "allow", ElapsedMS: 100},
		{Timestamp: now, ToolName: "Bash", Decision: "deny", ElapsedMS: 100,
			ToolInput: ToolInputFields{Command: "rm -rf /"}},
		{Timestamp: now, ToolName: "Bash", Decision: "fallthrough", FallthroughKind: "llm",
			ToolInput: ToolInputFields{Command: "gh pr list"}, ElapsedMS: 100},
	})

	var buf bytes.Buffer
	if err := PrintReport(&buf, []string{path}, ReportOptions{Days: 7, DetailsTop: 10}); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	for _, want := range []string{
		"ccgate metrics",
		"Bash",
		"Auto%",
		"F.Allow",
		"F.Deny",
		"Automation rate:",
		"Top fallthrough commands",
		"Top deny commands",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in table output; got:\n%s", want, output)
		}
	}
}

// TestBuildReportCredentialFailures validates the new section
// introduced for issue #61: credential_unavailable entries roll up
// by (source, reason) regardless of tool_input, and entries written
// by an older binary (no credential_source field) don't get dropped
// — they roll into a stand-in source so the user can still see they
// happened.
func TestBuildReportCredentialFailures(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	now := time.Now().UTC()
	writeEntries(t, path, []Entry{
		{Timestamp: now, ToolName: "Bash", Decision: "fallthrough",
			FallthroughKind: "credential_unavailable", Reason: "expired",
			CredentialSource: "exec", ElapsedMS: 5},
		{Timestamp: now, ToolName: "Read", Decision: "fallthrough",
			FallthroughKind: "credential_unavailable", Reason: "expired",
			CredentialSource: "exec", ElapsedMS: 5},
		{Timestamp: now, ToolName: "Write", Decision: "fallthrough",
			FallthroughKind: "credential_unavailable", Reason: "provider_auth",
			CredentialSource: "exec", ElapsedMS: 5},
		{Timestamp: now, ToolName: "Read", Decision: "fallthrough",
			FallthroughKind: "credential_unavailable", Reason: "file_missing",
			CredentialSource: "file", ElapsedMS: 5},
		// Older binary: no CredentialSource. Should still appear in
		// the section so the user notices the failure existed.
		{Timestamp: now, ToolName: "Read", Decision: "fallthrough",
			FallthroughKind: "credential_unavailable", Reason: "timeout",
			ElapsedMS: 5},
	})

	report, _, err := buildReport([]string{path}, ReportOptions{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.CredentialFailures) != 4 {
		t.Fatalf("got %d credential failures, want 4: %+v", len(report.CredentialFailures), report.CredentialFailures)
	}
	// Sorted descending by Count, so the (exec, expired) pair
	// (count=2) must come first.
	first := report.CredentialFailures[0]
	if first.Source != "exec" || first.Reason != "expired" || first.Count != 2 {
		t.Fatalf("first row = %+v, want {exec, expired, 2}", first)
	}
	// Older entry without CredentialSource appears under
	// "(unknown)" so it doesn't get silently swallowed.
	var sawUnknown bool
	for _, s := range report.CredentialFailures {
		if s.Source == "(unknown)" && s.Reason == "timeout" && s.Count == 1 {
			sawUnknown = true
		}
	}
	if !sawUnknown {
		t.Fatalf("expected (unknown,timeout,1) row for legacy entry, got: %+v", report.CredentialFailures)
	}

	// Table output renders the new section header.
	var buf bytes.Buffer
	if err := PrintReport(&buf, []string{path}, ReportOptions{Days: 7, DetailsTop: 10}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Credential failures") {
		t.Fatalf("expected 'Credential failures' header in TTY output:\n%s", buf.String())
	}
}

func writeEntries(t *testing.T, path string, entries []Entry) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

// writeRawJSONLines writes pre-constructed JSONL lines without going through
// Entry serialization. Used to simulate entries written by an older binary
// that didn't know about tool_input.
func writeRawJSONLines(t *testing.T, path string, lines []string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}
