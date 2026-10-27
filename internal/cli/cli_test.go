package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jsonnet "github.com/google/go-jsonnet"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/metrics"
)

type unreadableInput struct{ t *testing.T }

func (r unreadableInput) Read([]byte) (int, error) {
	r.t.Error("non-hook command read stdin")
	return 0, io.EOF
}

func isolateCLI(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Chdir(home)
	return home
}

func TestRunHelpVersionAndErrors(t *testing.T) {
	home := isolateCLI(t)
	cases := map[string]struct {
		args []string
		code int
	}{
		"help":             {args: []string{"--help"}},
		"context help":     {args: []string{"--help", "claude", "init"}},
		"target help":      {args: []string{"codex", "--help"}},
		"hook help":        {args: []string{"devin", "hook", "--help"}},
		"metrics help":     {args: []string{"claude", "metrics", "--help"}},
		"alias help":       {args: []string{"clm", "--help"}},
		"version":          {args: []string{"--version"}},
		"local version":    {args: []string{"devin", "--version"}},
		"invalid command":  {args: []string{"unknown"}, code: 2},
		"invalid flag":     {args: []string{"--unknown"}, code: 2},
		"help with error":  {args: []string{"--help", "--unknown"}, code: 2},
		"missing value":    {args: []string{"clm", "--days"}, code: 2},
		"invalid value":    {args: []string{"com", "--days=invalid"}, code: 2},
		"deprecated init":  {args: []string{"init"}, code: 2},
		"deprecated stats": {args: []string{"metrics"}, code: 2},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			version := "test-" + name
			if code := Run(version, tc.args, unreadableInput{t}, &stdout, &stderr); code != tc.code {
				t.Fatalf("exit = %d, want %d; stderr: %s", code, tc.code, &stderr)
			}
			if tc.code == 0 {
				if stdout.Len() == 0 || stderr.Len() != 0 {
					t.Fatalf("stdout = %q, stderr = %q", &stdout, &stderr)
				}
				if strings.Contains(name, "version") && stdout.String() != version+"\n" {
					t.Fatalf("version output = %q, want %q", &stdout, version+"\n")
				}
			} else if stdout.Len() != 0 || stderr.Len() == 0 {
				t.Fatalf("stdout = %q, stderr = %q", &stdout, &stderr)
			}
		})
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("help/version/errors created files: %v", entries)
	}
}

func evaluateInit(t *testing.T, source string) map[string]any {
	t.Helper()
	out, err := jsonnet.MakeVM().EvaluateAnonymousSnippet("init.jsonnet", source)
	if err != nil {
		t.Fatalf("evaluate init output: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRunInit(t *testing.T) {
	isolateCLI(t)
	cases := map[string]struct{ target string }{
		"claude": {target: "claude"},
		"codex":  {target: "codex"},
		"devin":  {target: "devin"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run("test", []string{tc.target, "init"}, unreadableInput{t}, &stdout, &stderr); code != 0 {
				t.Fatalf("init exit = %d: %s", code, &stderr)
			}
			global := evaluateInit(t, stdout.String())
			if _, ok := global["provider"].(map[string]any); !ok {
				t.Fatalf("global init has no provider: %v", global)
			}
			output := filepath.Join(t.TempDir(), "nested", "config.jsonnet")
			stdout.Reset()
			stderr.Reset()
			args := []string{tc.target, "init", "-p", "-o", output}
			if code := Run("test", args, unreadableInput{t}, &stdout, &stderr); code != 0 {
				t.Fatalf("project init exit = %d: %s", code, &stderr)
			}
			if stdout.Len() != 0 {
				t.Fatalf("init with output wrote stdout: %q", &stdout)
			}
			project, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := evaluateInit(t, string(project))["provider"]; ok {
				t.Fatal("project init unexpectedly replaced the global provider")
			}
			const existing = "keep this file\n"
			if err := os.WriteFile(output, []byte(existing), 0o600); err != nil {
				t.Fatal(err)
			}
			stderr.Reset()
			if code := Run("test", args, unreadableInput{t}, &stdout, &stderr); code != 1 {
				t.Fatalf("existing file exit = %d, want 1", code)
			}
			kept, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if string(kept) != existing {
				t.Fatalf("existing file changed: %q", kept)
			}
			stderr.Reset()
			if code := Run("test", append(args, "-f"), unreadableInput{t}, &stdout, &stderr); code != 0 {
				t.Fatalf("forced init exit = %d: %s", code, &stderr)
			}
			forced, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(forced, project) {
				t.Fatal("forced init did not restore the selected project template")
			}
		})
	}
}

func TestRunMetricsTargetsAndAliases(t *testing.T) {
	isolateCLI(t)
	const olderAge = 48 * time.Hour
	for _, target := range []string{"claude", "codex", "devin"} {
		dir := config.StateDir(target)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		var data bytes.Buffer
		encoder := json.NewEncoder(&data)
		for _, timestamp := range []time.Time{time.Now().UTC(), time.Now().UTC().Add(-olderAge)} {
			entry := metrics.Entry{Timestamp: timestamp, ToolName: target, Decision: "fallthrough", FallthroughKind: "llm", ToolInput: metrics.ToolInputFields{Command: target}}
			if err := encoder.Encode(entry); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "metrics.jsonl"), data.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]struct {
		args   []string
		target string
		total  int
	}{
		"claude":      {args: []string{"claude", "metrics", "--json"}, target: "claude", total: 2},
		"codex":       {args: []string{"codex", "metrics", "--json"}, target: "codex", total: 2},
		"devin":       {args: []string{"devin", "metrics", "--json"}, target: "devin", total: 2},
		"clm":         {args: []string{"clm", "--json"}, target: "claude", total: 2},
		"com":         {args: []string{"com", "--json"}, target: "codex", total: 2},
		"dvm":         {args: []string{"dvm", "--json"}, target: "devin", total: 2},
		"days filter": {args: []string{"clm", "--days=1", "--details=0", "--json"}, target: "claude", total: 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run("test", tc.args, unreadableInput{t}, &stdout, &stderr); code != 0 {
				t.Fatalf("metrics exit = %d: %s", code, &stderr)
			}
			var report metrics.FullReport
			if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Tools) != 1 || report.Tools[0].ToolName != tc.target || report.Tools[0].Total != tc.total {
				t.Fatalf("tools = %+v, want %s with %d entries", report.Tools, tc.target, tc.total)
			}
		})
	}
}

type failingOutput struct{ err error }

func (w failingOutput) Write([]byte) (int, error) { return 0, w.err }

func TestRunOutputFailure(t *testing.T) {
	cases := map[string][]string{"help": {"--help"}, "version": {"--version"}}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			failure := errors.New("output unavailable")
			var stderr bytes.Buffer
			if code := Run("test", args, unreadableInput{t}, failingOutput{failure}, &stderr); code != 1 {
				t.Fatalf("exit = %d, want 1; stderr: %s", code, &stderr)
			}
		})
	}
}

func Example_parseCommand() {
	c, err := parseCommand([]string{"com", "--days=2", "--json"})
	fmt.Println(c.target(), c.action(), c.days, c.json, err)
	// Output: codex metrics 2 true <nil>
}
