package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/alecthomas/kong"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/gate"
	"github.com/tak848/ccgate/internal/hookctx"
)

var version = "dev"

type CLI struct {
	Version kong.VersionFlag `help:"Print version and exit."`
}

func main() { os.Exit(_main()) }

func _main() int {
	var cli CLI
	kong.Parse(&cli,
		kong.Name("ccgate"),
		kong.Description("Claude Code PermissionRequest hook. Reads HookInput JSON from stdin, returns allow/deny/fallthrough to stdout."),
		kong.Vars{"version": version},
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var input hookctx.HookInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		slog.Error("failed to decode stdin", "error", err)
		return 1
	}

	cfg, err := config.Load(input.Cwd)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		return 1
	}

	logger, cleanup := initLogger(cfg.ResolveLogPath(), cfg.LogDisabled)
	defer cleanup()
	slog.SetDefault(logger)

	slog.Info("hook invoked",
		"tool", input.ToolName,
		"permission_mode", input.PermissionMode,
	)

	start := time.Now()
	decision, ok, err := gate.DecidePermission(ctx, cfg, input)
	elapsed := time.Since(start)

	if err != nil {
		slog.Error("DecidePermission failed",
			"error", err,
			"tool", input.ToolName,
			"elapsed_ms", elapsed.Milliseconds(),
		)
		return 1
	}
	if !ok {
		slog.Info("DecidePermission: no decision (fallthrough)",
			"tool", input.ToolName,
			"elapsed_ms", elapsed.Milliseconds(),
		)
		return 0
	}

	slog.Info("DecidePermission: decision made",
		"behavior", decision.Behavior,
		"message", decision.Message,
		"tool", input.ToolName,
		"elapsed_ms", elapsed.Milliseconds(),
	)

	resp := gate.NewPermissionResponse(decision)
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		slog.Error("failed to encode response to stdout", "error", err)
		return 1
	}
	return 0
}

const maxLogSize = 5 * 1024 * 1024 // 5 MB

func initLogger(logPath string, disabled bool) (*slog.Logger, func()) {
	if disabled {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1})), func() {}
	}

	logDir := filepath.Dir(logPath)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		slog.Warn("failed to create log directory", "error", err)
		return slog.New(slog.NewTextHandler(os.Stderr, nil)), func() {}
	}

	rotateLog(logPath)

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
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

func rotateLog(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxLogSize {
		return
	}
	prev := path + ".1"
	if err := os.Remove(prev); err != nil && !os.IsNotExist(err) {
		slog.Warn("failed to remove old log", "path", prev, "error", err)
	}
	if err := os.Rename(path, prev); err != nil {
		slog.Warn("failed to rotate log", "path", path, "error", err)
	}
}
