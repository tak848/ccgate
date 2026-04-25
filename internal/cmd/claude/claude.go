// Package claude implements the Claude Code PermissionRequest hook.
//
// Today this package is a thin orchestration shim around the existing
// internal/{gate,hookctx,metrics,config} packages so cli/ has a clean
// per-target entry point while the underlying logic stays unchanged.
// As shared primitives (internal/{prompt,llm,...}) absorb more of the
// gate orchestration, the implementation will move down here.
package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/gate"
	"github.com/tak848/ccgate/internal/hookctx"
	"github.com/tak848/ccgate/internal/metrics"
)

// Run reads a single PermissionRequest from stdin, decides allow / deny /
// fallthrough, and writes the response to stdout. It returns the exit
// code the wrapping main() should propagate.
func Run(stdin io.Reader, stdout io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var input hookctx.HookInput
	if err := json.NewDecoder(stdin).Decode(&input); err != nil {
		slog.Error("failed to decode stdin", "error", err)
		return 1
	}

	lr, err := config.Load(config.ClaudeLoadOptions(), input.Cwd)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		return 1
	}
	cfg := lr.Config

	logger, cleanup := initLogger(cfg.ResolveLogPath(), cfg.IsLogDisabled(), cfg.GetLogMaxSize())
	defer cleanup()
	slog.SetDefault(logger)

	if lr.Source == config.SourceGlobalConfig && len(cfg.Allow) == 0 && len(cfg.Deny) == 0 {
		slog.Warn("allow and deny rules are both empty; embedded defaults were not applied because a global config exists")
	}

	slog.Info("hook invoked",
		"tool", input.ToolName,
		"permission_mode", input.PermissionMode,
		"config_source", string(lr.Source),
	)

	start := time.Now()
	result, err := gate.DecidePermission(ctx, cfg, input)
	elapsed := time.Since(start)

	// Record metrics (fire-and-forget). user_interaction fallthrough is still
	// written so an audit trail exists; it is filtered out at aggregation time
	// (see metrics.aggregate) so it does not pollute automation_rate / Fall /
	// tool totals.
	if !cfg.IsMetricsDisabled() {
		entry := buildMetricsEntry(start, elapsed, input, cfg, result, err)
		metrics.Record(cfg.ResolveMetricsPath(), cfg.GetMetricsMaxSize(), entry)
	}

	if err != nil {
		slog.Error("DecidePermission failed",
			"error", err,
			"tool", input.ToolName,
			"elapsed_ms", elapsed.Milliseconds(),
		)
		return 1
	}
	if !result.HasDecision {
		slog.Info("DecidePermission: no decision (fallthrough)",
			"tool", input.ToolName,
			"fallthrough_kind", result.FallthroughKind,
			"elapsed_ms", elapsed.Milliseconds(),
		)
		return 0
	}

	slog.Info("DecidePermission: decision made",
		"behavior", result.Decision.Behavior,
		"message", result.Decision.Message,
		"tool", input.ToolName,
		"elapsed_ms", elapsed.Milliseconds(),
	)

	resp := gate.NewPermissionResponse(result.Decision)
	if err := json.NewEncoder(stdout).Encode(resp); err != nil {
		slog.Error("failed to encode response to stdout", "error", err)
		return 1
	}
	return 0
}

// InitOptions describes how `ccgate claude init` should output the
// embedded default configuration.
type InitOptions struct {
	// Project switches between the global template (false, default)
	// and the project-local template (true).
	Project bool
	// Output, when non-empty, writes to a file instead of stdout.
	Output string
	// Force overwrites an existing file at Output.
	Force bool
}

// Init writes the embedded default Claude Code configuration to stdout
// or to opts.Output. Returns the exit code the wrapping main() should
// propagate.
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

// MetricsOptions controls the `ccgate claude metrics` aggregation.
type MetricsOptions struct {
	Days       int
	AsJSON     bool
	DetailsTop int
}

// Metrics aggregates the Claude Code metrics file plus the legacy
// `$XDG_STATE_HOME/ccgate/metrics.jsonl` written by pre-v0.5 ccgate
// and prints the report to stdout. cwd seeds project-local config
// resolution. Returns the exit code the wrapping main() should
// propagate.
func Metrics(stdout io.Writer, stderr io.Writer, cwd string, opts MetricsOptions) int {
	lr, err := config.Load(config.ClaudeLoadOptions(), cwd)
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

// Version returns the build version baked into the binary, or "dev"
// when running from `go run` / unstamped builds. Exported so cli/
// can reuse the same lookup.
func Version() string {
	if v := buildVersion; v != "dev" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// buildVersion is overwritten via -ldflags at release time. Kept as
// a package-private var (instead of relying on main.version) so
// cli/ does not need to thread the version string into every
// subcommand.
var buildVersion = "dev"

// SetBuildVersion is invoked by main() at startup to forward the
// linker-injected version into this package.
func SetBuildVersion(v string) { buildVersion = v }

// initLogger opens the per-target log file (rotating when needed)
// and returns a slog.Logger plus a cleanup function. When logging
// is disabled or the file cannot be opened, it falls back to a
// no-op (or stderr) logger so the hook never crashes on logging.
func initLogger(logPath string, disabled bool, maxLogSize int64) (*slog.Logger, func()) {
	if disabled {
		return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1})), func() {}
	}

	logDir := filepath.Dir(logPath)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		slog.Warn("failed to create log directory", "error", err)
		return slog.New(slog.NewTextHandler(os.Stderr, nil)), func() {}
	}

	metrics.RotateIfNeeded(logPath, maxLogSize)

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to open log file %s: %v\n", logPath, err)
		return slog.New(slog.NewTextHandler(os.Stderr, nil)), func() {}
	}

	w := &atomicWriter{f: f}
	return slog.New(slog.NewTextHandler(w, nil)), func() { f.Close() }
}

type atomicWriter struct {
	f  *os.File
	mu sync.Mutex
}

func (w *atomicWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Write(p)
}

// legacyStateDir returns the historical $XDG_STATE_HOME/ccgate/
// directory where pre-v0.5 ccgate wrote both ccgate.log and
// metrics.jsonl. Used as a read-only fallback so existing users
// keep seeing their metrics history through `ccgate claude metrics`.
func legacyStateDir() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" && filepath.IsAbs(d) {
		return filepath.Join(d, "ccgate")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "ccgate")
	}
	return "."
}
