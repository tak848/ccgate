package cli

import (
	"path/filepath"
	"testing"
)

func TestParseCommand(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		args    []string
		name    string
		show    string
		project bool
		output  string
		force   bool
		days    int
		json    bool
		details int
	}{
		"bare target":          {args: []string{"claude"}, name: "claude", days: defaultDays, details: defaultDetails},
		"explicit hook":        {args: []string{"codex", "hook"}, name: "codex hook", days: defaultDays, details: defaultDetails},
		"version":              {args: []string{"--version"}, show: "version", days: defaultDays, details: defaultDetails},
		"version false":        {args: []string{"--version=false"}, show: "version", days: defaultDays, details: defaultDetails},
		"root help":            {args: []string{"-h"}, show: "help", days: defaultDays, details: defaultDetails},
		"contextual help":      {args: []string{"--help", "devin", "init"}, name: "devin init", show: "help", days: defaultDays, details: defaultDetails},
		"help false":           {args: []string{"claude", "--help=false"}, name: "claude", show: "help", days: defaultDays, details: defaultDetails},
		"first help wins":      {args: []string{"--help", "--version"}, show: "help", days: defaultDays, details: defaultDetails},
		"first version wins":   {args: []string{"--version", "--help"}, show: "version", days: defaultDays, details: defaultDetails},
		"deprecated init":      {args: []string{"init"}, name: "init", days: defaultDays, details: defaultDetails},
		"deprecated metrics":   {args: []string{"metrics"}, name: "metrics", days: defaultDays, details: defaultDetails},
		"init defaults":        {args: []string{"claude", "init"}, name: "claude init", days: defaultDays, details: defaultDetails},
		"long init flags":      {args: []string{"codex", "init", "--project", "--force", "--output=-"}, name: "codex init", project: true, force: true, output: "-", days: defaultDays, details: defaultDetails},
		"short init flags":     {args: []string{"devin", "init", "-p", "-f", "-o", "-"}, name: "devin init", project: true, force: true, output: "-", days: defaultDays, details: defaultDetails},
		"cluster":              {args: []string{"claude", "init", "-pfo-"}, name: "claude init", project: true, force: true, output: "-", days: defaultDays, details: defaultDetails},
		"mixed case bool":      {args: []string{"codex", "init", "--project=YeS", "--force=NO"}, name: "codex init", project: true, days: defaultDays, details: defaultDetails},
		"bool override":        {args: []string{"claude", "init", "-p", "--project=false"}, name: "claude init", days: defaultDays, details: defaultDetails},
		"relative output":      {args: []string{"claude", "init", "-oconfig.jsonnet"}, name: "claude init", output: "config.jsonnet", days: defaultDays, details: defaultDetails},
		"short output equals":  {args: []string{"claude", "init", "-o=config.jsonnet"}, name: "claude init", output: "=config.jsonnet", days: defaultDays, details: defaultDetails},
		"empty output":         {args: []string{"claude", "init", "--output="}, name: "claude init", output: ".", days: defaultDays, details: defaultDetails},
		"metrics defaults":     {args: []string{"devin", "metrics"}, name: "devin metrics", days: defaultDays, details: defaultDetails},
		"metrics values":       {args: []string{"codex", "metrics", "--days", "2", "--json", "--details=0"}, name: "codex metrics", days: 2, json: true},
		"negative equals":      {args: []string{"claude", "metrics", "--days=-1"}, name: "claude metrics", days: -1, details: defaultDetails},
		"hex int":              {args: []string{"claude", "metrics", "--days=0x10"}, name: "claude metrics", days: 16, details: defaultDetails},
		"repeated int":         {args: []string{"claude", "metrics", "--days=1", "--days=2"}, name: "claude metrics", days: 2, details: defaultDetails},
		"claude metrics alias": {args: []string{"clm", "--json"}, name: "clm", days: defaultDays, json: true, details: defaultDetails},
		"codex metrics alias":  {args: []string{"com", "--days=3"}, name: "com", days: 3, details: defaultDetails},
		"devin metrics alias":  {args: []string{"dvm", "--details=0"}, name: "dvm", days: defaultDays},
		"end flags before cmd": {args: []string{"--", "claude", "init"}, name: "claude init", days: defaultDays, details: defaultDetails},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := parseCommand(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			output := tc.output
			if output != "" && output != "-" {
				output, err = filepath.Abs(output)
				if err != nil {
					t.Fatal(err)
				}
			}
			if got.name() != tc.name || got.show != tc.show || got.project != tc.project || got.output != output || got.force != tc.force || got.days != tc.days || got.json != tc.json || got.details != tc.details {
				t.Errorf("parseCommand(%q) = %+v, want %+v (output %q)", tc.args, got, tc, output)
			}
		})
	}
}

func TestParseCommandRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"empty":                     {},
		"separator only":            {"--"},
		"unknown command":           {"unknown"},
		"unknown target action":     {"claude", "unknown"},
		"extra argument":            {"claude", "init", "extra"},
		"unknown global flag":       {"--unknown"},
		"unknown local flag":        {"claude", "metrics", "--force"},
		"flag before its command":   {"claude", "--json", "metrics"},
		"deprecated flags":          {"init", "--project"},
		"help before error":         {"--help", "--unknown"},
		"version before error":      {"--version", "--unknown"},
		"invalid bool":              {"claude", "init", "--project=t"},
		"short bool equals":         {"claude", "init", "-p=false"},
		"invalid int":               {"clm", "--days=abc"},
		"invalid octal":             {"clm", "--days=08"},
		"int overflow":              {"clm", "--days=999999999999999999999999999999"},
		"missing int":               {"clm", "--days"},
		"separate negative":         {"clm", "--days", "-1"},
		"missing file":              {"claude", "init", "-o"},
		"flag instead of file":      {"claude", "init", "--output", "--help"},
		"help after flag separator": {"claude", "--", "--help"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseCommand(args); err == nil {
				t.Fatalf("parseCommand(%q) succeeded", args)
			}
		})
	}
}
