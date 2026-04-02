package metrics

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"
)

// DefaultReportDays is the default number of days for metrics reports.
const DefaultReportDays = 7

// ReportOptions controls how the report is generated.
type ReportOptions struct {
	Days   int
	AsJSON bool
}

// DailySummary aggregates metrics for a single calendar day.
type DailySummary struct {
	Date              string  `json:"date"`
	Total             int     `json:"total"`
	Allow             int     `json:"allow"`
	Deny              int     `json:"deny"`
	Fallthrough       int     `json:"fallthrough"`
	Errors            int     `json:"errors"`
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

// FullReport is the complete metrics report.
type FullReport struct {
	Period    string         `json:"period"`
	DataRange string         `json:"data_range,omitempty"`
	Daily     []DailySummary `json:"daily"`
	Tools     []ToolSummary  `json:"tools"`
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

	// Use local timezone for day boundaries (cutoff and daily grouping).
	now := time.Now()
	cutoff := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -opts.Days+1)

	entries, err := readEntries(path, cutoff)
	if err != nil {
		return FullReport{}, cutoff, err
	}

	return aggregate(entries, opts.Days, cutoff), cutoff, nil
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

func aggregate(entries []Entry, days int, cutoff time.Time) FullReport {
	dailyMap := make(map[string]*DailySummary)
	toolMap := make(map[string]*ToolSummary)

	var minTS, maxTS time.Time
	loc := time.Now().Location()
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
		ds.TotalInputTokens += e.InputTokens
		ds.TotalOutputTokens += e.OutputTokens
		ds.AvgElapsedMS += float64(e.ElapsedMS)

		switch e.Decision {
		case "allow":
			ds.Allow++
		case "deny":
			ds.Deny++
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
	}

	daily := make([]DailySummary, 0, len(dailyMap))
	for _, ds := range dailyMap {
		if ds.Total > 0 {
			ds.AvgElapsedMS = ds.AvgElapsedMS / float64(ds.Total)
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
		return tools[i].Total > tools[j].Total
	})

	var dataRange string
	if !minTS.IsZero() && !maxTS.IsZero() {
		dataRange = fmt.Sprintf("%s ~ %s",
			minTS.In(loc).Format("2006-01-02"),
			maxTS.In(loc).Format("2006-01-02"))
	}

	return FullReport{
		Period:    fmt.Sprintf("last %d days", days),
		DataRange: dataRange,
		Daily:     daily,
		Tools:     tools,
	}
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

	fmt.Fprintf(w, "%-12s %6s %6s %6s %6s %5s %9s %16s\n",
		"Date", "Total", "Allow", "Deny", "Fall", "Err", "Avg(ms)", "Tokens(in/out)")
	for _, ds := range report.Daily {
		fmt.Fprintf(w, "%-12s %6d %6d %6d %6d %5d %9.0f %7d / %-7d\n",
			ds.Date, ds.Total, ds.Allow, ds.Deny, ds.Fallthrough, ds.Errors,
			ds.AvgElapsedMS, ds.TotalInputTokens, ds.TotalOutputTokens)
	}

	if len(report.Tools) > 0 {
		fmt.Fprintln(w)
		var parts []string
		for _, ts := range report.Tools {
			parts = append(parts, fmt.Sprintf("%s:%d", ts.ToolName, ts.Total))
		}
		fmt.Fprintf(w, "Top tools: %s\n", strings.Join(parts, " "))
	}
}
