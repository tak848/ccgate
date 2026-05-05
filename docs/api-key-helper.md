# Short-lived / rotating API keys

[日本語版 (docs/ja/api-key-helper.md)](ja/api-key-helper.md)

When the credential the provider needs rotates faster than a static env var can keep up — AWS STS sessions, Vertex ADC, OpenAI-compatible gateway virtual keys, internal key brokers — ccgate can resolve it through `provider.auth` instead of `*_API_KEY`.

This document is the full reference. The README has the minimum config snippet and a single example; everything else (helper contract, caching semantics, account isolation, security guidance, recovery checklist) lives here.

## Output formats

The helper writes one of two shapes on stdout (or, for `auth.type=file`, into the file):

- **JSON**: `{"key":"sk-...","expires_at":"2026-05-04T01:23:45Z"}`. Parsed strictly. `key` is required; `expires_at` is optional. Unknown top-level fields (e.g. broker metadata) are accepted but dropped — only the canonical `{key, expires_at}` reaches the cache or the SDK.
- **Plain string**: a single non-empty line. Surrounding whitespace is trimmed; the trimmed value is forwarded to the SDK.

`expires_at` is RFC3339. Helper output exceeding 64 KiB is rejected as `output_too_large`. The same 64 KiB cap applies to file content.

How caching applies depends on the source:

- For `auth.type=exec`: JSON with a future `expires_at` is memoized to a per-target cache file (see [Caching](#caching)) and refreshed early via `auth.refresh_margin_ms`. JSON without `expires_at` and plain string output are accepted but **not cached** — the helper re-runs on every hook invocation.
- For `auth.type=file`: ccgate reads the file on every hook invocation and does not maintain an internal cache. The external rotator owns when the credential is refreshed.

`auth.refresh_margin_ms` is also enforced as a *minimum-remaining-TTL* guard on freshly produced credentials (helper exec output and file contents alike). A credential that would expire inside the margin window surfaces as `expired` instead of being handed to the SDK to race the next API call.

## Config

```jsonnet
// auth.type=exec: ccgate runs the command and reads stdout
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    auth: {
      type: 'exec',
      command: '/usr/local/bin/my-key-broker --provider anthropic',
      refresh_margin_ms: 60000, // optional, default 60000
      timeout_ms: 5000,         // optional, default 5000
      cache_key: '${AWS_PROFILE}', // optional; see Account isolation
    },
  },
}

// auth.type=file: an external rotator writes the credential
{
  provider: {
    name: 'anthropic',
    auth: {
      type: 'file',
      path: '~/.config/my-broker/anthropic.json',
      refresh_margin_ms: 60000, // optional, default 60000
    },
  },
}
```

| Field | Type | Default | What it does |
|---|---|---|---|
| `auth.type` | `"exec"` / `"file"` | (required when `auth` is set) | Discriminator. Selects which other fields are valid. |
| `auth.command` | string | `""` | (`type=exec` only, required) Shell command run via `/bin/sh -c` whose stdout is the credential. |
| `auth.path` | string (abs or `~/`) | `""` | (`type=file` only, required) Local regular file whose contents follow the same JSON / plain-string shape. |
| `auth.refresh_margin_ms` | int (ms) | `60000` | Cache is stale once `now + margin >= expires_at` (`type=exec`); minimum-remaining-TTL guard on freshly produced credentials (both branches). `>= 0` (`0` disables the guard). |
| `auth.timeout_ms` | int (ms) | `5000` | (`type=exec` only) Hot-path upper bound for one helper invocation. `> 0`. |
| `auth.cache_key` | string | `""` | (`type=exec` only) Secret-free salt added to the cache fingerprint; supports `${VAR}` env expansion. See [Account isolation](#cache-key-and-account-isolation). |

The `provider` block is replaced atomically across config layers, so a project-local config that restates `provider` must repeat the `auth` block — otherwise the helper config is silently dropped on that project.

### `auth.type=file` is local-FS only

`auth.path` is a **best-effort, local regular file** contract. Local POSIX filesystems (XFS, ext4, APFS, HFS+) are supported; NFS / SMB / FUSE / keychain mounts are explicitly **unsupported** because Go's `os.File.SetDeadline` does not apply to regular files, and we cannot impose a hard read deadline on a slow remote mount without leaking goroutines. If you need a hard timeout on credential reads, switch to `auth.type=exec` (the helper runs under `auth.timeout_ms`).

ccgate opens the file with `O_NONBLOCK` so a misconfigured FIFO / device returns immediately instead of wedging the hook, but a slow regular file on a stalled NFS mount can still hang for the duration of the kernel I/O.

### File permissions

ccgate emits a `slog.Warn` (no hard reject) when `auth.path`:

- has any `group` or `other` read bit set (`mode & 0o044`), or
- is owned by a UID different from the current user.

The recommendation is `chmod 0600 <path>` inside a `chmod 0700` parent directory. The warning is informational — if you have a deliberate reason for looser permissions, ignore it.

## Resolution order and platform support

`provider.auth` (configured) > `CCGATE_*_API_KEY` > `*_API_KEY`.

When `auth` is configured ccgate will **not** silently fall back to env vars on failure: that would mask the helper bug. Instead the hook falls through with `kind=credential_unavailable` and a reason that pinpoints which step failed (see [docs/configuration.md](configuration.md) for the full reason taxonomy).

`auth` is Unix only (Linux, macOS, *BSD). On Windows configuring it falls through with `reason=unsupported_platform`; users who do not configure `auth` keep using the regular `*_API_KEY` env-var path unchanged. ccgate does **not** silently re-route a configured-but-unsupported `auth` block to env vars.

## Caching

- Path: `$XDG_CACHE_HOME/ccgate/<target>/api_key.<sha256[:16]>.json` (target = `claude` / `codex`). Falls back to `~/.cache/ccgate/<target>/...` when `XDG_CACHE_HOME` is unset.
- Permissions: directory `0700`, file `0600`. ccgate `chmod`s the directory back to `0700` even if it pre-existed at a looser mode.
- Cache content is the canonical `{key, expires_at}` only — extra fields the helper printed (refresh tokens, broker session IDs) are dropped on write so they do not get persisted to disk.
- Atomic rename: temp file is created in the same directory and renamed into place. Cross-filesystem rename pitfalls do not apply.
- Concurrent fires are serialised by `flock` on a sibling lock file (`*.lock`). The lock file is never deleted; its presence is normal.

### Cache key and account isolation

The cache fingerprint is built from `(target, provider.name, base_url, auth.command, auth.cache_key)` only — environment variables are **not** part of the hash by default. If your helper depends on `$AWS_PROFILE`, `$GCLOUD_ACCOUNT`, `$OP_ACCOUNT`, etc., a literal `auth.command: 'aws sts ...'` would otherwise share the same cache file across every account.

Three ways to isolate accounts:

- **Use `auth.cache_key` with the jsonnet `std.native('env')` helper**: `auth: { type: 'exec', command: 'aws sts ...', cache_key: std.native('env')('AWS_PROFILE') }`. ccgate registers `std.native('env')` (returns empty for undefined variables) and `std.native('must_env')` (raises a jsonnet evaluation error for undefined variables) so config-load resolves env values at the same time everything else is evaluated. The resolved string lands in the cache fingerprint as-is.
- **Bake the account into the command string**: `auth.command: 'aws sts assume-role --profile prod ...'`. Different command strings hash to different cache files, so two project-local configs aimed at different accounts stay isolated. Works without any env machinery.
- **Use `auth.type=file` per account**: each account's rotator writes to its own path; the path itself separates the credentials.

## Security guidance

- `auth.path`: ccgate emits a permission warning but does not normalise the mode. Set `chmod 0600 <path>` and put the file under a `chmod 0700` directory yourself.
- `auth.command`: do **not** put a literal secret into the command string. The string is passed to `/bin/sh -c`, so it appears in `ps`, `/proc/<pid>/cmdline`, audit logs, and shell history. Wrap secret material in a file or keychain that the helper reads internally.
- Helper stderr body is **never** written to `ccgate.log`. ccgate captures stderr internally to bound memory but logs only the byte count and exit error on failure. Re-run the helper manually with `2>&1` if you need to inspect its diagnostic output.
- Provider error response bodies are redacted before they reach `ccgate.log` and `metrics.jsonl`. Both `anthropic-sdk-go` and `openai-go` embed the response body in `Error.Error()`; ccgate replaces that with a short `<provider> API error (status N)` summary so a misbehaving proxy cannot leak credentials through the log.

## Helper contract

The helper must:

- Be **non-interactive** (no TTY input, no browser open; stdin is closed).
- **Not daemonize**: forking past the process group escapes the timeout-kill.
- Write **only the credential** on stdout. Diagnostics belong on stderr — and even there, never put secrets, since some operators capture stderr.
- For plain-string mode: the trimmed stdout must be a single non-empty line. Multi-line output is rejected as `invalid_plain_output`.
- Be **deterministic** for the same `(auth.command, provider.name, base_url, auth.cache_key)` tuple: two callers with the same config must agree on what the credential is for.

ccgate exports `CCGATE_API_KEY_RESOLUTION=1` into the helper environment so a helper that wraps ccgate can detect recursive invocation. All other environment variables (including `*_API_KEY`) are inherited so wrappers that read existing credentials keep working.

## Helper examples

### Plain string: wrap an existing env-var credential

The simplest helper just echoes a credential the operator already has in an env var. Useful for testing the resolution path before wiring a real broker:

```sh
#!/bin/sh
# ~/bin/ccgate-key-passthrough.sh
set -eu
printf '%s' "${ANTHROPIC_API_KEY:?ANTHROPIC_API_KEY is not set}"
```

Then `chmod 700 ~/bin/ccgate-key-passthrough.sh` and `auth: { type: 'exec', command: '~/bin/ccgate-key-passthrough.sh' }`. ccgate runs this on every hook invocation (no caching, since plain string has no `expires_at`) and forwards the env value to the SDK.

### JSON with expiry: cache through a broker

When a real broker mints time-limited credentials, wrap the response in `{key, expires_at}` so ccgate can cache and refresh just before expiry. Build the JSON with `jq` so a token containing `"`, `\`, or newlines cannot break the payload:

```sh
#!/bin/sh
# ~/bin/ccgate-key-broker.sh
set -eu
TOKEN=$(my-key-broker --provider anthropic) # outputs the API key
# Pick an `expires_at` slightly inside the broker's TTL so the
# refresh_margin_ms (default 60000) has slack.
EXP=$(date -u -v+50M +%FT%TZ 2>/dev/null || date -u -d '+50 minutes' +%FT%TZ)
jq -nc --arg key "$TOKEN" --arg expires_at "$EXP" '{key:$key, expires_at:$expires_at}'
```

`auth: { type: 'exec', command: '~/bin/ccgate-key-broker.sh' }`. Test the script standalone first (`~/bin/ccgate-key-broker.sh | jq .` should print a JSON object) before handing it to ccgate.

### AWS profile separation via `cache_key`

When the same broker command returns a different credential per AWS profile, add `cache_key` so each profile gets its own cache file:

```jsonnet
{
  provider: {
    name: 'anthropic',
    auth: {
      type: 'exec',
      command: 'aws-sts-broker --provider anthropic',
      cache_key: std.native('must_env')('AWS_PROFILE'),
    },
  },
}
```

Switching `AWS_PROFILE=prod` and `AWS_PROFILE=dev` between hook fires now produces two separate cache files (`api_key.<hash-prod>.json` and `api_key.<hash-dev>.json`) instead of overwriting one another. `must_env` raises a jsonnet evaluation error if `AWS_PROFILE` is unset, so a misconfiguration surfaces at config-load time rather than as a silently shared credential.

### `auth.type=file` rotator: hot-path-free

When you want zero exec cost on the hook, schedule an external rotator to write the same JSON shape to a file, atomically:

```sh
#!/bin/sh
# Run from cron / launchd / systemd-timer.
set -eu
TOKEN=$(my-key-broker --provider anthropic)
EXP=$(date -u -v+1H +%FT%TZ 2>/dev/null || date -u -d '+1 hour' +%FT%TZ)
TMP=$(mktemp ~/.config/my-broker/anthropic.json.XXXXXX)
jq -nc --arg key "$TOKEN" --arg expires_at "$EXP" '{key:$key, expires_at:$expires_at}' > "$TMP"
chmod 0600 "$TMP"
mv "$TMP" ~/.config/my-broker/anthropic.json
```

Then `auth: { type: 'file', path: '~/.config/my-broker/anthropic.json' }`. ccgate just reads the file on every hook invocation — there is no internal cache to refresh, the rotator owns rotation.

## Provider 401/403 behaviour

When the provider rejects the credential ccgate just used, the HTTP status alone determines the reaction.

| HTTP status         | `auth.type=exec`                          | `auth.type=file`                         | env var      |
|---------------------|-------------------------------------------|------------------------------------------|--------------|
| 401 / 403           | `provider_auth`, **invalidate cache + fallthrough** | `provider_auth`, fallthrough only (no cache) | **exit 1**   |
| 5xx / network / 429 | exit 1 (existing behaviour)               | exit 1                                   | exit 1       |

The env-var path keeps the existing exit-1 behaviour on 401/403 because ccgate cannot rotate env vars; swallowing the rejection would hide a real user-side configuration error.

## Differences from AWS `credential_process`

The output shape is intentionally close to AWS `credential_process` so existing helpers can be adapted with a one-line wrapper, but ccgate **memoizes** the helper output to disk while the AWS CLI re-execs on every call. That trade favours hot-path latency (hooks fire dozens of times per session) at the cost of one extra "is this credential actually still valid?" answer in your helper.

If your broker does not want callers to memoize, return JSON without `expires_at` (`{"key":"..."}`) and ccgate will re-exec every time.

## Recovery checklist

When something looks wrong:

1. `tail` `ccgate.log` (`$XDG_STATE_HOME/ccgate/<target>/ccgate.log`) and look for entries with `kind=credential_unavailable`. Read the `reason` and `source` (`exec` / `file` / `cache` / `lock`) attributes — they pinpoint which step failed.
2. Run `ccgate <target> metrics` and inspect the **Credential failures** section. It groups failures by `(source, reason)`.
3. If the failure looks cache-related (`cache_parse` / `cache_read` / `cache_write` log warnings), remove `$XDG_CACHE_HOME/ccgate/<target>/api_key.*.json` to force a refresh. The sibling `*.lock` files are reused — leave them alone.
4. If `expired` keeps appearing, compare the helper's `expires_at` with `date -u`. Clock skew or a broken TTL inside the helper is the usual cause; a margin smaller than the helper's TTL would also produce this.
5. If `provider_auth` keeps coming back even with cache invalidation, the helper itself is producing a credential the provider rejects. Re-run `/bin/sh -c "$your_command"` manually and verify the output — the same stdout the helper writes is what reached the SDK.

The full reason taxonomy (with the difference between metrics fallthrough reasons and log-only cache warnings) is in [docs/configuration.md](configuration.md#reason-values-for-credential_unavailable).
