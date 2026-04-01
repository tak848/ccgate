package metrics

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

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
	Period string         `json:"period"`
	Daily  []DailySummary `json:"daily"`
	Tools  []ToolSummary  `json:"tools"`
}

// PrintReport reads the metrics file and prints a report to w.
func PrintReport(w io.Writer, path string, opts ReportOptions) error {
	report, err := buildReport(path, opts)
	if err != nil {
		return err
	}

	if opts.AsJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	printTable(w, report)
	return nil
}

func buildReport(path string, opts ReportOptions) (FullReport, error) {
	if opts.Days <= 0 {
		opts.Days = 7
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -opts.Days).Truncate(24 * time.Hour)

	entries, err := readEntries(path, cutoff)
	if err != nil {
		return FullReport{}, err
	}

	return aggregate(entries, opts.Days), nil
}

func readEntries(path string, cutoff time.Time) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
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

func aggregate(entries []Entry, days int) FullReport {
	dailyMap := make(map[string]*DailySummary)
	toolMap := make(map[string]*ToolSummary)

	for _, e := range entries {
		dateKey := e.Timestamp.UTC().Format("2006-01-02")

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

	return FullReport{
		Period: fmt.Sprintf("last %d days", days),
		Daily:  daily,
		Tools:  tools,
	}
}

func printTable(w io.Writer, report FullReport) {
	fmt.Fprintf(w, "ccgate metrics (%s)\n\n", report.Period)

	if len(report.Daily) == 0 {
		fmt.Fprintln(w, "No data.")
		return
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
