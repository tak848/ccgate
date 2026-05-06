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
	"time"

	"github.com/gofrs/flock"
)

// helperPayload is the canonical shape Resolve parses helper / file
// JSON output into. Unknown fields are dropped on read so the cache
// file we re-marshal carries only `{key, expires_at}` and never
// echoes incidental secrets the helper happened to print (refresh
// tokens, broker session IDs, etc.).
type helperPayload struct {
	Key       string `json:"key"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

const (
	// stdoutLimit caps how many bytes we read from a helper. AWS /
	// kubectl-style credential payloads are kilobytes at most; an
	// unbounded stream here would let a misbehaving helper exhaust
	// memory and the user would be billed for our hot path.
	stdoutLimit = 64 * 1024
	// stderrLimit caps stderr capture so a chatty helper cannot blow
	// up memory while we wait for it to finish. The body is never
	// written to ccgate.log on failure (only the byte count + exit
	// error are logged) by design, so a helper that accidentally
	// echoed a token through `set -x` would not leak it through our
	// logs.
	stderrLimit = 8 * 1024
	// lockBackoff is the polling interval used while waiting for the
	// flock to become available. The deadline is set by ctx
	// (CommandTimeout) so we don't need an exponential backoff: the
	// upper bound on retries is bounded already.
	lockBackoff = 50 * time.Millisecond
)

// shellCommand maps the validated auth.shell value to the binary +
// flag pair that runs Command. Defaults to bash for an unset value
// (validation should have rejected anything else).
func shellCommand(shell string) (string, string) {
	if shell == "powershell" {
		return "pwsh", "-Command"
	}
	return "bash", "-c"
}

// Resolve dispatches to the exec or file implementation based on
// Options. The runner is responsible for picking exactly one of the
// two; if both are empty we return a generic error rather than
// silently succeeding (it would mean the runner forgot to short-
// circuit to the env-var path).
func Resolve(ctx context.Context, opts Options) (Result, error) {
	switch {
	case opts.Command != "":
		return resolveCommand(ctx, opts)
	case opts.Path != "":
		return resolveFile(opts)
	default:
		return Result{Source: SourceExec}, errors.New("keystore: no auth.command or auth.path configured")
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

	// Fail fast if the cache subsystem is unavailable: without a
	// sibling lock file every concurrent hook fire would hit the
	// helper in parallel, which single-valid-key brokers handle by
	// revoking the older credential. We'd rather surface
	// cache_unavailable to the runner than silently degrade into a
	// credential thrash.
	cp, err := cachePath(opts)
	if err != nil {
		return Result{Reason: ReasonCacheUnavailable, Source: SourceCache},
			fmt.Errorf("cache path unavailable: %w", err)
	}

	// Fast path: cache hit, no lock.
	if key, ok := readCacheValid(cp, opts); ok {
		return Result{Key: key, Source: SourceCache}, nil
	}

	cacheDir := filepath.Dir(cp)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return Result{Reason: ReasonCacheUnavailable, Source: SourceCache},
			fmt.Errorf("cache dir mkdir %s: %w", cacheDir, err)
	}
	// MkdirAll does not normalise permissions on existing dirs, so a
	// pre-existing 0755 cache dir from an older ccgate version (or
	// from an unrelated tool that happens to share `ccgate/`) would
	// leak the cache file into world-readable territory. Tighten it
	// here. (No-op on Windows: NTFS does not honour POSIX mode bits;
	// users rely on per-user `%LocalAppData%` ACL inheritance.)
	if err := os.Chmod(cacheDir, 0o700); err != nil {
		return Result{Reason: ReasonCacheUnavailable, Source: SourceCache},
			fmt.Errorf("cache dir chmod %s: %w", cacheDir, err)
	}

	// Refresh path: take an exclusive non-blocking flock on a
	// sibling lock file. Retry with a small sleep until the ctx
	// deadline so we never block uninterruptibly. If the lock
	// subsystem is broken we fail fast — running the helper without
	// the lock would defeat the whole "only one helper exec at a
	// time" property that single-valid-key brokers rely on.
	lockPath := cp + ".lock"
	lock, lockReason, lockErr := acquireLock(ctx, lockPath)
	if lockErr != nil {
		// Last-chance reread: a peer may have refreshed in the
		// window we were retrying.
		if key, ok := readCacheValid(cp, opts); ok {
			return Result{Key: key, Source: SourceCache}, nil
		}
		return Result{Reason: lockReason, Source: SourceLock}, lockErr
	}
	defer releaseLock(lock)

	// Double-check: a peer may have already refreshed while we
	// waited for the lock.
	if key, ok := readCacheValid(cp, opts); ok {
		return Result{Key: key, Source: SourceCache}, nil
	}

	payload, reason, err := execHelper(ctx, opts)
	if err != nil {
		return Result{Reason: reason, Source: SourceExec}, err
	}

	// Reject expired keys returned freshly: replaying a doomed
	// credential just to re-exec the helper next fire would burn
	// the broker's rate limit. Surface as `expired` so the user can
	// notice their helper is producing invalid output.
	if reason, err := checkFresh(payload, opts.RefreshMargin); err != nil {
		slog.Warn("keystore: helper returned an already-expired credential",
			"reason", string(reason),
			"source", string(SourceExec),
			"expires_at", payload.ExpiresAt,
		)
		return Result{Reason: reason, Source: SourceExec}, err
	}

	if payload.ExpiresAt != "" {
		// Only credentials with a future expires_at are cacheable —
		// otherwise we have no way to know when to refresh, so we
		// re-exec on every fire.
		if err := writeCache(cp, payload); err != nil {
			slog.Warn("keystore: cache write failed, returning fresh key cacheless",
				"reason", string(ReasonCacheWrite),
				"source", string(SourceCache),
				"path", cp,
				"error", err,
			)
		}
	}
	return Result{Key: payload.Key, Source: SourceExec}, nil
}

func resolveFile(opts Options) (Result, error) {
	// Validate trims whitespace before checking the path shape, so a
	// config like `auth: { type: 'file', path: " ~/.ccgate/key " }`
	// passes validation. Trim here too, otherwise the raw value would
	// be sent to expandHomePath / os.Open and surface as a misleading
	// file_missing.
	path, err := expandHomePath(strings.TrimSpace(opts.Path))
	if err != nil {
		return Result{Reason: ReasonFileRead, Source: SourceFile}, err
	}
	data, info, err := openBoundedRegularFile(path, stdoutLimit)
	if err != nil {
		switch {
		case os.IsNotExist(err):
			return Result{Reason: ReasonFileMissing, Source: SourceFile}, err
		case errors.Is(err, errOutputTooLarge):
			return Result{Reason: ReasonOutputTooLarge, Source: SourceFile}, err
		case errors.Is(err, errNotRegularFile):
			return Result{Reason: ReasonFileRead, Source: SourceFile}, err
		}
		return Result{Reason: ReasonFileRead, Source: SourceFile}, err
	}
	warnLoosePermissions(path, info)
	payload, reason, err := parseHelperOutput(data)
	if err != nil {
		return Result{Reason: reason, Source: SourceFile}, err
	}
	// Files have no fresh-vs-cache distinction (we did not produce
	// the file ourselves, the rotator did) so any past expires_at
	// here is the rotator's bug, not ours. Surface it as expired so
	// the user notices instead of letting the SDK 401. We also enforce
	// the same minimum-remaining-TTL guard (RefreshMargin) the cache
	// path uses, so a file containing a credential that's about to
	// expire mid-API-call surfaces as expired here instead of being
	// handed to the SDK and producing a confused 401.
	if reason, err := checkFresh(payload, opts.RefreshMargin); err != nil {
		slog.Warn("keystore: auth.path contains an expired or near-expiry credential",
			"reason", string(reason),
			"source", string(SourceFile),
			"path", path,
			"expires_at", payload.ExpiresAt,
		)
		return Result{Reason: reason, Source: SourceFile}, err
	}
	return Result{Key: payload.Key, Source: SourceFile}, nil
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
	data, err := readBoundedRegularFile(path, stdoutLimit)
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

// writeCache writes the canonical `{key, expires_at}` payload (no
// extra fields the helper happened to print) to the cache file using
// a tempfile + atomic rename in the same directory. We deliberately
// discard everything except the canonical fields so a long-lived
// `refresh_token` never makes it onto disk even if the helper hands
// it back.
func writeCache(path string, payload helperPayload) error {
	canonical := helperPayload{
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
// ctx fires. gofrs/flock abstracts the OS-specific syscall (flock(2)
// on Unix, LockFileEx on Windows) so the retry / cancellation
// scaffolding lives here and is identical for both platforms.
func acquireLock(ctx context.Context, path string) (*flock.Flock, Reason, error) {
	lock := flock.New(path)
	for {
		ok, err := lock.TryLock()
		if err != nil {
			return nil, ReasonLockError, fmt.Errorf("try-lock %s: %w", path, err)
		}
		if ok {
			return lock, ReasonOK, nil
		}
		select {
		case <-ctx.Done():
			return nil, ReasonLockTimeout, ctx.Err()
		case <-time.After(lockBackoff):
		}
	}
}

func releaseLock(lock *flock.Flock) {
	if lock == nil {
		return
	}
	_ = lock.Unlock()
}

// execHelper runs the configured shell command with the right
// timeout / env / kill semantics and parses the output.
//
// applyHelperProcessAttrs and killHelperProcessTree are defined per
// platform so the shell child is placed in its own process group
// (Setpgid on Unix, CREATE_NEW_PROCESS_GROUP + Job Object on
// Windows). Cancelling the context kills the whole pipeline, not
// just the shell, so a helper that spawns `go run` / a browser
// process doesn't leak it on timeout.
func execHelper(ctx context.Context, opts Options) (helperPayload, Reason, error) {
	bin, flag := shellCommand(opts.Shell)
	cmd := exec.CommandContext(ctx, bin, flag, opts.Command)
	applyHelperProcessAttrs(cmd)
	cmd.Cancel = func() error { return killHelperProcessTree(cmd) }
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
		// Deliberately do NOT log the stderr body. A misbehaving
		// helper that prints a token through `set -x` or similar
		// debug paths would otherwise leak it into ccgate.log,
		// which is a 0644 file shared across the whole ccgate
		// session. The size + exit error are enough to triage; the
		// user can re-run the helper manually with `2>&1` if they
		// need the actual stderr contents.
		slog.Warn("keystore: auth.command exited non-zero",
			"reason", string(ReasonCommandExit),
			"source", string(SourceExec),
			"stderr_bytes", stderr.buf.Len(),
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
	// Permissive decoder: we accept unknown fields because real
	// brokers attach metadata (`access_token_id`, `account`, ...)
	// alongside the credential, and we already drop those when we
	// re-marshal the canonical `{key, expires_at}` payload to the
	// cache file (writeCache).
	dec := json.NewDecoder(strings.NewReader(trimmed))
	var payload helperPayload
	if err := dec.Decode(&payload); err != nil {
		return helperPayload{}, ReasonJSONParse, fmt.Errorf("decode helper json: %w", err)
	}
	// Trailing non-whitespace after the JSON value (`{...} garbage`,
	// `{...}}`, `{...}{...}`) is a strong signal the helper printed
	// both a credential and debug noise — refuse it. Decode again and
	// require io.EOF; anything else means there's still data
	// (Decoder.More returns false on the bare `}` / `]` cases that
	// occur as accidental trailing characters, so it is not a
	// reliable trailing-data check on its own).
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
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

// checkFresh validates that a freshly-produced payload (helper or
// file) has remaining TTL strictly greater than RefreshMargin. The
// boundary is the same one the cache fast path uses
// (now+margin < expires_at), so the equality case is treated as
// stale. Without margin (margin == 0) this collapses to the
// "already expired" check.
//
// The margin guard matters even for fresh credentials: if a helper
// hands us a key with 1 second of TTL left, the next API call
// (provider.timeout_ms = default 20s) would race the expiry and
// surface as a confused 401. Surface it here as `expired` instead.
func checkFresh(payload helperPayload, margin time.Duration) (Reason, error) {
	if payload.ExpiresAt == "" {
		return ReasonOK, nil
	}
	exp, err := time.Parse(time.RFC3339, payload.ExpiresAt)
	if err != nil {
		// We already validated this in parseHelperOutput, but
		// belt-and-braces in case a future refactor reorders calls.
		return ReasonInvalidExpiration, fmt.Errorf("expires_at not RFC3339: %w", err)
	}
	if !time.Now().Add(margin).Before(exp) {
		return ReasonExpired, fmt.Errorf("credential expired or within refresh_margin (expires_at=%s)", payload.ExpiresAt)
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

// errOutputTooLarge / errNotRegularFile are sentinel errors used by
// readBoundedRegularFile so callers can map them to the right
// keystore Reason without parsing error strings.
var (
	errOutputTooLarge = errors.New("keystore: file exceeds size limit")
	errNotRegularFile = errors.New("keystore: not a regular file")
)

// readBoundedRegularFile reads up to limit+1 bytes from path,
// rejecting non-regular files (FIFO / device / socket / symlink to
// device, ...) and anything larger than limit. We need the cap on
// both `auth.path` and the cache file because a misconfigured
// path like `/dev/zero` or a corrupt cache symlink would otherwise
// let the hot path read unboundedly and either OOM or hang. The
// helper-stdout reader has the same cap (stdoutLimit); applying it
// uniformly across every file/stream credentials enter through
// keeps the budget honest.
func readBoundedRegularFile(path string, limit int) ([]byte, error) {
	data, _, err := openBoundedRegularFile(path, limit)
	return data, err
}

// openBoundedRegularFile is the same as readBoundedRegularFile but
// also returns the os.FileInfo so callers can inspect mode/uid for
// permission-warning purposes.
//
// We Stat first and reject anything that isn't a regular file: this
// catches a misconfigured `auth.path` pointing at a FIFO without
// having to open it (which would otherwise block on Unix waiting
// for a writer). The TOCTOU window between Stat and Open is
// acceptable for a credential file under the user's own home
// directory — an attacker with write access to that directory
// already has a much bigger problem than ccgate's hook.
func openBoundedRegularFile(path string, limit int) ([]byte, os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, info, fmt.Errorf("%w: %s", errNotRegularFile, path)
	}
	f, err := os.Open(path) //nolint:gosec // path is user-supplied auth.path or per-target cache file, validated upstream
	if err != nil {
		return nil, info, err
	}
	defer func() { _ = f.Close() }()
	// Read one byte beyond the limit so we can detect "exactly
	// limit bytes" vs "limit+1 or more bytes" without consuming
	// arbitrary amounts of memory.
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, info, err
	}
	if len(data) > limit {
		return nil, info, fmt.Errorf("%w: %s (>%d bytes)", errOutputTooLarge, path, limit)
	}
	return data, info, nil
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

// cachePath returns the per-target cache file location. We honour
// `$XDG_CACHE_HOME` on every OS for users who explicitly opt in to
// the XDG layout; otherwise we fall back to `os.UserCacheDir`, which
// gives the platform-native location: Linux `~/.cache`, macOS
// `~/Library/Caches`, Windows `%LocalAppData%`.
func cachePath(opts Options) (string, error) {
	var root string
	if env := os.Getenv("XDG_CACHE_HOME"); env != "" && filepath.IsAbs(env) {
		root = env
	} else {
		var err error
		root, err = os.UserCacheDir()
		if err != nil {
			return "", fmt.Errorf("locate user cache dir: %w", err)
		}
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

// Compile-time guard: limitedBuffer must satisfy io.Writer.
var _ io.Writer = (*limitedBuffer)(nil)
