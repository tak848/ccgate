package metrics

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tak848/ccgate/internal/gate"
)

// DefaultReportDays is the default number of days for metrics reports.
const DefaultReportDays = 7

// defaultDetailsTop is the default number of rows shown in the
// "Top fallthrough/deny commands" sections.
const defaultDetailsTop = 10

// maxDisplayToolInput caps the rune length of tool_input displayed in
// the TTY details sections. Aggregation keys use the full raw value;
// this limit only affects what is printed.
const maxDisplayToolInput = 200

// noInputPlaceholder is the TTY-only sentinel rendered for entries whose
// ToolInput is entirely empty. JSON output keeps the empty object instead.
const noInputPlaceholder = "(no input)"

// Only fallthroughs with gate.FallthroughKindLLM are promotable via
// permission rule additions; the other kinds indicate runtime-mode or
// configuration conditions and are excluded from the top section.

// ReportOptions controls how the report is generated.
//
// DetailsTop semantics:
//   - == 0: both fallthrough/deny detail sections are suppressed.
//   - < 0:  falls back to defaultDetailsTop (10) as an internal safety net.
//   - > 0:  the top N rows are shown in each section.
//
// The Go zero value (0) is "suppress", not "use default", which differs
// from how the kong CLI flag defaults to 10. Programmatic callers that
// want the default behavior must set DetailsTop explicitly (e.g. 10).
type ReportOptions struct {
	Days       int
	AsJSON     bool
	DetailsTop int
}

// DailySummary aggregates metrics for a single calendar day.
//
// AutomationRate is (Allow+Deny)/Total. Total counts every entry on the
// day including errors, so error bursts drag the rate down rather than up.
type DailySummary struct {
	Date              string  `json:"date"`
	Total             int     `json:"total"`
	Allow             int     `json:"allow"`
	Deny              int     `json:"deny"`
	Fallthrough       int     `json:"fallthrough"`
	Errors            int     `json:"errors"`
	AutomationRate    float64 `json:"automation_rate"`
	AvgElapsedMS      float64 `json:"avg_elapsed_ms"`
	TotalInputTokens  int64   `json:"total_input_tokens"`
	TotalOutputTokens int64   `json:"total_output_tokens"`
}

// ToolSummary aggregates metrics per tool.
type ToolSummary struct {
	ToolName    string `json:"tool"`
	Total       int    `json:"total"`
	Allow       int    `json:"allow"`
	Deny        int    `json:"deny"`
	Fallthrough int    `json:"fallthrough"`
}

// ToolInputSummary aggregates entries sharing the same tool name and
// structured tool_input. Used for "Top fallthrough commands" / "Top deny
// commands" sections. ToolInput is emitted verbatim (no omitempty) so
// consumers can distinguish "all fields empty" from "a literal (no input)".
type ToolInputSummary struct {
	ToolName  string          `json:"tool"`
	ToolInput ToolInputFields `json:"tool_input"`
	Count     int             `json:"count"`
}

// FullReport is the complete metrics report.
//
// AutomationRate is computed across all entries in the range.
// FallthroughTop is restricted to entries whose FallthroughKind is "llm"
// because only those are promotable by adding permission rules.
type FullReport struct {
	Period         string             `json:"period"`
	DataRange      string             `json:"data_range,omitempty"`
	AutomationRate float64            `json:"automation_rate"`
	Daily          []DailySummary     `json:"daily"`
	Tools          []ToolSummary      `json:"tools"`
	FallthroughTop []ToolInputSummary `json:"fallthrough_top"`
	DenyTop        []ToolInputSummary `json:"deny_top"`
}

// PrintReport reads the metrics file and prints a report to w.
func PrintReport(w io.Writer, path string, opts ReportOptions) error {
	report, cutoff, err := buildReport(path, opts)
	if err != nil {
		return err
	}

	if opts.AsJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	printTable(w, report, cutoff)
	return nil
}

func buildReport(path string, opts ReportOptions) (FullReport, time.Time, error) {
	if opts.Days <= 0 {
		opts.Days = DefaultReportDays
	}
	// Normalize DetailsTop: negative falls back to default, zero/positive kept as-is.
	detailsTop := opts.DetailsTop
	if detailsTop < 0 {
		detailsTop = defaultDetailsTop
	}

	// Use local timezone for day boundaries (cutoff and daily grouping).
	now := time.Now()
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -opts.Days+1)

	entries, err := readEntries(path, cutoff)
	if err != nil {
		return FullReport{}, cutoff, err
	}

	return aggregate(entries, opts.Days, detailsTop), cutoff, nil
}

func readEntries(path string, cutoff time.Time) ([]Entry, error) {
	var entries []Entry

	// Read both current and rotated file.
	for _, p := range []string{path + ".1", path} {
		more, err := readEntriesFromFile(p, cutoff)
		if err != nil {
			return nil, err
		}
		entries = append(entries, more...)
	}

	return entries, nil
}

func readEntriesFromFile(path string, cutoff time.Time) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			slog.Warn("metrics: skipping malformed entry", "error", err)
			continue
		}
		if e.Timestamp.Before(cutoff) {
			continue
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning %s: %w", path, err)
	}
	return entries, nil
}

type aggKey struct {
	Tool  string
	Input ToolInputFields
}

func aggregate(entries []Entry, days int, detailsTop int) FullReport {
	dailyMap := make(map[string]*DailySummary)
	toolMap := make(map[string]*ToolSummary)
	fallthroughMap := make(map[aggKey]*ToolInputSummary)
	denyMap := make(map[aggKey]*ToolInputSummary)

	var minTS, maxTS time.Time
	loc := time.Now().Location()
	var totalAll, totalAllowDeny int
	for _, e := range entries {
		if minTS.IsZero() || e.Timestamp.Before(minTS) {
			minTS = e.Timestamp
		}
		if maxTS.IsZero() || e.Timestamp.After(maxTS) {
			maxTS = e.Timestamp
		}
		dateKey := e.Timestamp.In(loc).Format("2006-01-02")

		ds, ok := dailyMap[dateKey]
		if !ok {
			ds = &DailySummary{Date: dateKey}
			dailyMap[dateKey] = ds
		}
		ds.Total++
		totalAll++
		ds.TotalInputTokens += e.InputTokens
		ds.TotalOutputTokens += e.OutputTokens
		ds.AvgElapsedMS += float64(e.ElapsedMS)

		switch e.Decision {
		case "allow":
			ds.Allow++
			totalAllowDeny++
		case "deny":
			ds.Deny++
			totalAllowDeny++
		case "fallthrough":
			ds.Fallthrough++
		}
		if e.Error != "" {
			ds.Errors++
		}

		ts, ok := toolMap[e.ToolName]
		if !ok {
			ts = &ToolSummary{ToolName: e.ToolName}
			toolMap[e.ToolName] = ts
		}
		ts.Total++
		switch e.Decision {
		case "allow":
			ts.Allow++
		case "deny":
			ts.Deny++
		case "fallthrough":
			ts.Fallthrough++
		}

		// Top fallthrough commands: only the LLM-driven ones, since those
		// are the fallthroughs that a new permission rule could eliminate.
		if e.Decision == "fallthrough" && e.FallthroughKind == gate.FallthroughKindLLM {
			bumpToolInputSummary(fallthroughMap, e)
		}
		if e.Decision == "deny" {
			bumpToolInputSummary(denyMap, e)
		}
	}

	daily := make([]DailySummary, 0, len(dailyMap))
	for _, ds := range dailyMap {
		if ds.Total > 0 {
			ds.AvgElapsedMS = ds.AvgElapsedMS / float64(ds.Total)
			ds.AutomationRate = float64(ds.Allow+ds.Deny) / float64(ds.Total)
		}
		daily = append(daily, *ds)
	}
	sort.Slice(daily, func(i, j int) bool {
		return daily[i].Date > daily[j].Date
	})

	tools := make([]ToolSummary, 0, len(toolMap))
	for _, ts := range toolMap {
		tools = append(tools, *ts)
	}
	sort.Slice(tools, func(i, j int) bool {
		if tools[i].Total != tools[j].Total {
			return tools[i].Total > tools[j].Total
		}
		return tools[i].ToolName < tools[j].ToolName
	})

	fallthroughTop := rankToolInputSummaries(fallthroughMap, detailsTop)
	denyTop := rankToolInputSummaries(denyMap, detailsTop)

	var overallRate float64
	if totalAll > 0 {
		overallRate = float64(totalAllowDeny) / float64(totalAll)
	}

	var dataRange string
	if !minTS.IsZero() && !maxTS.IsZero() {
		dataRange = fmt.Sprintf("%s ~ %s",
			minTS.In(loc).Format("2006-01-02"),
			maxTS.In(loc).Format("2006-01-02"))
	}

	return FullReport{
		Period:         fmt.Sprintf("last %d days", days),
		DataRange:      dataRange,
		AutomationRate: overallRate,
		Daily:          daily,
		Tools:          tools,
		FallthroughTop: fallthroughTop,
		DenyTop:        denyTop,
	}
}

func bumpToolInputSummary(m map[aggKey]*ToolInputSummary, e Entry) {
	k := aggKey{Tool: e.ToolName, Input: e.ToolInput}
	s, ok := m[k]
	if !ok {
		s = &ToolInputSummary{ToolName: e.ToolName, ToolInput: e.ToolInput}
		m[k] = s
	}
	s.Count++
}

// rankToolInputSummaries sorts with a deterministic tie-breaker so the
// output is stable across runs and CI, independent of map iteration order.
func rankToolInputSummaries(m map[aggKey]*ToolInputSummary, top int) []ToolInputSummary {
	if top <= 0 {
		return []ToolInputSummary{}
	}
	out := make([]ToolInputSummary, 0, len(m))
	for _, s := range m {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].ToolName != out[j].ToolName {
			return out[i].ToolName < out[j].ToolName
		}
		if out[i].ToolInput.Command != out[j].ToolInput.Command {
			return out[i].ToolInput.Command < out[j].ToolInput.Command
		}
		if out[i].ToolInput.FilePath != out[j].ToolInput.FilePath {
			return out[i].ToolInput.FilePath < out[j].ToolInput.FilePath
		}
		if out[i].ToolInput.Path != out[j].ToolInput.Path {
			return out[i].ToolInput.Path < out[j].ToolInput.Path
		}
		return out[i].ToolInput.Pattern < out[j].ToolInput.Pattern
	})
	if len(out) > top {
		out = out[:top]
	}
	return out
}

// humanInt formats an integer with thousands separators.
// Works correctly for math.MinInt64 by operating on the string representation
// rather than negating the numeric value.
func humanInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	negative := strings.HasPrefix(s, "-")
	if negative {
		s = s[1:]
	}
	if len(s) <= 3 {
		if negative {
			return "-" + s
		}
		return s
	}
	// Insert commas from the right in groups of 3.
	var b strings.Builder
	firstGroup := len(s) % 3
	if firstGroup == 0 {
		firstGroup = 3
	}
	b.WriteString(s[:firstGroup])
	for i := firstGroup; i < len(s); i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	if negative {
		return "-" + b.String()
	}
	return b.String()
}

// formatToolInputLine renders a ToolInputFields value as a single line of
// text for the details section. It is display-only: it collapses newlines
// and tabs to spaces so the table never breaks a row, and never mutates
// the stored value (which stays verbatim for JSON output).
func formatToolInputLine(tif ToolInputFields) string {
	var s string
	switch {
	case tif.Command != "":
		s = tif.Command
	case tif.FilePath != "":
		s = tif.FilePath
	case tif.Path != "" && tif.Pattern != "":
		s = tif.Pattern + " @ " + tif.Path
	case tif.Path != "":
		s = tif.Path
	case tif.Pattern != "":
		s = tif.Pattern
	default:
		return noInputPlaceholder
	}
	// Collapse line-breaking whitespace for table rendering only.
	s = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
	return truncateRunes(s, maxDisplayToolInput)
}

// columnWidths tracks the formatted (humanInt) width of each numeric column
// so the table can be aligned without any column running over its header.
type columnWidths struct {
	date, total, allow, deny, fall, err, avg, inTok, outTok int
}

func computeColumnWidths(daily []DailySummary) columnWidths {
	cw := columnWidths{
		date:   len("Date"),
		total:  len("Total"),
		allow:  len("Allow"),
		deny:   len("Deny"),
		fall:   len("Fall"),
		err:    len("Err"),
		avg:    len("Avg(ms)"),
		inTok:  len("Tokens(in"),
		outTok: len("out)"),
	}
	for _, ds := range daily {
		cw.date = maxInt(cw.date, len(ds.Date))
		cw.total = maxInt(cw.total, len(humanInt(int64(ds.Total))))
		cw.allow = maxInt(cw.allow, len(humanInt(int64(ds.Allow))))
		cw.deny = maxInt(cw.deny, len(humanInt(int64(ds.Deny))))
		cw.fall = maxInt(cw.fall, len(humanInt(int64(ds.Fallthrough))))
		cw.err = maxInt(cw.err, len(humanInt(int64(ds.Errors))))
		cw.avg = maxInt(cw.avg, len(humanInt(int64(math.Round(ds.AvgElapsedMS)))))
		cw.inTok = maxInt(cw.inTok, len(humanInt(ds.TotalInputTokens)))
		cw.outTok = maxInt(cw.outTok, len(humanInt(ds.TotalOutputTokens)))
	}
	return cw
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func printTable(w io.Writer, report FullReport, cutoff time.Time) {
	fmt.Fprintf(w, "ccgate metrics (%s)\n", report.Period)
	if report.DataRange != "" {
		fmt.Fprintf(w, "Data range: %s\n", report.DataRange)
	}
	fmt.Fprintln(w)

	if len(report.Daily) == 0 {
		fmt.Fprintln(w, "No data.")
		return
	}

	// Warn if the earliest data is newer than cutoff (data may be incomplete due to rotation).
	if len(report.Daily) > 0 {
		oldest := report.Daily[len(report.Daily)-1].Date
		cutoffDate := cutoff.Format("2006-01-02")
		if oldest > cutoffDate {
			fmt.Fprintln(w, "Note: data limited by file rotation")
			fmt.Fprintln(w)
		}
	}

	cw := computeColumnWidths(report.Daily)

	const autoColWidth = 6 // e.g. " 88.4%"
	fmt.Fprintf(w, "%-*s %*s %*s %*s %*s %*s %*s %*s %*s / %*s\n",
		cw.date, "Date",
		cw.total, "Total",
		cw.allow, "Allow",
		cw.deny, "Deny",
		cw.fall, "Fall",
		cw.err, "Err",
		autoColWidth, "Auto%",
		cw.avg, "Avg(ms)",
		cw.inTok, "Tokens(in",
		cw.outTok, "out)")
	for _, ds := range report.Daily {
		fmt.Fprintf(w, "%-*s %*s %*s %*s %*s %*s %*s %*s %*s / %*s\n",
			cw.date, ds.Date,
			cw.total, humanInt(int64(ds.Total)),
			cw.allow, humanInt(int64(ds.Allow)),
			cw.deny, humanInt(int64(ds.Deny)),
			cw.fall, humanInt(int64(ds.Fallthrough)),
			cw.err, humanInt(int64(ds.Errors)),
			autoColWidth, fmt.Sprintf("%.1f%%", ds.AutomationRate*100),
			cw.avg, humanInt(int64(math.Round(ds.AvgElapsedMS))),
			cw.inTok, humanInt(ds.TotalInputTokens),
			cw.outTok, humanInt(ds.TotalOutputTokens))
	}

	fmt.Fprintf(w, "\nAutomation rate: %.1f%% ((allow+deny) / total)\n", report.AutomationRate*100)

	if len(report.Tools) > 0 {
		fmt.Fprintln(w)
		var parts []string
		for _, ts := range report.Tools {
			parts = append(parts, fmt.Sprintf("%s:%s", ts.ToolName, humanInt(int64(ts.Total))))
		}
		fmt.Fprintf(w, "Top tools: %s\n", strings.Join(parts, " "))
	}

	printTopSection(w, "Top fallthrough commands", report.FallthroughTop)
	printTopSection(w, "Top deny commands", report.DenyTop)
}

func printTopSection(w io.Writer, title string, summaries []ToolInputSummary) {
	if len(summaries) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%s (top %d):\n", title, len(summaries))
	toolWidth := 0
	countWidth := 0
	formatted := make([]string, len(summaries))
	for i, s := range summaries {
		toolWidth = maxInt(toolWidth, len(s.ToolName))
		countWidth = maxInt(countWidth, len(humanInt(int64(s.Count))))
		formatted[i] = formatToolInputLine(s.ToolInput)
	}
	for i, s := range summaries {
		fmt.Fprintf(w, "  %-*s  %*s  %s\n",
			toolWidth, s.ToolName,
			countWidth, humanInt(int64(s.Count)),
			formatted[i])
	}
}
