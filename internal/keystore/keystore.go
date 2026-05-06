// Package keystore resolves a provider API key from `auth.type=exec`
// (a shell helper) or `auth.type=file` (a rotator-managed file) on
// the ccgate hook's hot path. Resolve returns a Result with the
// credential plus a secret-free Reason / Source pair for metrics
// and recovery logs.
package keystore

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"time"
)

// Reason is a secret-free classifier surfaced in metrics for
// credential_unavailable fallthroughs and in log-only warnings.
// "" means success. See docs/configuration.md for the full list.
type Reason string

// Reason values. Keep these aligned with docs/configuration.md.
const (
	ReasonOK                 Reason = ""
	ReasonCommandExit        Reason = "command_exit"
	ReasonJSONParse          Reason = "json_parse"
	ReasonInvalidExpiration  Reason = "invalid_expiration"
	ReasonEmptyOutput        Reason = "empty_output"
	ReasonInvalidPlainOutput Reason = "invalid_plain_output"
	ReasonExpired            Reason = "expired"
	ReasonFileMissing        Reason = "file_missing"
	ReasonFileRead           Reason = "file_read"
	ReasonTimeout            Reason = "timeout"
	ReasonOutputTooLarge     Reason = "output_too_large"
	ReasonLockTimeout        Reason = "lock_timeout"
	ReasonLockError          Reason = "lock_error"
	ReasonCacheUnavailable   Reason = "cache_unavailable"
	ReasonProviderAuth       Reason = "provider_auth"

	// Log-only (Resolve still succeeds; these never sit in metrics).
	ReasonCacheParse Reason = "cache_parse"
	ReasonCacheRead  Reason = "cache_read"
	ReasonCacheWrite Reason = "cache_write"
)

// Source labels where Resolve produced (or failed to produce) the
// credential, used in the slog `source` attribute.
type Source string

const (
	SourceExec  Source = "exec"
	SourceFile  Source = "file"
	SourceCache Source = "cache"
	SourceLock  Source = "lock"
)

// Options carries everything Resolve needs from the runner. It is
// the runner's responsibility to flatten ProviderConfig into this
// struct (the keystore package does not import config to keep the
// dependency direction one-way).
//
// RefreshMargin and CommandTimeout are pre-validated time.Duration
// values rather than the original duration strings: validation
// happens at config load, so resolving never re-parses or has to
// fall back to defaults at hot-path time.
type Options struct {
	// Shell is "bash" (default) or "powershell"; see shellInvocation
	// for how each is launched. The runner validates and defaults.
	Shell string
	// Command is the verbatim auth.command. Empty when only Path is set.
	Command string
	// Path is the verbatim auth.path (absolute or `~/`-prefixed).
	// Empty when only Command is set.
	Path string
	// ProviderName / BaseURL / TargetName / CacheKey contribute to
	// the cache fingerprint so credential scopes don't accidentally
	// share a file. CacheKey is a user-supplied salt for the
	// "same command, different account" case; pull env values via
	// the jsonnet std.native('env') / must_env helpers.
	ProviderName string
	BaseURL      string
	TargetName   string
	CacheKey     string
	// RefreshMargin: early-refresh slack. >= 0; 0 disables.
	RefreshMargin time.Duration
	// CommandTimeout: hard cap on one Resolve call (lock + I/O + exec).
	CommandTimeout time.Duration
}

// Result has one of (Key, Source=non-cache) on success or
// (Reason, Source) on failure.
type Result struct {
	Key    string
	Reason Reason
	Source Source
}

// CacheFingerprint hashes the inputs that distinguish credential
// scopes. Inputs are length-prefixed (uint32 BE len || bytes) so any
// byte stays inside its own segment.
func CacheFingerprint(opts Options) string {
	h := sha256.New()
	writeLP(h, "v1")
	writeLP(h, opts.TargetName)
	writeLP(h, opts.ProviderName)
	writeLP(h, opts.BaseURL)
	writeLP(h, opts.Shell)
	writeLP(h, opts.Command)
	writeLP(h, opts.CacheKey)
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8]) // 16 hex chars
}

func writeLP(w interface{ Write([]byte) (int, error) }, s string) {
	var buf [4]byte
	// Inputs are config-derived strings bounded well under uint32.
	binary.BigEndian.PutUint32(buf[:], uint32(len(s))) //nolint:gosec // bounded inputs
	_, _ = w.Write(buf[:])
	_, _ = w.Write([]byte(s))
}
