//go:build unix

package keystore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helperScript writes a tiny shell script under the test's tempdir
// and returns the command ccgate should run. Each test gets its own
// script so we can run cases in parallel without stomping a shared
// path. We always write `0o700` so the script is executable but not
// world-readable, matching how a real user would deploy a helper.
func helperScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "helper.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	return path
}

// withCacheRoot points $XDG_CACHE_HOME at a fresh tempdir so cache
// files cannot leak between tests or between this test run and the
// developer's real cache. cachePath() honours XDG_CACHE_HOME when
// it's an absolute path, which is exactly what we need.
func withCacheRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	return root
}

func opts(cmd string) Options {
	return Options{
		Command:        cmd,
		ProviderName:   "test",
		BaseURL:        "",
		TargetName:     "claude",
		RefreshMargin:  30 * time.Second,
		CommandTimeout: 5 * time.Second,
	}
}

func TestResolveCommand(t *testing.T) {

	future := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)

	cases := map[string]struct {
		body       string // shell body for helper script
		wantKey    string
		wantReason Reason
		wantErr    bool
	}{
		"json with future expiry caches and returns key": {
			body:    `printf '{"key":"sk-future","expires_at":"` + future + `"}'`,
			wantKey: "sk-future",
		},
		"json with no expiry returns key uncached": {
			body:    `printf '{"key":"sk-nopexp"}'`,
			wantKey: "sk-nopexp",
		},
		"json with extra fields keeps cache canonical": {
			body:    `printf '{"key":"sk-extra","expires_at":"` + future + `","refresh_token":"rt"}'`,
			wantKey: "sk-extra",
		},
		"json with explicit version 1": {
			body:    `printf '{"version":1,"key":"sk-v1","expires_at":"` + future + `"}'`,
			wantKey: "sk-v1",
		},
		"json with unsupported version rejected": {
			body:       `printf '{"version":2,"key":"sk-v2","expires_at":"` + future + `"}'`,
			wantReason: ReasonJSONParse,
			wantErr:    true,
		},
		"json with past expiry rejects fresh as expired": {
			body:       `printf '{"key":"sk-stale","expires_at":"` + past + `"}'`,
			wantReason: ReasonExpired,
			wantErr:    true,
		},
		"json with malformed expires_at": {
			body:       `printf '{"key":"sk-x","expires_at":"not-rfc3339"}'`,
			wantReason: ReasonInvalidExpiration,
			wantErr:    true,
		},
		"json missing key": {
			body:       `printf '{"expires_at":"` + future + `"}'`,
			wantReason: ReasonJSONParse,
			wantErr:    true,
		},
		"json with trailing garbage": {
			body:       `printf '{"key":"sk-x"} extra'`,
			wantReason: ReasonJSONParse,
			wantErr:    true,
		},
		"plain string passthrough": {
			body:    `printf 'sk-plain-token'`,
			wantKey: "sk-plain-token",
		},
		"plain string with internal newline rejected": {
			body:       `printf 'debug line\nsk-broken'`,
			wantReason: ReasonInvalidPlainOutput,
			wantErr:    true,
		},
		"empty stdout rejected": {
			body:       `printf ''`,
			wantReason: ReasonEmptyOutput,
			wantErr:    true,
		},
		"non-zero exit rejected": {
			body:       `printf 'unused'; exit 7`,
			wantReason: ReasonCommandExit,
			wantErr:    true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			withCacheRoot(t)
			path := helperScript(t, tc.body)
			res, err := Resolve(context.Background(), opts(path))
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v (reason=%q)", err, tc.wantErr, res.Reason)
			}
			if res.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", res.Reason, tc.wantReason)
			}
			if !tc.wantErr && res.Key != tc.wantKey {
				t.Fatalf("key = %q, want %q", res.Key, tc.wantKey)
			}
		})
	}
}

func TestResolveCommandCachesAndReuses(t *testing.T) {
	root := withCacheRoot(t)

	// Helper writes one credential per invocation and appends to a
	// counter file we can inspect to verify caching: the first
	// Resolve should exec, the second should be served from cache.
	dir := t.TempDir()
	counter := filepath.Join(dir, "count")
	future := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	body := `echo x >> "` + counter + `"
printf '{"key":"sk-cache","expires_at":"` + future + `"}'`
	cmd := helperScript(t, body)

	first, err := Resolve(context.Background(), opts(cmd))
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if first.Key != "sk-cache" || first.Source != SourceCommand {
		t.Fatalf("first: key=%q source=%q", first.Key, first.Source)
	}
	second, err := Resolve(context.Background(), opts(cmd))
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if second.Key != "sk-cache" || second.Source != SourceCache {
		t.Fatalf("second: key=%q source=%q (want cache hit)", second.Key, second.Source)
	}

	bytes, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	if got := strings.Count(string(bytes), "x\n"); got != 1 {
		t.Fatalf("helper executed %d times, want 1 (counter=%q)", got, string(bytes))
	}

	// Cache layout: $XDG_CACHE_HOME/ccgate/<target>/api_key.<hash>.json
	cacheDir := filepath.Join(root, "ccgate", "claude")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	var found bool
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "api_key.") || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("cache file perm = %o, want 0600", info.Mode().Perm())
		}
		raw, err := os.ReadFile(filepath.Join(cacheDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var got helperPayload
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("cache file not valid json: %v", err)
		}
		// Canonicalisation: only {version, key, expires_at} remain.
		if got.Key != "sk-cache" || got.ExpiresAt != future || got.Version != 1 {
			t.Fatalf("cache payload = %+v, want canonical {version:1, key:sk-cache, expires_at:%s}", got, future)
		}
		// Make sure no extra top-level fields slipped in (e.g.
		// refresh_token from a real broker).
		var keys map[string]any
		if err := json.Unmarshal(raw, &keys); err != nil {
			t.Fatal(err)
		}
		for k := range keys {
			switch k {
			case "version", "key", "expires_at":
			default:
				t.Fatalf("unexpected cache field %q", k)
			}
		}
		found = true
	}
	if !found {
		t.Fatalf("no cache file under %s", cacheDir)
	}
}

func TestResolveFile(t *testing.T) {
	withCacheRoot(t)

	future := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)

	cases := map[string]struct {
		body       string
		wantKey    string
		wantReason Reason
		wantErr    bool
	}{
		"json future":        {body: `{"key":"sk-file","expires_at":"` + future + `"}`, wantKey: "sk-file"},
		"json past":          {body: `{"key":"sk-file","expires_at":"` + past + `"}`, wantReason: ReasonExpired, wantErr: true},
		"plain":              {body: `sk-file-plain`, wantKey: "sk-file-plain"},
		"plain whitespace":   {body: "  \n\t ", wantReason: ReasonEmptyOutput, wantErr: true},
		"plain multi-line":   {body: "debug\nsk-file\n", wantReason: ReasonInvalidPlainOutput, wantErr: true},
		"json missing key":   {body: `{"expires_at":"` + future + `"}`, wantReason: ReasonJSONParse, wantErr: true},
		"json bad expiry":    {body: `{"key":"sk-file","expires_at":"not-rfc3339"}`, wantReason: ReasonInvalidExpiration, wantErr: true},
		"json trailing junk": {body: `{"key":"sk-file"} extra`, wantReason: ReasonJSONParse, wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "key")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			res, err := Resolve(context.Background(), Options{
				File:           path,
				ProviderName:   "test",
				TargetName:     "claude",
				RefreshMargin:  30 * time.Second,
				CommandTimeout: 5 * time.Second,
			})
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v (reason=%q)", err, tc.wantErr, res.Reason)
			}
			if res.Reason != tc.wantReason {
				t.Fatalf("reason = %q, want %q", res.Reason, tc.wantReason)
			}
			if !tc.wantErr && res.Key != tc.wantKey {
				t.Fatalf("key = %q, want %q", res.Key, tc.wantKey)
			}
		})
	}
}

func TestResolveFileMissing(t *testing.T) {
	withCacheRoot(t)

	res, err := Resolve(context.Background(), Options{
		File:           filepath.Join(t.TempDir(), "absent"),
		ProviderName:   "test",
		TargetName:     "claude",
		RefreshMargin:  30 * time.Second,
		CommandTimeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if res.Reason != ReasonFileMissing {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonFileMissing)
	}
	if res.Source != SourceFile {
		t.Fatalf("source = %q, want file", res.Source)
	}
}

func TestResolveCommandTimeout(t *testing.T) {
	withCacheRoot(t)

	cmd := helperScript(t, `sleep 5; printf 'late'`)
	o := opts(cmd)
	o.CommandTimeout = 200 * time.Millisecond

	start := time.Now()
	res, err := Resolve(context.Background(), o)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if res.Reason != ReasonTimeout {
		t.Fatalf("reason = %q, want %q (err=%v)", res.Reason, ReasonTimeout, err)
	}
	// WaitDelay caps how much we slip past the timeout. Allow a
	// generous upper bound (timeout + WaitDelay + a little CI slack)
	// while still failing if we wait the full 5s.
	if elapsed > 2*time.Second {
		t.Fatalf("took %s; expected to be killed near the 200ms timeout", elapsed)
	}
}

func TestResolveCommandStaleCacheRefreshes(t *testing.T) {
	withCacheRoot(t)

	// Pre-seed the cache with an already-expired payload so the
	// fast-path check rejects it; the helper must run and replace
	// the entry. We compute the path the same way Resolve does.
	o := opts("printf '{\"key\":\"sk-fresh\",\"expires_at\":\"" +
		time.Now().Add(2*time.Hour).UTC().Format(time.RFC3339) + "\"}'")
	o.Command = helperScript(t, `printf '{"key":"sk-fresh","expires_at":"`+
		time.Now().Add(2*time.Hour).UTC().Format(time.RFC3339)+`"}'`)
	path, err := cachePath(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := helperPayload{
		Version:   1,
		Key:       "sk-stale",
		ExpiresAt: time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339),
	}
	body, _ := json.Marshal(stale)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Resolve(context.Background(), o)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Key != "sk-fresh" {
		t.Fatalf("key = %q, want sk-fresh (helper should have refreshed stale cache)", res.Key)
	}
	if res.Source != SourceCommand {
		t.Fatalf("source = %q, want command", res.Source)
	}
}

func TestResolveCommandCorruptCacheRecovers(t *testing.T) {
	withCacheRoot(t)

	o := opts(helperScript(t, `printf '{"key":"sk-recover","expires_at":"`+
		time.Now().Add(2*time.Hour).UTC().Format(time.RFC3339)+`"}'`))
	path, err := cachePath(o)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Resolve(context.Background(), o)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if res.Key != "sk-recover" {
		t.Fatalf("key = %q, want sk-recover (helper should refresh after corrupt cache unlinked)", res.Key)
	}
}

func TestInvalidateRemovesCache(t *testing.T) {
	withCacheRoot(t)

	o := opts(helperScript(t, `printf '{"key":"sk","expires_at":"`+
		time.Now().Add(1*time.Hour).UTC().Format(time.RFC3339)+`"}'`))
	if _, err := Resolve(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	path, err := cachePath(o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cache file missing after resolve: %v", err)
	}
	if err := Invalidate(o); err != nil {
		t.Fatalf("invalidate: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cache file still present after invalidate: %v", err)
	}
	// Idempotent: invalidating an already-removed cache is fine.
	if err := Invalidate(o); err != nil {
		t.Fatalf("invalidate idempotent: %v", err)
	}
}

// TestResolveFileTooLarge ensures we reject pathologically large
// `api_key_file` paths (e.g. /dev/zero, runaway log file). Without
// the bounded reader the hot path would either OOM or hang.
func TestResolveFileTooLarge(t *testing.T) {
	withCacheRoot(t)

	path := filepath.Join(t.TempDir(), "huge")
	body := bytesRepeat('x', stdoutLimit+10)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Resolve(context.Background(), Options{
		File:           path,
		ProviderName:   "test",
		TargetName:     "claude",
		RefreshMargin:  30 * time.Second,
		CommandTimeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
	if res.Reason != ReasonOutputTooLarge {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonOutputTooLarge)
	}
}

// TestResolveFileNonRegularRejected makes sure a `api_key_file`
// pointed at a directory (or any non-regular file) does not block
// the hot path with an unbounded read.
func TestResolveFileNonRegularRejected(t *testing.T) {
	withCacheRoot(t)

	// A directory is a non-regular file; using one as the file path
	// is the most portable misconfiguration we can simulate without
	// platform-specific FIFO/device tricks.
	dir := t.TempDir()
	res, err := Resolve(context.Background(), Options{
		File:           dir,
		ProviderName:   "test",
		TargetName:     "claude",
		RefreshMargin:  30 * time.Second,
		CommandTimeout: 5 * time.Second,
	})
	if err == nil {
		t.Fatal("expected error for non-regular file (directory)")
	}
	if res.Reason != ReasonFileRead {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonFileRead)
	}
}

// TestResolveCommandTightensExistingCacheDirPerm guards Medium #1
// from the holistic review: an existing 0755 ccgate cache dir
// (e.g. left behind by an older release) must be chmod'd back to
// 0700 before we drop a credential file in there.
func TestResolveCommandTightensExistingCacheDirPerm(t *testing.T) {
	root := withCacheRoot(t)

	// Pre-create the per-target cache dir at 0755 so we can verify
	// Resolve tightens it. Using the runtime path layout here
	// instead of computing it ourselves so a future move catches us.
	cacheDir := filepath.Join(root, "ccgate", "claude")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := helperScript(t, `printf '{"key":"sk-tight","expires_at":"`+
		time.Now().Add(2*time.Hour).UTC().Format(time.RFC3339)+`"}'`)

	if _, err := Resolve(context.Background(), opts(cmd)); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	info, err := os.Stat(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("cache dir perm = %o, want 0700 (resolve must tighten loose existing dirs)", perm)
	}
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func TestExpandHomePath(t *testing.T) {

	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := map[string]struct {
		in   string
		want string
	}{
		"absolute":       {in: "/etc/key", want: "/etc/key"},
		"home alone":     {in: "~", want: home},
		"home prefix":    {in: "~/key", want: filepath.Join(home, "key")},
		"home subdir":    {in: "~/.config/x", want: filepath.Join(home, ".config", "x")},
		"non-tilde leaf": {in: "/var/lib/key", want: "/var/lib/key"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := expandHomePath(tc.in)
			if err != nil {
				t.Fatalf("expand %q: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("expand %q = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
