//go:build unix

package keystore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// helperPayload is the canonical shape Resolve parses helper / file
// JSON output into. Unknown fields are dropped on read so the cache
// file we re-marshal carries only `{version, key, expires_at}` and
// never echoes incidental secrets the helper happened to print
// (refresh tokens, broker session IDs, etc.).
type helperPayload struct {
	Version   int    `json:"version,omitempty"`
	Key       string `json:"key"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// Supported is true on Unix builds (linux, darwin, freebsd, ...);
// the runner uses this constant to short-circuit on platforms where
// keystore_other.go is built instead.
const Supported = true

const (
	// stdoutLimit caps how many bytes we read from a helper. AWS /
	// kubectl-style credential payloads are kilobytes at most; an
	// unbounded stream here would let a misbehaving helper exhaust
	// memory and the user would be billed for our hot path.
	stdoutLimit = 64 * 1024
	// stderrLimit caps stderr capture so a chatty helper cannot blow
	// up our log file. Only the head of stderr is attached to the
	// warning log on failure; the body is discarded by design so
	// secrets the helper accidentally printed do not get persisted.
	stderrLimit = 8 * 1024
	// lockBackoff is the polling interval used while waiting for the
	// flock to become available. The deadline is set by ctx
	// (CommandTimeout) so we don't need an exponential backoff: the
	// upper bound on retries is bounded already.
	lockBackoff = 50 * time.Millisecond
)

// Resolve dispatches to the command or file implementation based on
// Options. The runner is responsible for picking exactly one of the
// two; if both are empty we return a generic error rather than
// silently succeeding (it would mean the runner forgot to short-
// circuit to the env-var path).
func Resolve(ctx context.Context, opts Options) (Result, error) {
	switch {
	case opts.Command != "":
		return resolveCommand(ctx, opts)
	case opts.File != "":
		return resolveFile(opts)
	default:
		return Result{Source: SourceCommand}, errors.New("keystore: no api_key_command or api_key_file configured")
	}
}

// Invalidate removes the on-disk cache file for the given Options
// (best-effort: a missing file is fine). The runner calls this when
// the provider returns 401/403 against a key that came out of
// helper / file resolution, so the next hook fire forces a fresh
// helper exec instead of replaying the bad credential.
func Invalidate(opts Options) error {
	if opts.Command == "" {
		// File mode does not write a cache file, and env-var
		// callers never reach Invalidate.
		return nil
	}
	path, err := cachePath(opts)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("invalidate %s: %w", path, err)
	}
	return nil
}

func resolveCommand(ctx context.Context, opts Options) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, opts.CommandTimeout)
	defer cancel()

	cachePath, cacheErr := cachePath(opts)
	if cacheErr != nil {
		// cachePath only fails when we cannot derive the user's home
		// dir, which is also where helper output would have nowhere
		// to land. Continue cacheless.
		slog.Warn("keystore: cache path unavailable, running helper without cache",
			"reason", string(ReasonCacheUnavailable),
			"source", string(SourceCache),
			"error", cacheErr,
		)
		return execHelperOnly(ctx, opts)
	}

	// Fast path: cache hit, no lock.
	if key, ok := readCacheValid(cachePath, opts); ok {
		return Result{Key: key, Source: SourceCache}, nil
	}

	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		slog.Warn("keystore: cache dir mkdir failed, running helper without cache",
			"reason", string(ReasonCacheUnavailable),
			"source", string(SourceCache),
			"path", filepath.Dir(cachePath),
			"error", err,
		)
		return execHelperOnly(ctx, opts)
	}

	// Refresh path: take an exclusive non-blocking flock on a
	// sibling lock file. Retry with a small sleep until the ctx
	// deadline so we never block uninterruptibly. If the lock
	// subsystem is broken we fail fast — running the helper without
	// the lock would defeat the whole "only one helper exec at a
	// time" property that single-valid-key brokers rely on.
	lockPath := cachePath + ".lock"
	lockFD, lockReason, lockErr := acquireLock(ctx, lockPath)
	if lockErr != nil {
		// Last-chance reread: a peer may have refreshed in the
		// window we were retrying.
		if key, ok := readCacheValid(cachePath, opts); ok {
			return Result{Key: key, Source: SourceCache}, nil
		}
		return Result{Reason: lockReason, Source: SourceLock}, lockErr
	}
	defer releaseLock(lockFD)

	// Double-check: a peer may have already refreshed while we
	// waited for the lock.
	if key, ok := readCacheValid(cachePath, opts); ok {
		return Result{Key: key, Source: SourceCache}, nil
	}

	payload, reason, err := execHelper(ctx, opts)
	if err != nil {
		return Result{Reason: reason, Source: SourceCommand}, err
	}

	// Reject expired keys returned freshly: replaying a doomed
	// credential just to re-exec the helper next fire would burn
	// the broker's rate limit. Surface as `expired` so the user can
	// notice their helper is producing invalid output.
	if reason, err := checkFreshExpiration(payload); err != nil {
		slog.Warn("keystore: helper returned an already-expired credential",
			"reason", string(reason),
			"source", string(SourceCommand),
			"expires_at", payload.ExpiresAt,
		)
		return Result{Reason: reason, Source: SourceCommand}, err
	}

	if payload.ExpiresAt != "" {
		// Only credentials with a future expires_at are cacheable —
		// otherwise we have no way to know when to refresh, so we
		// re-exec on every fire.
		if err := writeCache(cachePath, payload); err != nil {
			slog.Warn("keystore: cache write failed, returning fresh key cacheless",
				"reason", string(ReasonCacheWrite),
				"source", string(SourceCache),
				"path", cachePath,
				"error", err,
			)
		}
	}
	return Result{Key: payload.Key, Source: SourceCommand}, nil
}

func resolveFile(opts Options) (Result, error) {
	path, err := expandHomePath(opts.File)
	if err != nil {
		return Result{Reason: ReasonFileRead, Source: SourceFile}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{Reason: ReasonFileMissing, Source: SourceFile}, err
		}
		return Result{Reason: ReasonFileRead, Source: SourceFile}, err
	}
	payload, reason, err := parseHelperOutput(data)
	if err != nil {
		return Result{Reason: reason, Source: SourceFile}, err
	}
	// Files have no fresh-vs-cache distinction (we did not produce
	// the file ourselves, the rotator did) so any past expires_at
	// here is the rotator's bug, not ours. Surface it as expired so
	// the user notices instead of letting the SDK 401.
	if reason, err := checkFreshExpiration(payload); err != nil {
		slog.Warn("keystore: api_key_file contains an already-expired credential",
			"reason", string(reason),
			"source", string(SourceFile),
			"path", path,
			"expires_at", payload.ExpiresAt,
		)
		return Result{Reason: reason, Source: SourceFile}, err
	}
	return Result{Key: payload.Key, Source: SourceFile}, nil
}

// execHelperOnly runs the helper without ever touching the cache.
// Used when the cache subsystem is unavailable (mkdir failed, no
// home dir, ...) so the hook still produces a credential — just
// without the memoization benefit. Concurrent fires will all hit
// the helper, which is the price for "the cache is broken but the
// hook still works".
func execHelperOnly(ctx context.Context, opts Options) (Result, error) {
	payload, reason, err := execHelper(ctx, opts)
	if err != nil {
		return Result{Reason: reason, Source: SourceCommand}, err
	}
	if reason, err := checkFreshExpiration(payload); err != nil {
		return Result{Reason: reason, Source: SourceCommand}, err
	}
	return Result{Key: payload.Key, Source: SourceCommand}, nil
}

// readCacheValid is the lock-free fast path. We accept any of:
//
//   - file missing (cold cache, normal first-fire case)
//   - read error (treat as "cache unusable", continue to refresh
//     after warning + best-effort unlink)
//   - JSON parse error (treat as a corrupted cache file: unlink and
//     refresh, so a one-time bit-flip / partial write doesn't wedge
//     the user's hook)
//   - stale expiry (refresh)
//
// Only a successful read with `now + RefreshMargin < expires_at`
// returns ok=true.
func readCacheValid(path string, opts Options) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false
		}
		slog.Warn("keystore: cache read failed, will refresh",
			"reason", string(ReasonCacheRead),
			"source", string(SourceCache),
			"path", path,
			"error", err,
		)
		_ = os.Remove(path)
		return "", false
	}
	var payload helperPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		slog.Warn("keystore: cache parse failed, will refresh",
			"reason", string(ReasonCacheParse),
			"source", string(SourceCache),
			"path", path,
			"error", err,
		)
		_ = os.Remove(path)
		return "", false
	}
	if payload.Key == "" || payload.ExpiresAt == "" {
		// A cache entry without the fields we rely on is unusable.
		_ = os.Remove(path)
		return "", false
	}
	exp, err := time.Parse(time.RFC3339, payload.ExpiresAt)
	if err != nil {
		_ = os.Remove(path)
		return "", false
	}
	if !time.Now().Add(opts.RefreshMargin).Before(exp) {
		return "", false
	}
	return payload.Key, true
}

// writeCache writes the canonical `{version, key, expires_at}`
// payload (no extra fields the helper happened to print) to the
// cache file using a tempfile + atomic rename in the same
// directory. We deliberately discard everything except the canonical
// fields so a long-lived `refresh_token` never makes it onto disk
// even if the helper hands it back.
func writeCache(path string, payload helperPayload) error {
	canonical := helperPayload{
		Version:   1,
		Key:       payload.Key,
		ExpiresAt: payload.ExpiresAt,
	}
	body, err := json.Marshal(canonical)
	if err != nil {
		return fmt.Errorf("marshal cache payload: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "api_key.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp to cache: %w", err)
	}
	return nil
}

// acquireLock loops until it gets an exclusive flock on path or the
// ctx fires. EWOULDBLOCK means a peer is refreshing — sleep a touch
// and retry. Any other syscall error is fatal: we fail fast rather
// than fall through to a lock-free helper exec which would defeat
// "one helper at a time" for single-valid-key brokers.
func acquireLock(ctx context.Context, path string) (int, Reason, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return -1, ReasonLockError, fmt.Errorf("open lock %s: %w", path, err)
	}
	for {
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return fd, ReasonOK, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = unix.Close(fd)
			return -1, ReasonLockError, fmt.Errorf("flock %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			_ = unix.Close(fd)
			return -1, ReasonLockTimeout, ctx.Err()
		case <-time.After(lockBackoff):
		}
	}
}

func releaseLock(fd int) {
	if fd < 0 {
		return
	}
	_ = unix.Flock(fd, unix.LOCK_UN)
	_ = unix.Close(fd)
}

// execHelper runs the configured shell command with the right
// timeout / env / kill semantics and parses the output.
//
// The shell child is placed in its own process group so cancelling
// the context kills the whole pipeline, not just `/bin/sh`. We
// document that helpers must not daemonize (anything that calls
// setsid escapes the group) — that contract is enforced by
// observation rather than by force-kill heuristics.
func execHelper(ctx context.Context, opts Options) (helperPayload, Reason, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", opts.Command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Negative pid in syscall.Kill targets the process group,
		// taking down sub-processes spawned through the shell. We
		// SIGKILL rather than SIGTERM because helpers are expected
		// to be non-interactive and quick to terminate.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = waitDelayFor(opts.CommandTimeout)
	cmd.Env = helperEnv(os.Environ())
	cmd.Stdin = nil // no interactive input

	stdout := &limitedBuffer{cap: stdoutLimit}
	stderr := &limitedBuffer{cap: stderrLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()
	if stdout.over {
		// Helper printed too much: don't trust any of it. (A
		// well-behaved helper writes only the credential line.)
		return helperPayload{}, ReasonOutputTooLarge,
			fmt.Errorf("helper stdout exceeded %d bytes", stdoutLimit)
	}
	if runErr != nil {
		// Distinguish ctx-deadline hits from non-zero exit so the
		// user sees `timeout` vs `command_exit` in metrics.
		if ctxErr := ctx.Err(); errors.Is(ctxErr, context.DeadlineExceeded) {
			return helperPayload{}, ReasonTimeout,
				fmt.Errorf("helper timed out after %s: %w", opts.CommandTimeout, ctxErr)
		}
		// Bound the stderr we attach to the warning so a chatty
		// helper cannot bloat ccgate.log; the body is *not* placed
		// in metrics by design.
		slog.Warn("keystore: api_key_command exited non-zero",
			"reason", string(ReasonCommandExit),
			"source", string(SourceCommand),
			"stderr", stderr.head(256),
			"error", runErr,
		)
		return helperPayload{}, ReasonCommandExit, runErr
	}
	payload, reason, err := parseHelperOutput(stdout.Bytes())
	if err != nil {
		return helperPayload{}, reason, err
	}
	return payload, ReasonOK, nil
}

// parseHelperOutput is shared by command and file paths: trim, look
// for a leading `{` to dispatch to strict JSON parsing, otherwise
// validate as a single-line plain string.
//
// Plain-string mode is an explicit *narrow* contract: trim must
// leave exactly one non-empty line. Multi-line plain output is what
// you'd get from `gcloud auth print-access-token` followed by an
// accidental debug print, and silently passing that to the SDK
// produced confusing 401s in the past — surface it as a helper
// failure instead.
func parseHelperOutput(data []byte) (helperPayload, Reason, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return helperPayload{}, ReasonEmptyOutput, errors.New("helper produced no output")
	}
	if strings.HasPrefix(trimmed, "{") {
		return parseHelperJSON(trimmed)
	}
	if strings.ContainsAny(trimmed, "\r\n") {
		return helperPayload{}, ReasonInvalidPlainOutput,
			errors.New("plain helper output must be a single line")
	}
	return helperPayload{Key: trimmed}, ReasonOK, nil
}

func parseHelperJSON(trimmed string) (helperPayload, Reason, error) {
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.DisallowUnknownFields()
	// We don't actually disallow unknown fields at the parse step
	// because helpers might add metadata (`access_token_id`,
	// `account`, ...) and we already drop them when we re-marshal
	// for the cache. Re-enable for now via a permissive decoder.
	dec = json.NewDecoder(strings.NewReader(trimmed))
	var payload helperPayload
	if err := dec.Decode(&payload); err != nil {
		return helperPayload{}, ReasonJSONParse, fmt.Errorf("decode helper json: %w", err)
	}
	// Trailing non-whitespace after the JSON value (`{...} garbage`)
	// is a strong signal the helper printed both a credential and
	// debug noise — refuse it the same way we refuse multi-line
	// plain output.
	if dec.More() {
		return helperPayload{}, ReasonJSONParse, errors.New("trailing data after helper json")
	}
	if strings.TrimSpace(payload.Key) == "" {
		return helperPayload{}, ReasonJSONParse, errors.New("helper json missing key")
	}
	if payload.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, payload.ExpiresAt); err != nil {
			return helperPayload{}, ReasonInvalidExpiration,
				fmt.Errorf("expires_at not RFC3339: %w", err)
		}
	}
	return payload, ReasonOK, nil
}

// checkFreshExpiration validates that a freshly-produced payload
// (helper or file) hasn't already passed expires_at. Distinct from
// the "cache stale" check, which uses RefreshMargin.
func checkFreshExpiration(payload helperPayload) (Reason, error) {
	if payload.ExpiresAt == "" {
		return ReasonOK, nil
	}
	exp, err := time.Parse(time.RFC3339, payload.ExpiresAt)
	if err != nil {
		// We already validated this in parseHelperOutput, but
		// belt-and-braces in case a future refactor reorders calls.
		return ReasonInvalidExpiration, fmt.Errorf("expires_at not RFC3339: %w", err)
	}
	if !time.Now().Before(exp) {
		return ReasonExpired, fmt.Errorf("credential already expired at %s", payload.ExpiresAt)
	}
	return ReasonOK, nil
}

// helperEnv builds the env we pass to the helper. We inherit the
// caller's env (the helper might need `AWS_PROFILE`, `GH_TOKEN`,
// ...) and add a sentinel so a helper that wraps ccgate can detect
// recursive invocation.
func helperEnv(parent []string) []string {
	out := make([]string, 0, len(parent)+1)
	out = append(out, parent...)
	out = append(out, "CCGATE_API_KEY_RESOLUTION=1")
	return out
}

// waitDelayFor caps the extra time `os/exec` keeps reading from the
// helper after ctx fires. We pick the smaller of 500ms and 1/10 of
// the timeout so a 5s timeout has a 500ms tail, a 1s timeout has a
// 100ms tail. The point is to make CommandTimeout a near-real upper
// bound on hot-path latency.
func waitDelayFor(timeout time.Duration) time.Duration {
	const cap = 500 * time.Millisecond
	if timeout/10 < cap {
		return timeout / 10
	}
	return cap
}

// expandHomePath turns `~` / `~/foo` into the absolute path. The
// runner already rejected relative paths at validate time, so any
// path that doesn't start with `~` is returned verbatim.
func expandHomePath(p string) (string, error) {
	switch {
	case p == "~":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return home, nil
	case strings.HasPrefix(p, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, p[2:]), nil
	default:
		return p, nil
	}
}

// cachePath returns `$XDG_CACHE_HOME/ccgate/<target>/api_key.<hash>.json`
// (or the `~/.cache/ccgate/<target>/...` fallback when XDG_CACHE_HOME
// is unset / not absolute, mirroring the stateDir() semantics in
// internal/config so users see one consistent layout for state /
// metrics / cache).
func cachePath(opts Options) (string, error) {
	root := os.Getenv("XDG_CACHE_HOME")
	if root == "" || !filepath.IsAbs(root) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locate home dir: %w", err)
		}
		root = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(root, "ccgate", opts.TargetName)
	return filepath.Join(dir, "api_key."+CacheFingerprint(opts)+".json"), nil
}

// limitedBuffer is a bounded io.Writer used for stdout / stderr
// capture. Once cap is reached the buffer flips `over` and discards
// further writes; callers inspect `over` to surface
// `output_too_large` when stdout is the offender.
type limitedBuffer struct {
	buf  bytes.Buffer
	cap  int
	over bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.over {
		return len(p), nil
	}
	remaining := b.cap - b.buf.Len()
	if remaining <= 0 {
		b.over = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.over = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }

// head returns up to n bytes from the start of the buffer, useful
// for putting bounded stderr previews into log attrs.
func (b *limitedBuffer) head(n int) string {
	bs := b.buf.Bytes()
	if len(bs) <= n {
		return string(bs)
	}
	return string(bs[:n])
}

// Compile-time guard: limitedBuffer must satisfy io.Writer.
var _ io.Writer = (*limitedBuffer)(nil)
