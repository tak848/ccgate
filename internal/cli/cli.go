// Package cli wires the kong subcommand tree for the ccgate binary.
//
// Layout:
//
//	ccgate                       (no args + stdin pipe) -> claude.Run     (permanent default)
//	ccgate claude                                       -> claude.Run     (explicit)
//	ccgate claude init                                  -> claude.Init
//	ccgate claude metrics                               -> claude.Metrics
//	ccgate init / ccgate metrics                        -> deprecated     (exit 2 with migration hint)
//
// Bare `ccgate` is the canonical Claude Code hook invocation and will
// keep working forever — existing `~/.claude/settings.json` entries
// using `"command": "ccgate"` are not touched.
package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/alecthomas/kong"
	"golang.org/x/term"

	"github.com/tak848/ccgate/internal/cmd/claude"
)

// CLI is the kong-bound root command tree.
type CLI struct {
	Version kong.VersionFlag `help:"Print version and exit."`

	Claude  ClaudeCmd            `cmd:"" help:"Run the Claude Code PermissionRequest hook (or manage its config / metrics)."`
	Init    DeprecatedInitCmd    `cmd:"" help:"[removed in v0.5] Use 'ccgate claude init' instead."`
	Metrics DeprecatedMetricsCmd `cmd:"" help:"[removed in v0.5] Use 'ccgate claude metrics' instead."`
}

// Run is the binary entry point. main() should call cli.Run with the
// process's args/stdin/stdout/stderr and propagate the returned exit
// code.
func Run(version string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	claude.SetBuildVersion(version)

	// No args: behave like the legacy bare `ccgate` invocation —
	// pipe-driven hook on stdin, friendly usage on a tty. This path
	// must keep working forever; existing user settings refer to it.
	if len(args) == 0 {
		if isTerminal(stdin) {
			printUsage(stderr, version)
			return 0
		}
		return claude.Run(stdin, stdout)
	}

	var cli CLI
	parser, err := kong.New(&cli,
		kong.Name("ccgate"),
		kong.Description("ccgate — PermissionRequest hook for AI coding tools (Claude Code today; Codex CLI coming).\nNo args + stdin pipe = Claude Code hook (legacy invocation, permanent)."),
		kong.Vars{"version": version},
		kong.Writers(stdout, stderr),
		kong.Exit(func(code int) {
			// kong calls Exit on --help / --version / parse errors.
			// We can't actually exit here without aborting the
			// caller; instead we rely on the int return below.
			// Empty body is intentional — see the panic recovery
			// in main() if this assumption ever changes.
			panic(kongExit{code: code})
		}),
	)
	if err != nil {
		fmt.Fprintf(stderr, "internal error: %v\n", err)
		return 1
	}

	defer func() {
		if r := recover(); r != nil {
			if exit, ok := r.(kongExit); ok {
				os.Exit(exit.code)
			}
			panic(r)
		}
	}()

	kctx, err := parser.Parse(args)
	if err != nil {
		parser.Errorf("%v", err)
		return 2
	}

	return dispatch(kctx, &cli, stdin, stdout, stderr)
}

// kongExit unwinds back to Run so we can translate kong's process-level
// exit semantics into the int the caller already returns.
type kongExit struct{ code int }

func dispatch(kctx *kong.Context, cli *CLI, stdin io.Reader, stdout, stderr io.Writer) int {
	switch kctx.Command() {
	case "claude":
		return claude.Run(stdin, stdout)
	case "claude init":
		return claude.Init(stdout, stderr, claude.InitOptions{
			Project: cli.Claude.Init.Project,
			Output:  cli.Claude.Init.Output,
			Force:   cli.Claude.Init.Force,
		})
	case "claude metrics":
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "warning: failed to get working directory: %v\n", err)
		}
		return claude.Metrics(stdout, stderr, cwd, claude.MetricsOptions{
			Days:       cli.Claude.Metrics.Days,
			AsJSON:     cli.Claude.Metrics.JSON,
			DetailsTop: cli.Claude.Metrics.Details,
		})
	case "init":
		return runDeprecatedInit(stderr)
	case "metrics":
		return runDeprecatedMetrics(stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", kctx.Command())
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
  ccgate claude metrics [--days N] [--json]  Show usage metrics (current + legacy paths).

Flags:
  --version    Print version and exit
  --help       Show help
`, version)
}
