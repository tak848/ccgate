// Package devin is the Devin CLI wrapper for the ccgate
// PermissionRequest hook. The hook orchestration itself lives in
// internal/runner; this package only owns the per-target config
// (where to read ~/.config/devin/ccgate.jsonnet, where to write the
// per-target log/metrics), the embedded defaults Init outputs, the
// Devin-shaped stdout encoder, and the metrics report path Metrics
// aggregates.
package devin

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/llm"
	"github.com/tak848/ccgate/internal/metrics"
	"github.com/tak848/ccgate/internal/runner"
)

//go:embed defaults.jsonnet
var defaultsJsonnet string

//go:embed defaults_project.jsonnet
var defaultsProjectJsonnet string

// Defaults exposes the embedded Devin defaults.
func Defaults() string { return defaultsJsonnet }

// Devin's PermissionRequest decision vocabulary differs from Claude
// Code's: approve/block instead of allow/deny. Devin also accepts the
// Claude hookSpecificOutput envelope for .claude/-imported hooks, but
// the flat decision object is the documented shape for
// .devin/hooks.v1.json, so that is what ccgate emits.
const (
	decisionApprove = "approve"
	decisionBlock   = "block"
)

// devinHookOutput is the flat JSON object Devin reads from a command
// hook's stdout to decide a PermissionRequest.
type devinHookOutput struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

// encodeOutput maps the runner's allow/deny Decision onto Devin's
// approve/block vocabulary. Fallthrough never reaches this function:
// the runner emits no output at all and Devin shows its own prompt.
func encodeOutput(_ string, d llm.Decision) any {
	out := devinHookOutput{Decision: decisionApprove, Reason: d.Message}
	if d.Behavior == llm.BehaviorDeny {
		out.Decision = decisionBlock
	}
	return out
}

// LoadOptions builds the config.LoadOptions for the Devin hook.
// Project-local config is read from `{repo_root}/.devin/ccgate.local.jsonnet`
// only. Returns an error when the user home directory cannot be
// resolved (rare: misconfigured CI / sandbox without HOME); the
// caller surfaces that as a hard failure rather than silently
// degrading the global config path to a relative one.
func LoadOptions() (config.LoadOptions, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config.LoadOptions{}, fmt.Errorf("resolve user home dir: %w", err)
	}
	sd := config.StateDir("devin")
	return config.LoadOptions{
		GlobalConfigPath:          filepath.Join(configDir(home), config.BaseConfigName),
		ProjectLocalRelativePaths: []string{filepath.Join(".devin", config.LocalConfigName)},
		EmbedDefaults:             defaultsJsonnet,
		DefaultLogPath:            filepath.Join(sd, "ccgate.log"),
		DefaultMetricsPath:        filepath.Join(sd, "metrics.jsonl"),
	}, nil
}

// configDir mirrors Devin's own user-config resolution:
// $XDG_CONFIG_HOME/devin or ~/.config/devin on unix,
// %APPDATA%\devin on Windows.
func configDir(home string) string {
	if runtime.GOOS == "windows" {
		if dir := os.Getenv("APPDATA"); dir != "" {
			return filepath.Join(dir, "devin")
		}
		return filepath.Join(home, "AppData", "Roaming", "devin")
	}
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" && filepath.IsAbs(dir) {
		return filepath.Join(dir, "devin")
	}
	return filepath.Join(home, ".config", "devin")
}

// Run reads a single PermissionRequest from stdin and writes the
// response to stdout. Delegates the orchestration to internal/runner.
// The Devin-specific knobs the runner needs: the target-name label for
// the system prompt header, the DEVIN_PROJECT_DIR cwd fallback (Devin
// delivers no cwd on the wire), and the flat decision encoder. Devin
// delivers no recent_transcript, no settings.json equivalent, and no
// permission_mode today, so we pass none of the corresponding runner
// options.
func Run(stdin io.Reader, stdout io.Writer) int {
	opts, err := LoadOptions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ccgate devin: %v\n", err)
		return 1
	}
	return runner.Run(stdin, stdout, opts,
		runner.WithTargetName("Devin"),
		runner.WithCacheTarget("devin"),
		runner.WithCwdEnv("DEVIN_PROJECT_DIR"),
		runner.WithOutputEncoder(encodeOutput),
	)
}

// InitOptions describes how `ccgate devin init` should output the
// embedded defaults.
type InitOptions struct {
	Project bool
	Output  string
	Force   bool
}

// Init writes the embedded Devin defaults to stdout or opts.Output.
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

// MetricsOptions controls `ccgate devin metrics`.
type MetricsOptions struct {
	Days       int
	AsJSON     bool
	DetailsTop int
}

// Metrics aggregates the Devin metrics file and prints the report
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
