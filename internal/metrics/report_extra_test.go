package metrics

import (
	"bytes"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHumanInt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},
		{1000, "1,000"},
		{12345, "12,345"},
		{1234567, "1,234,567"},
		{-1, "-1"},
		{-999, "-999"},
		{-1000, "-1,000"},
		{-1234, "-1,234"},
		{math.MaxInt64, "9,223,372,036,854,775,807"},
		{math.MinInt64, "-9,223,372,036,854,775,808"},
	}
	for _, tc := range tests {
		got := humanInt(tc.in)
		if got != tc.want {
			t.Errorf("humanInt(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatToolInputLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   ToolInputFields
		want string
	}{
		{"all empty", ToolInputFields{}, "(no input)"},
		{"command only", ToolInputFields{Command: "gh pr list"}, "gh pr list"},
		{"file_path only", ToolInputFields{FilePath: "/tmp/foo"}, "/tmp/foo"},
		{"path and pattern", ToolInputFields{Path: "internal/", Pattern: "TODO"}, "TODO @ internal/"},
		{"path only", ToolInputFields{Path: "**/*.go"}, "**/*.go"},
		{"pattern only", ToolInputFields{Pattern: "fnord"}, "fnord"},
		{"command wins over file_path", ToolInputFields{Command: "c", FilePath: "fp"}, "c"},
		{"command newline collapsed to space", ToolInputFields{Command: "line1\nline2"}, "line1 line2"},
		{"command tab and carriage return collapsed",
			ToolInputFields{Command: "a\tb\rc"}, "a b c"},
		{"long command truncated at display limit",
			ToolInputFields{Command: strings.Repeat("x", maxDisplayToolInput+10)},
			strings.Repeat("x", maxDisplayToolInput)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := formatToolInputLine(tc.in)
			if got != tc.want {
				t.Errorf("formatToolInputLine(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildReportAutomationRateEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	// Create an empty file (no entries).
	writeEntries(t, path, nil)

	report, _, err := buildReport(path, ReportOptions{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	if report.AutomationRate != 0 {
		t.Errorf("AutomationRate = %v, want 0", report.AutomationRate)
	}
	if len(report.Daily) != 0 {
		t.Errorf("len(Daily) = %d, want 0", len(report.Daily))
	}
	if len(report.FallthroughTop) != 0 {
		t.Errorf("len(FallthroughTop) = %d, want 0", len(report.FallthroughTop))
	}
	if len(report.DenyTop) != 0 {
		t.Errorf("len(DenyTop) = %d, want 0", len(report.DenyTop))
	}
}

func TestBuildReportAutomationRateWithError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	now := time.Now().UTC()
	// 1 allow + 1 error + 1 fallthrough = 3 total; numerator = 1 (allow only).
	writeEntries(t, path, []Entry{
		{Timestamp: now, ToolName: "Bash", Decision: "allow", ElapsedMS: 10},
		{Timestamp: now, ToolName: "Bash", Decision: "error", Error: "boom", ElapsedMS: 20},
		{Timestamp: now, ToolName: "Bash", Decision: "fallthrough", FallthroughKind: "llm", ElapsedMS: 30},
	})
	report, _, err := buildReport(path, ReportOptions{Days: 7})
	if err != nil {
		t.Fatal(err)
	}
	wantRate := 1.0 / 3.0
	if math.Abs(report.AutomationRate-wantRate) > 1e-9 {
		t.Errorf("AutomationRate = %v, want ~%v", report.AutomationRate, wantRate)
	}
	if report.Daily[0].Errors != 1 {
		t.Errorf("Errors = %d, want 1", report.Daily[0].Errors)
	}
}

func TestBuildReportDetailsTopFallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	now := time.Now().UTC()
	// Create 15 distinct llm fallthroughs so the top section would normally have >10 rows.
	var entries []Entry
	for i := range 15 {
		entries = append(entries, Entry{
			Timestamp:       now,
			ToolName:        "Bash",
			Decision:        "fallthrough",
			FallthroughKind: "llm",
			ElapsedMS:       100,
			ToolInput:       ToolInputFields{Command: "cmd" + string(rune('A'+i))},
		})
	}
	writeEntries(t, path, entries)

	cases := []struct {
		name      string
		detailsIn int
		wantLen   int
	}{
		{"negative falls back to default 10", -5, 10},
		{"zero suppresses section", 0, 0},
		{"positive limits to N", 3, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, _, err := buildReport(path, ReportOptions{Days: 7, DetailsTop: tc.detailsIn})
			if err != nil {
				t.Fatal(err)
			}
			if got := len(report.FallthroughTop); got != tc.wantLen {
				t.Errorf("len(FallthroughTop) = %d, want %d", got, tc.wantLen)
			}
		})
	}
}

func TestToolInputTop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	now := time.Now().UTC()

	writeEntries(t, path, []Entry{
		// llm fallthroughs: grouped by ToolInputFields value
		{Timestamp: now, ToolName: "Bash", Decision: "fallthrough", FallthroughKind: "llm",
			ToolInput: ToolInputFields{Command: "gh pr list"}, ElapsedMS: 1},
		{Timestamp: now, ToolName: "Bash", Decision: "fallthrough", FallthroughKind: "llm",
			ToolInput: ToolInputFields{Command: "gh pr list"}, ElapsedMS: 1},
		{Timestamp: now, ToolName: "Bash", Decision: "fallthrough", FallthroughKind: "llm",
			ToolInput: ToolInputFields{Command: "gh pr list"}, ElapsedMS: 1},
		// connective-whitespace variant must be a DIFFERENT group (normalize=no)
		{Timestamp: now, ToolName: "Bash", Decision: "fallthrough", FallthroughKind: "llm",
			ToolInput: ToolInputFields{Command: "gh  pr   list"}, ElapsedMS: 1},
		{Timestamp: now, ToolName: "Write", Decision: "fallthrough", FallthroughKind: "llm",
			ToolInput: ToolInputFields{FilePath: "/tmp/foo"}, ElapsedMS: 1},
		// non-llm fallthroughs should NOT appear in FallthroughTop
		{Timestamp: now, ToolName: "Bash", Decision: "fallthrough", FallthroughKind: "bypass",
			ToolInput: ToolInputFields{Command: "skip-me"}, ElapsedMS: 1},
		{Timestamp: now, ToolName: "Bash", Decision: "fallthrough", FallthroughKind: "user_interaction",
			ToolInput: ToolInputFields{Command: "also-skip"}, ElapsedMS: 1},
		// deny entries
		{Timestamp: now, ToolName: "Bash", Decision: "deny",
			ToolInput: ToolInputFields{Command: "rm -rf /"}, ElapsedMS: 1},
		{Timestamp: now, ToolName: "Bash", Decision: "deny",
			ToolInput: ToolInputFields{Command: "rm -rf /"}, ElapsedMS: 1},
	})
	report, _, err := buildReport(path, ReportOptions{Days: 7, DetailsTop: 10})
	if err != nil {
		t.Fatal(err)
	}

	// FallthroughTop should contain 3 groups: "gh pr list"(3), "gh  pr   list"(1), Write "/tmp/foo"(1).
	// Non-llm fallthroughs must be filtered out.
	if len(report.FallthroughTop) != 3 {
		t.Fatalf("len(FallthroughTop) = %d, want 3. content: %+v",
			len(report.FallthroughTop), report.FallthroughTop)
	}
	// Top entry is the "gh pr list" group with count=3.
	if report.FallthroughTop[0].Count != 3 {
		t.Errorf("top count = %d, want 3", report.FallthroughTop[0].Count)
	}
	if report.FallthroughTop[0].ToolInput.Command != "gh pr list" {
		t.Errorf("top command = %q, want %q",
			report.FallthroughTop[0].ToolInput.Command, "gh pr list")
	}
	// Ensure skipped kinds don't leak in
	for _, s := range report.FallthroughTop {
		if s.ToolInput.Command == "skip-me" || s.ToolInput.Command == "also-skip" {
			t.Errorf("non-llm fallthrough leaked into FallthroughTop: %+v", s)
		}
	}
	// "gh  pr   list" stays a separate group (no whitespace normalization).
	foundConnWS := false
	for _, s := range report.FallthroughTop {
		if s.ToolInput.Command == "gh  pr   list" && s.Count == 1 {
			foundConnWS = true
		}
	}
	if !foundConnWS {
		t.Errorf("whitespace variant was not kept as its own group")
	}

	// DenyTop should have one group with count=2.
	if len(report.DenyTop) != 1 || report.DenyTop[0].Count != 2 {
		t.Fatalf("DenyTop = %+v, want 1 group with count 2", report.DenyTop)
	}
}

func TestToolInputTopLegacyEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	rotatedPath := path + ".1"
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Mimic old binary output: no tool_input field at all.
	legacyLine := `{"ts":"` + now + `","tool":"Bash","decision":"fallthrough","ft_kind":"llm","elapsed_ms":10}`
	writeRawJSONLines(t, path, []string{legacyLine})
	writeRawJSONLines(t, rotatedPath, []string{legacyLine})

	report, _, err := buildReport(path, ReportOptions{Days: 7, DetailsTop: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.FallthroughTop) != 1 {
		t.Fatalf("len(FallthroughTop) = %d, want 1", len(report.FallthroughTop))
	}
	got := report.FallthroughTop[0]
	// Legacy entries should aggregate as zero-value ToolInputFields, and
	// importantly JSON output must keep it as an empty object, not a sentinel.
	if got.ToolInput != (ToolInputFields{}) {
		t.Errorf("ToolInput = %+v, want zero value", got.ToolInput)
	}
	if got.Count != 2 {
		t.Errorf("Count = %d, want 2 (one from each file)", got.Count)
	}
	// Marshal the summary and ensure tool_input shows as {} (not omitted, not sentinel).
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"tool_input":{}`) {
		t.Errorf("marshaled summary = %s, want substring \"tool_input\":{}", string(raw))
	}
	if strings.Contains(string(raw), "(no input)") {
		t.Errorf("marshaled summary must not contain sentinel, got %s", string(raw))
	}
}

func TestPrintReportEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	writeEntries(t, path, nil)

	var buf bytes.Buffer
	if err := PrintReport(&buf, path, ReportOptions{Days: 7, DetailsTop: 10}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "No data.") {
		t.Errorf("expected %q in output, got:\n%s", "No data.", out)
	}
	// Must NOT print the automation rate footer or details when there is no data.
	for _, s := range []string{"Automation rate:", "Top fallthrough commands", "Top deny commands"} {
		if strings.Contains(out, s) {
			t.Errorf("unexpected %q in empty output:\n%s", s, out)
		}
	}
}

func TestPrintReportNoInputDisplay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	now := time.Now().UTC()

	writeEntries(t, path, []Entry{
		// All fields empty → (no input) at display time, {} in JSON.
		{Timestamp: now, ToolName: "Tool", Decision: "fallthrough", FallthroughKind: "llm", ElapsedMS: 5},
	})

	// TTY: should contain (no input)
	var buf bytes.Buffer
	if err := PrintReport(&buf, path, ReportOptions{Days: 7, DetailsTop: 10}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "(no input)") {
		t.Errorf("TTY output should contain (no input):\n%s", buf.String())
	}

	// JSON: should contain "tool_input":{} and NOT (no input)
	var jsonBuf bytes.Buffer
	if err := PrintReport(&jsonBuf, path, ReportOptions{Days: 7, AsJSON: true, DetailsTop: 10}); err != nil {
		t.Fatal(err)
	}
	j := jsonBuf.String()
	if !strings.Contains(j, `"tool_input": {}`) {
		t.Errorf("JSON output should contain tool_input:{}, got:\n%s", j)
	}
	if strings.Contains(j, "(no input)") {
		t.Errorf("JSON output must not contain sentinel, got:\n%s", j)
	}
}

func TestPrintReportMultilineCommandDisplay(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	now := time.Now().UTC()

	writeEntries(t, path, []Entry{
		{Timestamp: now, ToolName: "Bash", Decision: "fallthrough", FallthroughKind: "llm",
			ToolInput: ToolInputFields{Command: "line1\nline2"}, ElapsedMS: 5},
	})

	// TTY: the \n must NOT remain literal (row must stay on one line).
	// Concretely, "line1 line2" appears and "line1\nline2" does not.
	var buf bytes.Buffer
	if err := PrintReport(&buf, path, ReportOptions{Days: 7, DetailsTop: 10}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "line1 line2") {
		t.Errorf("TTY output should contain collapsed form, got:\n%s", out)
	}
	// Inspect the details row only: find the line starting with "  Bash" and
	// ensure it does not contain a literal LF between "line1" and "line2".
	for row := range strings.SplitSeq(out, "\n") {
		if strings.Contains(row, "Bash") && strings.Contains(row, "line1") {
			if !strings.Contains(row, "line1 line2") {
				t.Errorf("details row should collapse newline: %q", row)
			}
		}
	}

	// JSON: command must be preserved verbatim including the literal LF.
	var jsonBuf bytes.Buffer
	if err := PrintReport(&jsonBuf, path, ReportOptions{Days: 7, AsJSON: true, DetailsTop: 10}); err != nil {
		t.Fatal(err)
	}
	var decoded FullReport
	if err := json.Unmarshal(jsonBuf.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.FallthroughTop) != 1 {
		t.Fatalf("len(FallthroughTop) = %d, want 1", len(decoded.FallthroughTop))
	}
	if decoded.FallthroughTop[0].ToolInput.Command != "line1\nline2" {
		t.Errorf("JSON round-trip: Command = %q, want %q",
			decoded.FallthroughTop[0].ToolInput.Command, "line1\nline2")
	}
}

func TestPrintReportColumnAlignment(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	now := time.Now().UTC()

	// Different magnitudes to stress the pre-computed column widths.
	writeEntries(t, path, []Entry{
		{Timestamp: now, ToolName: "Bash", Decision: "allow", ElapsedMS: 100,
			InputTokens: 1234567, OutputTokens: 12345},
		{Timestamp: now.Add(-24 * time.Hour), ToolName: "Bash", Decision: "allow", ElapsedMS: 100,
			InputTokens: 5, OutputTokens: 5},
	})

	var buf bytes.Buffer
	if err := PrintReport(&buf, path, ReportOptions{Days: 7, DetailsTop: 10}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	// Find the header and daily rows; they all should contain only ASCII bytes.
	var header string
	var dataRows []string
	for line := range strings.SplitSeq(out, "\n") {
		if strings.HasPrefix(line, "Date") {
			header = line
			continue
		}
		if len(line) > 0 && line[0] >= '0' && line[0] <= '9' {
			dataRows = append(dataRows, line)
		}
	}
	if header == "" {
		t.Fatalf("header not found in:\n%s", out)
	}
	if len(dataRows) != 2 {
		t.Fatalf("expected 2 data rows, got %d:\n%s", len(dataRows), out)
	}
	// The "/" between in/out tokens is a reliable column anchor.
	anchor := strings.Index(header, "/")
	if anchor < 0 {
		t.Fatalf("header has no '/': %q", header)
	}
	for _, r := range dataRows {
		if idx := strings.Index(r, "/"); idx != anchor {
			t.Errorf("data row '/' column at %d, header at %d\nheader: %q\nrow: %q",
				idx, anchor, header, r)
		}
	}
	// "1,234,567" should appear somewhere with comma grouping.
	if !strings.Contains(out, "1,234,567") {
		t.Errorf("expected grouped number 1,234,567 in output:\n%s", out)
	}
}
