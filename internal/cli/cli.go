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
	"runtime"

	"github.com/alecthomas/kong"
	"golang.org/x/term"

	"github.com/tak848/ccgate/internal/cmd/claude"
	"github.com/tak848/ccgate/internal/cmd/codex"
)

// CLI is the kong-bound root command tree.
type CLI struct {
	Version kong.VersionFlag `help:"Print version and exit."`

	Claude  ClaudeCmd            `cmd:"" help:"Run the Claude Code PermissionRequest hook (or manage its config / metrics)."`
	Codex   CodexCmd             `cmd:"" help:"Run the OpenAI Codex CLI PermissionRequest hook (experimental, Linux/macOS only)."`
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
	case "claude", "claude hook":
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
	case "codex", "codex hook":
		if exit := requireCodexPlatform(stderr); exit != 0 {
			return exit
		}
		return codex.Run(stdin, stdout)
	case "codex init":
		// init does not call into the hook runtime, so it stays
		// available on Windows for users editing their config from
		// there even if they later run the hook on Linux/macOS.
		return codex.Init(stdout, stderr, codex.InitOptions{
			Output: cli.Codex.Init.Output,
			Force:  cli.Codex.Init.Force,
		})
	case "codex metrics":
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "warning: failed to get working directory: %v\n", err)
		}
		return codex.Metrics(stdout, stderr, cwd, codex.MetricsOptions{
			Days:       cli.Codex.Metrics.Days,
			AsJSON:     cli.Codex.Metrics.JSON,
			DetailsTop: cli.Codex.Metrics.Details,
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

// requireCodexPlatform fails fast on Windows because Codex hooks are
// upstream-disabled there ("temporarily disabled" per
// developers.openai.com/codex/hooks). Returns 0 to continue, 1 to
// abort with an explanatory message already written to stderr.
func requireCodexPlatform(stderr io.Writer) int {
	if runtime.GOOS == "windows" {
		fmt.Fprintln(stderr, "ccgate codex: Codex hooks are not supported on Windows (upstream feature is currently disabled there).")
		fmt.Fprintln(stderr, "See: https://developers.openai.com/codex/hooks")
		return 1
	}
	return 0
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
  ccgate claude metrics [--days N] [--json]  Show Claude Code metrics (current + legacy paths).

  ccgate codex                               Read HookInput JSON from stdin (Codex CLI hook, experimental).
  ccgate codex init [-o FILE] [-f]           Output the embedded Codex CLI defaults.
  ccgate codex metrics [--days N] [--json]   Show Codex CLI metrics.

Flags:
  --version    Print version and exit
  --help       Show help
`, version)
}
