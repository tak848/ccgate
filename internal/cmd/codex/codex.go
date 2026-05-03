// Package codex is the OpenAI Codex CLI wrapper for the ccgate
// PermissionRequest hook. The hook orchestration itself lives in
// internal/runner; this package only owns the per-target config
// (where to read ~/.codex/ccgate.jsonnet, where to write the
// per-target log/metrics), the embedded defaults Init outputs,
// and the metrics report path Metrics aggregates.
package codex

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/metrics"
	"github.com/tak848/ccgate/internal/runner"
)

//go:embed defaults.jsonnet
var defaultsJsonnet string

//go:embed defaults_project.jsonnet
var defaultsProjectJsonnet string

// Defaults exposes the embedded Codex defaults.
func Defaults() string { return defaultsJsonnet }

// LoadOptions builds the config.LoadOptions for the Codex hook.
// Project-local config is read from `{repo_root}/.codex/ccgate.local.jsonnet`
// only. Returns an error when the user home directory cannot be
// resolved (rare: misconfigured CI / sandbox without HOME); the
// caller surfaces that as a hard failure rather than silently
// degrading the global config path to a relative one.
func LoadOptions() (config.LoadOptions, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config.LoadOptions{}, fmt.Errorf("resolve user home dir: %w", err)
	}
	sd := config.StateDir("codex")
	return config.LoadOptions{
		GlobalConfigPath:          filepath.Join(home, ".codex", config.BaseConfigName),
		ProjectLocalRelativePaths: []string{filepath.Join(".codex", config.LocalConfigName)},
		EmbedDefaults:             defaultsJsonnet,
		DefaultLogPath:            filepath.Join(sd, "ccgate.log"),
		DefaultMetricsPath:        filepath.Join(sd, "metrics.jsonl"),
	}, nil
}

// Run reads a single PermissionRequest from stdin and writes the
// response to stdout. Delegates the orchestration to internal/runner.
// The only Codex-specific knob the runner needs is the target-name
// label for the system prompt header. Codex delivers no
// recent_transcript, no settings.json equivalent, and no
// permission_mode today, so we pass none of the corresponding
// runner options -- the LLM is told via the (empty) TargetSection
// not to expect those fields.
func Run(stdin io.Reader, stdout io.Writer) int {
	opts, err := LoadOptions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ccgate codex: %v\n", err)
		return 1
	}
	return runner.Run(stdin, stdout, opts,
		runner.WithTargetName("Codex CLI"),
		runner.WithCacheTarget("codex"),
	)
}

// InitOptions describes how `ccgate codex init` should output the
// embedded defaults.
type InitOptions struct {
	Project bool
	Output  string
	Force   bool
}

// Init writes the embedded Codex defaults to stdout or opts.Output.
// When opts.Project is set, the project-local template (which
// appends restrictions on top of the global config) is written
// instead of the global defaults.
func Init(stdout io.Writer, stderr io.Writer, opts InitOptions) int {
	content := defaultsJsonnet
	if opts.Project {
		content = defaultsProjectJsonnet
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

// MetricsOptions controls `ccgate codex metrics`.
type MetricsOptions struct {
	Days       int
	AsJSON     bool
	DetailsTop int
}

// Metrics aggregates the Codex metrics file and prints the report
// to stdout.
func Metrics(stdout io.Writer, stderr io.Writer, cwd string, opts MetricsOptions) int {
	loadOpts, err := LoadOptions()
	if err != nil {
		fmt.Fprintf(stderr, "failed to load options: %v\n", err)
		return 1
	}
	lr, err := config.Load(loadOpts, cwd)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load config: %v\n", err)
		return 1
	}
	if err := metrics.PrintReport(stdout, []string{lr.Config.ResolveMetricsPath()}, metrics.ReportOptions{
		Days:       opts.Days,
		AsJSON:     opts.AsJSON,
		DetailsTop: opts.DetailsTop,
	}); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}
