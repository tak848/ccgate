// Package keystore resolves a provider API key from one of two
// short-lived sources — an `api_key_command` shell helper or an
// `api_key_file` path — for use by the ccgate hook on its hot path.
//
// Resolve takes the configuration in Options and returns a Result
// carrying the credential, an optional secret-free Reason classifier
// for failures, and a Source label ("command", "file", "cache",
// "lock") describing where the result came from. Reason and Source
// are deliberately orthogonal: Reason fuels the metrics
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
// The package is split by build tag: keystore_unix.go owns the live
// implementation (flock, exec.Command with Setpgid for child kill,
// bounded io, atomic rename of the cache file). keystore_other.go is
// a stub that always returns Reason="unsupported_platform" so the
// runner can fall through gracefully on platforms that lack the Unix
// primitives this design depends on.
package keystore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
//	unsupported_platform, timeout, output_too_large, lock_timeout,
//	lock_error, provider_auth.
//
// log-only credential warnings (degraded but successful resolution):
//
//	cache_parse, cache_read, cache_write, cache_unavailable.
type Reason string

// Reason values. Keep these aligned with docs/configuration.md and
// the reason taxonomy in the issue #61 plan.
const (
	ReasonOK                  Reason = ""
	ReasonCommandExit         Reason = "command_exit"
	ReasonJSONParse           Reason = "json_parse"
	ReasonInvalidExpiration   Reason = "invalid_expiration"
	ReasonEmptyOutput         Reason = "empty_output"
	ReasonInvalidPlainOutput  Reason = "invalid_plain_output"
	ReasonExpired             Reason = "expired"
	ReasonFileMissing         Reason = "file_missing"
	ReasonFileRead            Reason = "file_read"
	ReasonUnsupportedPlatform Reason = "unsupported_platform"
	ReasonTimeout             Reason = "timeout"
	ReasonOutputTooLarge      Reason = "output_too_large"
	ReasonLockTimeout         Reason = "lock_timeout"
	ReasonLockError           Reason = "lock_error"
	ReasonProviderAuth        Reason = "provider_auth"

	// Log-only (Resolve still succeeds; these never sit in metrics).
	ReasonCacheParse       Reason = "cache_parse"
	ReasonCacheRead        Reason = "cache_read"
	ReasonCacheWrite       Reason = "cache_write"
	ReasonCacheUnavailable Reason = "cache_unavailable"
)

// Source labels where Resolve actually produced (or failed to
// produce) the credential. It feeds the `source` attribute on every
// credential-related log line so the operational recovery checklist
// can point users at the right component.
type Source string

const (
	SourceCommand Source = "command"
	SourceFile    Source = "file"
	SourceCache   Source = "cache"
	SourceLock    Source = "lock"
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
	// Command is the verbatim `provider.api_key_command` shell
	// command (passed to `/bin/sh -c`). Empty when only File is set.
	Command string
	// File is the verbatim `provider.api_key_file` path (absolute or
	// `~/`-prefixed). Empty when only Command is set.
	File string
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

// ErrUnsupported is returned by Resolve on platforms whose
// keystore_other.go stub is built (everything that doesn't satisfy
// the built-in `unix` build tag). Callers can errors.Is against it
// to distinguish "the OS lacks our flock/exec primitives" from
// other Resolve failures.
var ErrUnsupported = errors.New("keystore: api_key_command/api_key_file are not supported on this platform")

// CacheFingerprint is the deterministic per-cache-entry identifier.
// The same Options that should share a cache file produce the same
// fingerprint; differences in TargetName / ProviderName / BaseURL /
// Command always produce a fresh path.
//
// `v1\0` prefix gives us a versioning hook if the inputs ever need
// to change without immediately invalidating user caches; we don't
// include `Model` because users tend to flip models more often than
// they want to re-issue credentials.
func CacheFingerprint(opts Options) string {
	h := sha256.New()
	_, _ = h.Write([]byte("v1\x00"))
	_, _ = h.Write([]byte(opts.TargetName))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(opts.ProviderName))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(opts.BaseURL))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(opts.Command))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:8]) // 16 hex chars
}
