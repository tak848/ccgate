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
	tests := map[string]struct {
		in   int64
		want string
	}{
		"zero":                {in: 0, want: "0"},
		"single digit":        {in: 1, want: "1"},
		"max 3-digit":         {in: 999, want: "999"},
		"four digits":         {in: 1000, want: "1,000"},
		"five digits":         {in: 12345, want: "12,345"},
		"seven digits":        {in: 1234567, want: "1,234,567"},
		"negative single":     {in: -1, want: "-1"},
		"negative 3-digit":    {in: -999, want: "-999"},
		"negative four":       {in: -1000, want: "-1,000"},
		"negative four alt":   {in: -1234, want: "-1,234"},
		"int64 max":           {in: math.MaxInt64, want: "9,223,372,036,854,775,807"},
		"int64 min overflows": {in: math.MinInt64, want: "-9,223,372,036,854,775,808"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := humanInt(tc.in)
			if got != tc.want {
				t.Errorf("humanInt(%d) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatToolInputLine(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		in   ToolInputFields
		want string
	}{
		"all empty":                   {in: ToolInputFields{}, want: "(no input)"},
		"command only":                {in: ToolInputFields{Command: "gh pr list"}, want: "gh pr list"},
		"file_path only":              {in: ToolInputFields{FilePath: "/tmp/foo"}, want: "/tmp/foo"},
		"path and pattern":            {in: ToolInputFields{Path: "internal/", Pattern: "TODO"}, want: "TODO @ internal/"},
		"path only":                   {in: ToolInputFields{Path: "**/*.go"}, want: "**/*.go"},
		"pattern only":                {in: ToolInputFields{Pattern: "fnord"}, want: "fnord"},
		"command wins over file_path": {in: ToolInputFields{Command: "c", FilePath: "fp"}, want: "c"},
		"command newline collapsed to space": {
			in:   ToolInputFields{Command: "line1\nline2"},
			want: "line1 line2",
		},
		"command tab and carriage return collapsed": {
			in:   ToolInputFields{Command: "a\tb\rc"},
			want: "a b c",
		},
		"long command truncated at display limit": {
			in:   ToolInputFields{Command: strings.Repeat("x", maxDisplayToolInput+10)},
			want: strings.Repeat("x", maxDisplayToolInput),
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
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

	cases := map[string]struct {
		detailsIn int
		wantLen   int
	}{
		"negative falls back to default 10": {detailsIn: -5, wantLen: 10},
		"zero suppresses section":           {detailsIn: 0, wantLen: 0},
		"positive limits to N":              {detailsIn: 3, wantLen: 3},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
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

	// DenyTop: one group "rm -rf /" aggregated from 2 entries.
	if len(report.DenyTop) != 1 || report.DenyTop[0].Count != 2 {
		t.Fatalf("DenyTop = %+v, want 1 group with count 2", report.DenyTop)
	}

	// DetailsTop=2 must slice the ranked list to the top 2 entries.
	rep2, _, err := buildReport(path, ReportOptions{Days: 7, DetailsTop: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep2.FallthroughTop) != 2 {
		t.Errorf("DetailsTop=2: len(FallthroughTop) = %d, want 2", len(rep2.FallthroughTop))
	}
	if len(rep2.DenyTop) != 1 {
		// only one deny group exists overall, so DetailsTop=2 still gives 1.
		t.Errorf("DetailsTop=2: len(DenyTop) = %d, want 1", len(rep2.DenyTop))
	}
}

// TestToolInputTopTieBreaker pins down the full tie-breaker ordering
// (count desc → tool asc → command asc → file_path asc → path asc →
// pattern asc). All entries below share count=1, so ordering is
// determined purely by the later keys.
func TestToolInputTopTieBreaker(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	now := time.Now().UTC()

	// Construct entries across every tie-breaker key. All same count=1,
	// all Decision="deny" so the LLM filter is irrelevant. Expected order
	// reflects empty-string sorting before any non-empty value: entries
	// where an earlier key is empty come first, so Pattern-only rows
	// precede Command-only rows (Command:"" < Command:"a").
	entries := []Entry{
		{Timestamp: now, ToolName: "Bash", Decision: "deny", ElapsedMS: 1, ToolInput: ToolInputFields{Command: "a"}},
		{Timestamp: now, ToolName: "Bash", Decision: "deny", ElapsedMS: 1, ToolInput: ToolInputFields{Command: "b"}},
		{Timestamp: now, ToolName: "Bash", Decision: "deny", ElapsedMS: 1, ToolInput: ToolInputFields{FilePath: "a"}},
		{Timestamp: now, ToolName: "Bash", Decision: "deny", ElapsedMS: 1, ToolInput: ToolInputFields{FilePath: "b"}},
		{Timestamp: now, ToolName: "Bash", Decision: "deny", ElapsedMS: 1, ToolInput: ToolInputFields{Path: "a"}},
		// {Path:"b"} alone exercises the Path-only tie-breaker branch so that
		// removing it from the comparator cannot silently pass this test.
		{Timestamp: now, ToolName: "Bash", Decision: "deny", ElapsedMS: 1, ToolInput: ToolInputFields{Path: "b"}},
		{Timestamp: now, ToolName: "Bash", Decision: "deny", ElapsedMS: 1, ToolInput: ToolInputFields{Path: "a", Pattern: "a"}},
		{Timestamp: now, ToolName: "Bash", Decision: "deny", ElapsedMS: 1, ToolInput: ToolInputFields{Pattern: "a"}},
		{Timestamp: now, ToolName: "Bash", Decision: "deny", ElapsedMS: 1, ToolInput: ToolInputFields{Pattern: "b"}},
		{Timestamp: now, ToolName: "Write", Decision: "deny", ElapsedMS: 1, ToolInput: ToolInputFields{Command: "a"}},
	}
	writeEntries(t, path, entries)

	report, _, err := buildReport(path, ReportOptions{Days: 7, DetailsTop: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.DenyTop) != len(entries) {
		t.Fatalf("len(DenyTop) = %d, want %d; content: %+v",
			len(report.DenyTop), len(entries), report.DenyTop)
	}

	// Sort order: tool asc, then by command / file_path / path / pattern in
	// ascending order, where "" (empty) sorts before any non-empty value.
	want := []ToolInputFields{
		{Pattern: "a"},            // command="" file_path="" path=""  pattern="a"
		{Pattern: "b"},            // command="" file_path="" path=""  pattern="b"
		{Path: "a"},               // command="" file_path="" path="a" pattern=""
		{Path: "a", Pattern: "a"}, // command="" file_path="" path="a" pattern="a"
		{Path: "b"},               // command="" file_path="" path="b" pattern=""  (exercises path-only branch)
		{FilePath: "a"},           // command="" file_path="a"
		{FilePath: "b"},           // command="" file_path="b"
		{Command: "a"},            // command="a" (Bash)
		{Command: "b"},            // command="b" (Bash)
		{Command: "a"},            // Write (tool asc: Bash < Write)
	}
	wantTools := []string{"Bash", "Bash", "Bash", "Bash", "Bash", "Bash", "Bash", "Bash", "Bash", "Write"}
	for i, s := range report.DenyTop {
		if s.ToolName != wantTools[i] || s.ToolInput != want[i] {
			t.Errorf("DenyTop[%d] = (%s, %+v), want (%s, %+v)",
				i, s.ToolName, s.ToolInput, wantTools[i], want[i])
		}
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
	// ASCII-only assertion: every row must be plain ASCII so byte indices
	// equal display columns. If this ever needs to cover non-ASCII output,
	// switch to rune-width measurement instead.
	for i, r := range append([]string{header}, dataRows...) {
		for j := 0; j < len(r); j++ {
			if r[j] >= 0x80 {
				t.Fatalf("row %d contains non-ASCII byte at offset %d: %q", i, j, r)
			}
		}
	}

	// All right-aligned columns share the same width between header and
	// data rows, so the last character of each header label and the last
	// character of the column value must land at the same byte offset, and
	// the column separator space sits at that offset + 1. Verifying this
	// catches any regression in the pre-computed column widths.
	// "Tokens(in" is the right-aligned header for the input-tokens column
	// (directly before the literal "/"). Including it here catches regressions
	// where that column alone is flipped to %-*s (left-align) and values are
	// padded on the wrong side.
	rightAlignedLabels := []string{"Total", "Allow", "Deny", "Fall", "Err", "Auto%", "Avg(ms)", "Tokens(in"}
	for _, label := range rightAlignedLabels {
		anchor := strings.Index(header, label)
		if anchor < 0 {
			t.Fatalf("header missing label %q: %q", label, header)
		}
		endOffset := anchor + len(label) - 1
		for _, r := range dataRows {
			if endOffset+1 >= len(r) {
				t.Errorf("label %q: row shorter than header anchor; end=%d row len=%d\nrow: %q",
					label, endOffset, len(r), r)
				continue
			}
			if r[endOffset] == ' ' {
				t.Errorf("label %q: expected value char at col %d, got space. row: %q",
					label, endOffset, r)
			}
			if r[endOffset+1] != ' ' {
				t.Errorf("label %q: expected column separator space at col %d, got %q. row: %q",
					label, endOffset+1, r[endOffset+1], r)
			}
		}
	}
	// The literal `/` separator between in/out token columns must sit at
	// exactly the same byte offset in header and every data row.
	slashAnchor := strings.Index(header, "/")
	if slashAnchor < 0 {
		t.Fatalf("header has no '/': %q", header)
	}
	for _, r := range dataRows {
		if idx := strings.Index(r, "/"); idx != slashAnchor {
			t.Errorf("data row '/' column at %d, header at %d\nheader: %q\nrow: %q",
				idx, slashAnchor, header, r)
		}
	}
	// The final "out)" column is right-aligned with cw.outTok, so the line
	// ends at the exact same byte offset for header and every data row.
	// Comparing total row length catches any cw.outTok desync that the
	// `/` anchor alone would miss (it only locks columns up to `/`).
	for _, r := range dataRows {
		if len(r) != len(header) {
			t.Errorf("row length = %d, header length = %d (final column desync)\nheader: %q\nrow: %q",
				len(r), len(header), header, r)
		}
	}

	// Comma-grouped big number must appear verbatim.
	if !strings.Contains(out, "1,234,567") {
		t.Errorf("expected grouped number 1,234,567 in output:\n%s", out)
	}
}
