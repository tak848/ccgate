// Package claude is the Claude Code adapter for the ccgate runner.
// Everything Claude-specific (HookInput shape, settings.json reader,
// transcript reader, prefilter rules, output schema) lives here;
// the orchestration runs in internal/runner.
package claude

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/metrics"
	"github.com/tak848/ccgate/internal/runner"
)

// LoadOptions returns the config.LoadOptions for the Claude Code hook.
func LoadOptions() config.LoadOptions {
	home, _ := os.UserHomeDir()
	sd := config.ClaudeStateDir()
	return config.LoadOptions{
		GlobalConfigPath:          filepath.Join(home, ".claude", config.BaseConfigName),
		ProjectLocalRelativePaths: []string{filepath.Join(".claude", config.LocalConfigName)},
		EmbedDefaults:             config.DefaultsJsonnet,
		DefaultLogPath:            filepath.Join(sd, "ccgate.log"),
		DefaultMetricsPath:        filepath.Join(sd, "metrics.jsonl"),
	}
}

// Run reads a single PermissionRequest from stdin and writes the
// response to stdout. Delegates the entire orchestration to
// internal/runner; the only Claude-specific knowledge needed is
// LoadOptions (where to read config / write log+metrics) and the
// embedded defaults Init outputs.
func Run(stdin io.Reader, stdout io.Writer) int {
	return runner.Run(stdin, stdout, LoadOptions())
}

// InitOptions describes how `ccgate claude init` should output the
// embedded default configuration.
type InitOptions struct {
	Project bool
	Output  string
	Force   bool
}

// Init writes the embedded default Claude Code configuration.
func Init(stdout io.Writer, stderr io.Writer, opts InitOptions) int {
	content := config.DefaultsJsonnet
	if opts.Project {
		content = config.DefaultsProjectJsonnet
	}
	if opts.Output == "" {
		fmt.Fprint(stdout, content)
		return 0
	}
	if !opts.Force {
		if _, err := os.Stat(opts.Output); err == nil {
			fmt.Fprintf(stderr, "error: file already exists: %s (use -f to overwrite)\n", opts.Output)
			return 1
		}
	}
	dir := filepath.Dir(opts.Output)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(stderr, "error: failed to create directory %s: %v\n", dir, err)
		return 1
	}
	if err := os.WriteFile(opts.Output, []byte(content), 0o644); err != nil {
		fmt.Fprintf(stderr, "error: failed to write file %s: %v\n", opts.Output, err)
		return 1
	}
	fmt.Fprintf(stderr, "wrote %s\n", opts.Output)
	return 0
}

// MetricsOptions controls `ccgate claude metrics`.
type MetricsOptions struct {
	Days       int
	AsJSON     bool
	DetailsTop int
}

// Metrics aggregates the Claude Code metrics file plus
// `$XDG_STATE_HOME/ccgate/metrics.jsonl` (no `<target>` segment) and
// prints the report to stdout.
func Metrics(stdout io.Writer, stderr io.Writer, cwd string, opts MetricsOptions) int {
	lr, err := config.Load(LoadOptions(), cwd)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}

	paths := []string{
		lr.Config.ResolveMetricsPath(),
		filepath.Join(legacyStateDir(), "metrics.jsonl"),
	}
	if err := metrics.PrintReport(stdout, paths, metrics.ReportOptions{
		Days:       opts.Days,
		AsJSON:     opts.AsJSON,
		DetailsTop: opts.DetailsTop,
	}); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// Version returns the build version baked into the binary, or "dev".
func Version() string {
	if v := buildVersion; v != "dev" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

var buildVersion = "dev"

// SetBuildVersion forwards the linker-injected version into this
// package so cli/ does not need to thread it through every call.
func SetBuildVersion(v string) { buildVersion = v }

// legacyStateDir returns $XDG_STATE_HOME/ccgate/ (the path with no
// `<target>` segment). `Metrics` reads it in addition to the
// per-target path so the report includes any entries written there.
func legacyStateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "ccgate")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "ccgate")
	}
	return "."
}
