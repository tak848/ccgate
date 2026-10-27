// Package cli implements the ccgate command line.
// Bare ccgate remains the pipe-driven Claude Code hook entry point.
package cli

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"github.com/tak848/ccgate/internal/cmd/claude"
	"github.com/tak848/ccgate/internal/cmd/codex"
	"github.com/tak848/ccgate/internal/cmd/devin"
)

// Run dispatches args using the supplied streams and returns the process exit
// code. Only main exits the process, including for help and parse errors.
func Run(version string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	claude.SetBuildVersion(version)
	if len(args) == 0 {
		if isTerminal(stdin) {
			printUsage(stderr, version)
			return 0
		}
		return claude.Run(stdin, stdout)
	}
	c, err := parseCommand(args)
	if err != nil {
		fmt.Fprintf(stderr, "ccgate: error: %v\n", err)
		return 2
	}
	switch c.show {
	case "help":
		if err := writeHelp(stdout, c); err != nil {
			fmt.Fprintf(stderr, "ccgate: %v\n", err)
			return 1
		}
		return 0
	case "version":
		if _, err := fmt.Fprintln(stdout, version); err != nil {
			fmt.Fprintf(stderr, "ccgate: write version: %v\n", err)
			return 1
		}
		return 0
	}
	return dispatch(c, version, stdin, stdout, stderr)
}

func dispatch(c command, version string, stdin io.Reader, stdout, stderr io.Writer) int {
	switch c.name() {
	case "claude", "claude hook":
		if isTerminal(stdin) {
			printUsage(stderr, version)
			return 0
		}
		return claude.Run(stdin, stdout)
	case "claude init":
		return claude.Init(stdout, stderr, claude.InitOptions{
			Project: c.project,
			Output:  c.output,
			Force:   c.force,
		})
	case "claude metrics", "clm":
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "warning: failed to get working directory: %v\n", err)
		}
		return claude.Metrics(stdout, stderr, cwd, claude.MetricsOptions{
			Days:       c.days,
			AsJSON:     c.json,
			DetailsTop: c.details,
		})
	case "codex", "codex hook":
		if isTerminal(stdin) {
			printUsage(stderr, version)
			return 0
		}
		return codex.Run(stdin, stdout)
	case "codex init":
		return codex.Init(stdout, stderr, codex.InitOptions{
			Project: c.project,
			Output:  c.output,
			Force:   c.force,
		})
	case "codex metrics", "com":
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "warning: failed to get working directory: %v\n", err)
		}
		return codex.Metrics(stdout, stderr, cwd, codex.MetricsOptions{
			Days:       c.days,
			AsJSON:     c.json,
			DetailsTop: c.details,
		})
	case "devin", "devin hook":
		if isTerminal(stdin) {
			printUsage(stderr, version)
			return 0
		}
		return devin.Run(stdin, stdout)
	case "devin init":
		return devin.Init(stdout, stderr, devin.InitOptions{
			Project: c.project,
			Output:  c.output,
			Force:   c.force,
		})
	case "devin metrics", "dvm":
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "warning: failed to get working directory: %v\n", err)
		}
		return devin.Metrics(stdout, stderr, cwd, devin.MetricsOptions{
			Days:       c.days,
			AsJSON:     c.json,
			DetailsTop: c.details,
		})
	case "init":
		return runDeprecatedInit(stderr)
	case "metrics":
		return runDeprecatedMetrics(stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", c.name())
		return 2
	}
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func printUsage(w io.Writer, version string) {
	fmt.Fprintf(w, `ccgate %s

PermissionRequest hook for AI coding tools.

Usage:
  ccgate                                     Read HookInput JSON from stdin (Claude Code hook).
                                             Equivalent to 'ccgate claude'. Permanent default.
  ccgate claude                              Same as above (explicit form).
  ccgate claude init [-p] [-o FILE] [-f]     Output the embedded Claude Code defaults.
  ccgate claude metrics [--days N] [--json]  Show Claude Code metrics.

  ccgate codex                               Read HookInput JSON from stdin (Codex CLI hook, experimental).
  ccgate codex init [-p] [-o FILE] [-f]      Output the embedded Codex CLI defaults.
  ccgate codex metrics [--days N] [--json]   Show Codex CLI metrics.

  ccgate devin                               Read HookInput JSON from stdin (Devin hook).
  ccgate devin init [-p] [-o FILE] [-f]      Output the embedded Devin defaults.
  ccgate devin metrics [--days N] [--json]   Show Devin metrics.

  ccgate clm [--days N] [--json]             Shortcut for 'ccgate claude metrics'.
  ccgate com [--days N] [--json]             Shortcut for 'ccgate codex metrics'.
  ccgate dvm [--days N] [--json]             Shortcut for 'ccgate devin metrics'.

Flags:
  --version    Print version and exit
  --help       Show help
`, version)
}
