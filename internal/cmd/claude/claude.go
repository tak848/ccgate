// Package claude is the Claude Code adapter for the ccgate runner.
// Everything Claude-specific (HookInput shape, settings.json reader,
// transcript reader, prefilter rules, output schema) lives here;
// the orchestration runs in internal/runner.
package claude

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/metrics"
	"github.com/tak848/ccgate/internal/runner"
)

//go:embed defaults.jsonnet
var defaultsJsonnet string

//go:embed defaults_project.jsonnet
var defaultsProjectJsonnet string

// Defaults exposes the embedded Claude Code defaults.
func Defaults() string { return defaultsJsonnet }

// LoadOptions returns the config.LoadOptions for the Claude Code hook.
// Returns an error when the user home directory cannot be resolved
// (rare: misconfigured CI / sandbox without HOME); the caller should
// surface that as a hard failure rather than silently degrading the
// global config path to a relative one and accidentally reading repo
// files as user-trusted config.
func LoadOptions() (config.LoadOptions, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config.LoadOptions{}, fmt.Errorf("resolve user home dir: %w", err)
	}
	sd := config.StateDir("claude")
	return config.LoadOptions{
		GlobalConfigPath:          filepath.Join(home, ".claude", config.BaseConfigName),
		ProjectLocalRelativePaths: []string{filepath.Join(".claude", config.LocalConfigName)},
		EmbedDefaults:             defaultsJsonnet,
		DefaultLogPath:            filepath.Join(sd, "ccgate.log"),
		DefaultMetricsPath:        filepath.Join(sd, "metrics.jsonl"),
	}, nil
}

// claudeTargetSection is the Claude-Code-specific guidance the
// runner inserts between the decision rules and the allow/deny
// lists. It teaches the LLM how to read settings_permissions and
// recent_transcript -- both fields ccgate adds to the user payload
// only for Claude. Codex has no equivalent to either today and
// passes no TargetSection. Wording is editorial and intentionally
// not asserted in tests; only the wiring (that the section reaches
// the system prompt) is.
const claudeTargetSection = "The user message includes settings_permissions and recent_transcript as background context.\n" +
	"settings_permissions lists the user's Claude Code static allow/deny/ask patterns. Claude Code already matched them BEFORE invoking ccgate, so by design every request that reaches ccgate did NOT auto-match allow (often composite constructs like `$()` or pipelines that slip past literal matchers, or MCP tools without a static matcher). Absence from settings_permissions.allow is therefore the normal, expected case -- use it only as a hint about user preferences, never as a whitelist requirement.\n" +
	"recent_transcript shows recent user messages and tool calls. Use it to understand what the user asked for. If the user explicitly requested the operation, prefer fallthrough over deny so Claude Code can confirm with the user. Explicit user intent never escalates a deny rule to allow.\n"

// Run reads a single PermissionRequest from stdin and writes the
// response to stdout. Delegates the orchestration to internal/runner
// while injecting the Claude-Code-specific extras the runner does not
// know about: target-name labelling, the Claude-only TargetSection
// guidance, the settings.json static-permissions reader, and the
// transcript JSONL tail. Codex has no equivalent to any of these
// today (its `~/.codex/config.toml` rules / transcript ingestion is
// a separate piece of work) so cmd/codex passes none of them.
func Run(stdin io.Reader, stdout io.Writer) int {
	opts, err := LoadOptions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ccgate claude: %v\n", err)
		return 1
	}
	return runner.Run(stdin, stdout, opts,
		runner.WithTargetName("Claude Code"),
		runner.WithCacheTarget("claude"),
		runner.WithPromptSection(claudeTargetSection),
		runner.WithHasRecentTranscript(true),
		runner.WithStaticPermissions(staticPermissionsHook),
		runner.WithRecentTranscript(recentTranscriptHook),
	)
}

func staticPermissionsHook(cwd string) any {
	sp := loadSettingsPermissions(cwd)
	if sp.empty() {
		return nil
	}
	return sp
}

func recentTranscriptHook(path string) any {
	t, err := loadRecentTranscript(path)
	if err != nil {
		// transcript is best-effort context; never fail the hook on it.
		return nil
	}
	if t.empty() {
		return nil
	}
	return t
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

// MetricsOptions controls `ccgate claude metrics`.
type MetricsOptions struct {
	Days       int
	AsJSON     bool
	DetailsTop int
}

// Metrics aggregates the Claude Code metrics file and prints the
// report to stdout.
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
