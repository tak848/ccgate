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
	"unicode/utf8"

	"github.com/gofrs/flock"
)

// helperPayload is the canonical {key, expires_at} shape; unknown
// fields are dropped so the cache file never persists extras the
// helper happened to print.
type helperPayload struct {
	Key       string `json:"key"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

const (
	stdoutLimit = 64 * 1024
	stderrLimit = 8 * 1024
	lockBackoff = 50 * time.Millisecond
)

// shellInvocation returns the argv prefix for `auth.shell`. The
// powershell branch tries pwsh first (PowerShell 7+) and falls
// back to powershell.exe (5.1, ships with stock Windows); both run
// with -NoProfile -NonInteractive so a user profile cannot leak
// into stdout and an interactive prompt cannot wedge us.
func shellInvocation(shell string) (binary string, args []string) {
	if shell == "powershell" {
		bin := "powershell"
		if _, err := exec.LookPath("pwsh"); err == nil {
			bin = "pwsh"
		}
		return bin, []string{"-NoProfile", "-NonInteractive", "-Command"}
	}
	return "bash", []string{"-c"}
}

// shellCommand returns just the (binary, last-flag) pair, used by
// tests that pin the shell-name mapping without caring about the
// hardening flags.
func shellCommand(shell string) (string, string) {
	bin, args := shellInvocation(shell)
	return bin, args[len(args)-1]
}

// Resolve dispatches to exec or file based on Options.
func Resolve(ctx context.Context, opts Options) (Result, error) {
	switch {
	case opts.Command != "":
		return resolveCommand(ctx, opts)
	case opts.Path != "":
		return resolveFile(ctx, opts)
	default:
		return Result{Source: SourceExec}, errors.New("keystore: no auth.command or auth.path configured")
	}
}

// Invalidate removes the cache file for opts so the next fire
// re-runs the helper. Called on provider 401/403. No-op for the
// file branch (no cache there) and missing files.
func Invalidate(opts Options) error {
	if opts.Command == "" {
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

	cp, err := cachePath(opts)
	if err != nil {
		return Result{Reason: ReasonCacheUnavailable, Source: SourceCache},
			fmt.Errorf("cache path unavailable: %w", err)
	}

	// Fast path: cache hit, no lock.
	if key, ok := readCacheValid(cp, opts); ok {
		return Result{Key: key, Source: SourceCache}, nil
	}

	// Without a writable cache dir we cannot create the sibling
	// lock file that serialises concurrent fires; fail fast rather
	// than let parallel hooks all hit a single-valid-key broker.
	cacheDir := filepath.Dir(cp)
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return Result{Reason: ReasonCacheUnavailable, Source: SourceCache},
			fmt.Errorf("cache dir mkdir %s: %w", cacheDir, err)
	}
	// Tighten an inherited loose dir; no-op on Windows.
	if err := os.Chmod(cacheDir, 0o700); err != nil {
		return Result{Reason: ReasonCacheUnavailable, Source: SourceCache},
			fmt.Errorf("cache dir chmod %s: %w", cacheDir, err)
	}

	lock, lockReason, lockErr := acquireLock(ctx, cp+".lock")
	if lockErr != nil {
		// Last-chance reread: a peer may have refreshed during retry.
		if key, ok := readCacheValid(cp, opts); ok {
			return Result{Key: key, Source: SourceCache}, nil
		}
		return Result{Reason: lockReason, Source: SourceLock}, lockErr
	}
	defer releaseLock(lock)

	// Double-check the cache after acquiring the lock.
	if key, ok := readCacheValid(cp, opts); ok {
		return Result{Key: key, Source: SourceCache}, nil
	}

	payload, reason, err := execHelper(ctx, opts)
	if err != nil {
		return Result{Reason: reason, Source: SourceExec}, err
	}

	if reason, err := checkFresh(payload, opts.RefreshMargin); err != nil {
		slog.Warn("keystore: helper returned an already-expired credential",
			"reason", string(reason),
			"source", string(SourceExec),
			"expires_at", payload.ExpiresAt,
		)
		return Result{Reason: reason, Source: SourceExec}, err
	}

	// Cache only when expires_at lets us know when to refresh.
	if payload.ExpiresAt != "" {
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

func resolveFile(ctx context.Context, opts Options) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, opts.CommandTimeout)
	defer cancel()

	path, err := expandHomePath(strings.TrimSpace(opts.Path))
	if err != nil {
		return Result{Reason: ReasonFileRead, Source: SourceFile}, err
	}

	// Run the read on a goroutine and select on ctx so a stalled
	// mount surfaces as `timeout` instead of wedging the hook. The
	// goroutine itself stays blocked until the kernel completes the
	// I/O, but the hot path returns within CommandTimeout.
	type fileResult struct {
		data []byte
		info os.FileInfo
		err  error
	}
	ch := make(chan fileResult, 1)
	go func() {
		data, info, err := openBoundedRegularFile(path, stdoutLimit)
		ch <- fileResult{data, info, err}
	}()
	var r fileResult
	select {
	case r = <-ch:
	case <-ctx.Done():
		return Result{Reason: ReasonTimeout, Source: SourceFile},
			fmt.Errorf("file read timed out after %s: %w", opts.CommandTimeout, ctx.Err())
	}
	if r.err != nil {
		switch {
		case os.IsNotExist(r.err):
			return Result{Reason: ReasonFileMissing, Source: SourceFile}, r.err
		case errors.Is(r.err, errOutputTooLarge):
			return Result{Reason: ReasonOutputTooLarge, Source: SourceFile}, r.err
		case errors.Is(r.err, errNotRegularFile):
			return Result{Reason: ReasonFileRead, Source: SourceFile}, r.err
		}
		return Result{Reason: ReasonFileRead, Source: SourceFile}, r.err
	}
	warnLoosePermissions(path, r.info, SourceFile)
	payload, reason, err := parseHelperOutput(r.data)
	if err != nil {
		return Result{Reason: reason, Source: SourceFile}, err
	}
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

// readCacheValid is the lock-free fast path. Returns ok=true only
// when the cache file is present, parses, has both fields, and
// has at least RefreshMargin remaining; missing / corrupt / stale
// entries unlink themselves so a transient bad write self-heals
// on the next fire.
func readCacheValid(path string, opts Options) (string, bool) {
	data, info, err := openBoundedRegularFile(path, stdoutLimit)
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
	warnLoosePermissions(path, info, SourceCache)
	return payload.Key, true
}

// writeCache writes the canonical {key, expires_at} via tempfile
// + atomic rename. Extra fields from the helper (refresh tokens,
// session IDs) are dropped here so they never reach disk.
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

// acquireLock retries an exclusive non-blocking flock until ctx
// fires. gofrs/flock abstracts flock(2) / LockFileEx so the same
// loop runs on every OS.
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
// Process group setup + tree kill are platform-specific
// (applyHelperProcessAttrs / killHelperProcessTree) so a helper
// that spawns children gets cleaned up too on cancel.
func execHelper(ctx context.Context, opts Options) (helperPayload, Reason, error) {
	bin, args := shellInvocation(opts.Shell)
	cmd := exec.CommandContext(ctx, bin, append(args, opts.Command)...)
	applyHelperProcessAttrs(cmd)
	cmd.Cancel = func() error { return killHelperProcessTree(cmd) }
	cmd.WaitDelay = waitDelayFor(opts.CommandTimeout)
	cmd.Env = helperEnv(os.Environ())
	cmd.Stdin = nil

	stdout := &limitedBuffer{cap: stdoutLimit}
	stderr := &limitedBuffer{cap: stderrLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()
	if stdout.over {
		return helperPayload{}, ReasonOutputTooLarge,
			fmt.Errorf("helper stdout exceeded %d bytes", stdoutLimit)
	}
	if runErr != nil {
		if ctxErr := ctx.Err(); errors.Is(ctxErr, context.DeadlineExceeded) {
			return helperPayload{}, ReasonTimeout,
				fmt.Errorf("helper timed out after %s: %w", opts.CommandTimeout, ctxErr)
		}
		// stderr body is intentionally not logged: a misbehaving
		// helper using `set -x` could leak a token there, and the
		// log file is not 0600.
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

// parseHelperOutput is shared by exec and file paths. Output is
// rejected up-front if it is not valid UTF-8 or contains a NUL
// byte (both break HTTP header transport). A leading `{` dispatches
// to strict JSON parsing; otherwise the trimmed output must be a
// single non-empty line.
func parseHelperOutput(data []byte) (helperPayload, Reason, error) {
	if !utf8.Valid(data) {
		return helperPayload{}, ReasonInvalidPlainOutput,
			errors.New("helper output must be valid UTF-8")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return helperPayload{}, ReasonInvalidPlainOutput,
			errors.New("helper output contains a NUL byte")
	}
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
	// `{...}}`) signals helper noise; require EOF. Decoder.More is
	// not enough — it returns false on bare `}` / `]`.
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return helperPayload{}, ReasonJSONParse, errors.New("trailing data after helper json")
	}
	// Trim + single-line check, mirroring the plain-string branch.
	payload.Key = strings.TrimSpace(payload.Key)
	if payload.Key == "" {
		return helperPayload{}, ReasonJSONParse, errors.New("helper json missing key")
	}
	if strings.ContainsAny(payload.Key, "\r\n") {
		return helperPayload{}, ReasonJSONParse, errors.New("helper json key must be a single line")
	}
	// JSON-escaped NUL (the six-character sequence backslash-u-0-0-0-0)
	// passes the raw-byte scan in parseHelperOutput but ends up as
	// a literal NUL inside payload.Key, which would mangle
	// Authorization header transport.
	if strings.ContainsRune(payload.Key, 0) {
		return helperPayload{}, ReasonJSONParse, errors.New("helper json key contains a NUL byte")
	}
	if payload.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, payload.ExpiresAt); err != nil {
			return helperPayload{}, ReasonInvalidExpiration,
				fmt.Errorf("expires_at not RFC3339: %w", err)
		}
	}
	return payload, ReasonOK, nil
}

// checkFresh requires `now + margin < expires_at`, matching the
// cache fast-path boundary. A credential that would expire inside
// the margin (including the margin == 0 boundary) is rejected
// rather than handed to the SDK to race the next API call.
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

// helperEnv inherits the caller's env and adds a sentinel so a
// helper that wraps ccgate can self-detect recursion.
func helperEnv(parent []string) []string {
	out := make([]string, 0, len(parent)+1)
	out = append(out, parent...)
	out = append(out, "CCGATE_API_KEY_RESOLUTION=1")
	return out
}

// waitDelayFor caps the post-cancel reader tail so CommandTimeout
// stays a near-real upper bound: min(500ms, timeout/10).
func waitDelayFor(timeout time.Duration) time.Duration {
	const cap = 500 * time.Millisecond
	if timeout/10 < cap {
		return timeout / 10
	}
	return cap
}

var (
	errOutputTooLarge = errors.New("keystore: file exceeds size limit")
	errNotRegularFile = errors.New("keystore: not a regular file")
)

// openBoundedRegularFile reads up to limit bytes from a regular
// file. Stat-first rejects FIFOs / devices before Open could block;
// the small TOCTOU window between Stat and Open is acceptable for
// credential files under the user's own home directory.
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

// expandHomePath resolves `~` / `~/foo`. Other paths pass through.
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

// cachePath honours $XDG_CACHE_HOME if set, else os.UserCacheDir
// (Linux ~/.cache, macOS ~/Library/Caches, Windows %LocalAppData%).
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

// limitedBuffer is a bounded io.Writer; `over` flips once the cap
// is reached so callers can surface `output_too_large`.
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
