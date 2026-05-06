// Package keystore resolves a provider API key from one of two
// short-lived sources — an `auth.type=exec` shell helper or an
// `auth.type=file` path — for use by the ccgate hook on its hot path.
//
// Resolve takes the configuration in Options and returns a Result
// carrying the credential, an optional secret-free Reason classifier
// for failures, and a Source label ("exec", "file", "cache", "lock")
// describing where the result came from. Reason and Source are
// deliberately orthogonal: Reason fuels the metrics
// `CredentialFailures` aggregation, Source fuels the `slog` `source`
// attribute that the operational recovery checklist points users at.
//
// The hot path is short:
//
//   - Resolve consults the disk cache first (lock-free read). When the
//     cached `{key, expires_at}` is still valid (now + RefreshMargin <
//     expires_at) it returns the cached key as Source="cache".
//   - Otherwise it acquires a non-blocking flock on a sibling lock
//     file, double-checks the cache (so a peer that already refreshed
//     wins), and only then exec's the helper. The flock prevents
//     concurrent helpers from racing the same broker — important for
//     "only one valid key per user" issuers.
//   - Helper output is JSON-strict (`{key, expires_at}` —
//     unknown fields are dropped) when the trimmed stdout starts with
//     `{`, otherwise plain string. Plain strings are not cached and
//     must be a single non-empty line.
//
// The package keeps platform-portable code in keystore_common.go and
// uses gofrs/flock to abstract file locking. The two pieces that have
// no portable Go API — process-tree kill on cancel, and the
// permission-loose warning on a credential file — are split by build
// tag (keystore_unix.go uses POSIX primitives, keystore_windows.go uses
// Win32). Both implementations are real; there is no
// "unsupported platform" stub.
package keystore

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"time"
)

// Reason is a secret-free classifier embedded in the metrics
// `Reason` field for `credential_unavailable` fallthroughs and in
// log-only credential warnings. The empty string means success.
//
// fallthrough reasons (visible in metrics):
//
//	command_exit, json_parse, invalid_expiration, empty_output,
//	invalid_plain_output, expired, file_missing, file_read,
//	timeout, output_too_large, lock_timeout, lock_error,
//	cache_unavailable, provider_auth.
//
// log-only credential warnings (degraded but successful resolution):
//
//	cache_parse, cache_read, cache_write.
type Reason string

// Reason values. Keep these aligned with docs/configuration.md and
// the reason taxonomy in the issue #61 plan.
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

// Source labels where Resolve actually produced (or failed to
// produce) the credential. It feeds the `source` attribute on every
// credential-related log line so the operational recovery checklist
// can point users at the right component.
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
	// Shell selects which shell binary runs Command. Allowed values
	// are "bash" (default) and "powershell"; the runner is responsible
	// for validating and defaulting before populating Options.
	// "bash" runs `bash -c <Command>`; "powershell" runs
	// `pwsh -Command <Command>`.
	Shell string
	// Command is the verbatim `provider.auth.command` shell command
	// (run by the configured Shell). Empty when only Path is set.
	Command string
	// Path is the verbatim `provider.auth.path` file path (absolute or
	// `~/`-prefixed). Empty when only Command is set.
	Path string
	// ProviderName is the lower-cased provider key
	// (`anthropic`/`openai`/`gemini`/...). Used in the cache hash so
	// the same helper command does not silently share a credential
	// across providers.
	ProviderName string
	// BaseURL is the verbatim `provider.base_url` (may be empty).
	// Included in the cache hash so the same helper does not silently
	// share a credential across different proxies.
	BaseURL string
	// TargetName is the per-target subdir name (`claude`/`codex`)
	// used both for the cache path and the cache hash, so that
	// different targets keep credential scopes separate even when
	// the rest of the config matches.
	TargetName string
	// CacheKey is the `provider.auth.cache_key` value after `${VAR}`
	// env expansion. It contributes to the cache fingerprint as a
	// user-supplied salt so an env / profile dependent helper (e.g.
	// `aws sts ... --profile $AWS_PROFILE`) gets a separate cache
	// file per profile. Secret-free; runner is responsible for
	// rejecting undefined-env references upstream.
	CacheKey string
	// RefreshMargin is the early-refresh slack used by Resolve when
	// deciding whether the cached `expires_at` is still in the
	// future. Validated as `>= 0`; "0s" means "no early refresh".
	RefreshMargin time.Duration
	// CommandTimeout caps the hot-path cost of one Resolve call
	// (lock retry budget + helper exec). Validated as `> 0`.
	CommandTimeout time.Duration
}

// Result is what Resolve returns on every code path. Source is
// always populated; Reason is empty on success and set to a
// secret-free classifier on failure. Key is empty when Reason is
// non-empty.
type Result struct {
	Key    string
	Reason Reason
	Source Source
}

// CacheFingerprint is the deterministic per-cache-entry identifier.
// The same Options that should share a cache file produce the same
// fingerprint; differences in TargetName / ProviderName / BaseURL /
// Command / CacheKey always produce a fresh path.
//
// Inputs are fed length-prefixed (`uint32 BE len || raw bytes`) so
// any byte (including NUL) inside any field stays inside its own
// segment and never collides with adjacent fields. The leading
// "v1" tag itself is fed length-prefixed so the format version is
// part of the same uniform encoding.
//
// We don't include `Model` because users tend to flip models more
// often than they want to re-issue credentials.
func CacheFingerprint(opts Options) string {
	h := sha256.New()
	writeLP(h, "v1")
	writeLP(h, opts.TargetName)
	writeLP(h, opts.ProviderName)
	writeLP(h, opts.BaseURL)
	writeLP(h, opts.Command)
	writeLP(h, opts.CacheKey)
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8]) // 16 hex chars
}

func writeLP(w interface{ Write([]byte) (int, error) }, s string) {
	var buf [4]byte
	// gosec G115: len(s) cannot overflow uint32 in practice — every
	// caller writes a config-derived string (target / provider name /
	// base URL / shell command / cache_key salt) and helper output is
	// already bounded by stdoutLimit (64 KiB). uint32 holds 4 GiB.
	binary.BigEndian.PutUint32(buf[:], uint32(len(s))) //nolint:gosec // bounded inputs
	_, _ = w.Write(buf[:])
	_, _ = w.Write([]byte(s))
}
