# Short-lived / rotating API keys

[日本語版 (docs/ja/api-key-helper.md)](ja/api-key-helper.md)

When the credential the provider needs rotates faster than a static env var can keep up — AWS STS sessions, Vertex ADC, OpenAI-compatible gateway virtual keys, internal key brokers — ccgate resolves it through `provider.auth` instead of `*_API_KEY`.

`provider.auth` has two modes:

- **`type: 'exec'`**: ccgate runs a shell command and uses its stdout as the credential.
- **`type: 'file'`**: ccgate reads a file written by an external rotator.

Linux, macOS, *BSD, and Windows are supported. The shell that runs the helper command is selected per-config via `auth.shell` (default `bash`); set `shell: 'powershell'` on Windows boxes that don't have bash.

## Config

```jsonnet
// Helper command
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    auth: {
      type: 'exec',
      command: '/usr/local/bin/my-key-broker --provider anthropic',
      refresh_margin_ms: 60000,  // optional, default 60000
      timeout_ms: 30000,         // optional, default 30000
    },
  },
}

// External rotator writes a file (path optional; defaults to
// $XDG_STATE_HOME/ccgate/<target>/auth_key.json)
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    auth: {
      type: 'file',
      path: '~/.config/my-broker/anthropic.json',
      refresh_margin_ms: 60000,  // optional, default 60000
    },
  },
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| Field | Type | Default | Description |
|---|---|---|---|
| `auth.type` | `"exec"` / `"file"` | (required when `auth` is set) | Resolution mode. |
| `auth.command` | string | `""` | (`exec` only, required) Shell command; stdout is the credential. |
| `auth.shell` | `"bash"` / `"powershell"` | `"bash"` | (`exec` only) Shell. `powershell` tries `pwsh` first, falls back to `powershell`. |
| `auth.path` | string | `$XDG_STATE_HOME/ccgate/<target>/auth_key.json` | (`file` only) Credential file path. Omit to use the default. |
| `auth.refresh_margin_ms` | int (ms) | `60000` | Treat credentials as expired this many ms before `expires_at`. `0` disables. |
| `auth.timeout_ms` | int (ms) | `30000` | Hard cap on one Resolve call. `> 0`. |
| `auth.cache_key` | string | `""` | (`exec` only) Salt added to the cache fingerprint. See [Account isolation](#account-isolation). |

Relative paths in `auth.command` and `auth.path` resolve from the hook's working directory at fire time, not from the config file's directory.

Credential resolution order: `provider.auth` (when configured) > `CCGATE_*_API_KEY` > `*_API_KEY`. A configured `auth` block does not fall back to env vars on failure; the hook falls through with `kind=credential_unavailable` so the issue surfaces.

## Helper output

The helper writes one of two shapes on stdout (or, for `auth.type=file`, into the file):

- **JSON**: `{"key": "sk-...", "expires_at": "2026-05-04T01:23:45Z"}`. `key` is required; `expires_at` is RFC3339 and optional. Unknown top-level fields are accepted but dropped — only `{key, expires_at}` is forwarded or cached.
- **Plain string**: a single non-empty line. Surrounding whitespace is trimmed; multi-line output is rejected.

Output larger than 64 KiB is rejected; the same cap applies to file content.

## Helper contract

A helper must:

- Write **only the credential** on stdout. Diagnostics belong on stderr, and never put secrets there either.
- Be **deterministic** for the same `(shell, command, provider.name, base_url, cache_key)` tuple. Two callers with the same config must agree on what the credential is.
- **Not daemonize**. Forking past the process group escapes the timeout-kill.
- Finish within `auth.timeout_ms`.
- Avoid literal secrets in `auth.command`. The string is passed to the configured shell (`bash -c <command>`, or `pwsh -Command <command>` / `powershell -Command <command>` when `auth.shell: 'powershell'`) and shows up in `ps`, `/proc/<pid>/cmdline`, and shell history. Read secrets from a file or keychain inside the helper instead.

ccgate exports `CCGATE_API_KEY_RESOLUTION=1` into the helper environment so a wrapper helper can detect recursive invocation. All other environment variables (including `*_API_KEY`) are inherited. Stdin is closed; the helper cannot read from the parent terminal.

### Browser-based first-run auth

Helpers like `gcloud auth print-access-token`, `aws sso login`, or an internal SSO key broker open a browser on first run, complete OAuth / SAML in the browser, then print the credential on stdout. Subsequent runs read a cached refresh token and are silent.

This pattern is supported. The default `auth.timeout_ms` (`30000`) covers most non-interactive helpers; for browser-based first-run flows that involve the user clicking through a consent screen, raise it to e.g. `120000` so the first Permission Request after a long idle does not surface as `reason=timeout`.

## Examples

### Wrap an existing env-var credential

The simplest helper echoes a credential the operator already has in an env var. Useful for testing the resolution path before wiring a real broker:

```sh
#!/bin/sh
# ~/bin/ccgate-key-passthrough.sh
set -eu
printf '%s' "${ANTHROPIC_API_KEY:?ANTHROPIC_API_KEY is not set}"
```

```jsonnet
auth: { type: 'exec', command: '~/bin/ccgate-key-passthrough.sh' }
```

Plain-string output is not cached, so the helper runs on every hook invocation.

### Cache through a broker

When a real broker mints time-limited credentials, wrap the response in `{key, expires_at}` so ccgate can cache and refresh just before expiry. Build the JSON with `jq` so a token containing `"`, `\`, or newlines cannot break the payload:

```sh
#!/bin/sh
# ~/bin/ccgate-key-broker.sh
set -eu
TOKEN=$(my-key-broker --provider anthropic)
EXP=$(date -u -v+50M +%FT%TZ 2>/dev/null || date -u -d '+50 minutes' +%FT%TZ)
jq -nc --arg key "$TOKEN" --arg expires_at "$EXP" '{key:$key, expires_at:$expires_at}'
```

```jsonnet
auth: { type: 'exec', command: '~/bin/ccgate-key-broker.sh' }
```

Test the script standalone first (`~/bin/ccgate-key-broker.sh | jq .` should print a JSON object) before handing it to ccgate.

### External rotator (no helper exec on the hook path)

When you want zero exec cost on the hook, schedule an external rotator (cron / launchd / systemd-timer) to write the same JSON shape atomically:

```sh
#!/bin/sh
set -eu
TOKEN=$(my-key-broker --provider anthropic)
EXP=$(date -u -v+1H +%FT%TZ 2>/dev/null || date -u -d '+1 hour' +%FT%TZ)
TMP=$(mktemp ~/.config/my-broker/anthropic.json.XXXXXX)
jq -nc --arg key "$TOKEN" --arg expires_at "$EXP" '{key:$key, expires_at:$expires_at}' > "$TMP"
chmod 0600 "$TMP"
mv "$TMP" ~/.config/my-broker/anthropic.json
```

```jsonnet
auth: { type: 'file', path: '~/.config/my-broker/anthropic.json' }
```

The rotator owns refresh; ccgate just reads the file on each hook invocation.

## Caching

`auth.type=exec` JSON output with a future `expires_at` is memoized at `$XDG_CACHE_HOME/ccgate/<target>/api_key.<sha256[:16]>.json` (directory `0700`, file `0600`). The cached entry goes stale once `now + auth.refresh_margin_ms >= expires_at`, at which point the next hook invocation re-runs the helper. Concurrent hook invocations are serialised by `flock` on a sibling lock file so the helper still runs only once.

JSON without `expires_at`, plain-string output, and `auth.type=file` are never cached on ccgate's side.

## Account isolation

The cache fingerprint includes `auth.cache_key`, so two configs with the same `auth.command` but a different `cache_key` get separate cache files. Use it when one helper command returns a different credential per AWS profile / GCP account / etc:

```jsonnet
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    auth: {
      type: 'exec',
      command: 'aws-sts-broker --provider anthropic',
      cache_key: std.native('must_env')('AWS_PROFILE'),
    },
  },
}
```

ccgate registers two jsonnet helpers for reading env vars at config-load time:

- `std.native('env')(name)`: returns the env value, or `""` when unset.
- `std.native('must_env')(name)`: raises a jsonnet evaluation error when unset.

Alternatively, bake the account into the command string (`aws sts ... --profile prod`) — different command strings hash to different cache files automatically — or use `auth.type=file` with one path per account.

The cache fingerprint does **not** include the hook's working directory or the host. Concretely: **all checkouts of the same repo, all repos with the same `(provider.name, base_url, shell, command, cache_key)` tuple, share one cache file** — even on different machines if `$XDG_CACHE_HOME` happens to point at a synced directory. That is usually what you want (re-fetching the same credential per checkout is wasteful), but when you actively want separation set `cache_key` to whatever you want to scope by:

- per-checkout: `cache_key: std.native('env')('PWD')`
- per-host: `cache_key: std.native('env')('HOSTNAME')`
- per-account-via-env: `cache_key: std.native('must_env')('AWS_PROFILE')`

Leaving `cache_key` empty is the explicit "share with anything that has the same helper command" choice.

## File mode notes

`auth.path` reads are bounded by `auth.timeout_ms` (default 30000) just like the exec branch — a stalled mount surfaces as `reason=timeout` instead of blocking the hook. Local regular files are still strongly recommended (NFS / SMB / FUSE / keychain mounts will time out reliably but each fire pays a `timeout_ms` wait); a per-user local path is the cheapest path.

ccgate emits a `slog.Warn` when `auth.path` *or* the cache file has any group/other read bit set, or is owned by a different UID than the current user (Unix), or grants direct read access to the `Everyone` / `BuiltinUsers` SIDs (Windows). Both checks are best-effort security nudges, not policy enforcement: the Windows DACL walk only inspects allow ACEs to those well-known SIDs and does not compute effective access, deny ACEs, or inheritance. The recommended setup is `chmod 0600` on Unix inside a `chmod 0700` parent, or a per-user-only ACL on Windows.

## Provider 401/403 behaviour

When the provider rejects the credential ccgate just used, the HTTP status alone determines the reaction.

| HTTP status         | `auth.type=exec`                          | `auth.type=file`                         | env var      |
|---------------------|-------------------------------------------|------------------------------------------|--------------|
| 401 / 403           | `provider_auth`, **invalidate cache + fallthrough** | `provider_auth`, fallthrough only (no cache) | **exit 1**   |
| 5xx / 429 / network | exit 1 (existing behaviour)               | exit 1                                   | exit 1       |

The env-var path keeps the existing exit-1 behaviour on 401/403 because ccgate cannot rotate env vars; swallowing the rejection would hide a user-side configuration error.

## Relationship to AWS `credential_process` and kubectl exec credential plugins

`provider.auth` is shaped after the same family of credential helpers, but it is not a drop-in for any of them:

- **AWS `credential_process`** prints `{"Version":1, "AccessKeyId", "SecretAccessKey", "SessionToken", "Expiration"}` for SigV4 callers, and the AWS CLI re-execs the helper every call. ccgate prints `{"key", "expires_at"}` for an Authorization-header bearer, and memoizes to disk. An AWS-style helper needs a thin adapter that picks the right field and reshapes the JSON.
- **kubectl exec credential plugin** uses a separate `command` + `args` pair and prints an `ExecCredential` shape. ccgate uses a single shell-form `command` (matching the Claude Code / Codex hook conventions ccgate plugs into) and the JSON shape above.

To opt out of caching, return JSON without `expires_at` (or plain string) and the helper re-runs every fire.

## Recovery checklist

1. `tail $XDG_STATE_HOME/ccgate/<target>/ccgate.log` and look for entries with `kind=credential_unavailable`. The `reason` and `source` (`exec` / `file` / `cache` / `lock`) attributes pinpoint the failed step.
2. Run `ccgate <target> metrics` and check the **Credential failures** section, which groups failures by `(source, reason)`.
3. For `cache_parse` / `cache_read` / `cache_write` (log-only warnings), remove `$XDG_CACHE_HOME/ccgate/<target>/api_key.*.json` to force a refresh. Leave the sibling `*.lock` files alone.
4. For `expired`, compare the helper's `expires_at` with `date -u`. Clock skew or a broken TTL inside the helper is the usual cause.
5. For `command_exit` on a fresh setup, check first whether the configured `auth.shell` binary is on `$PATH`. `bash` is universal on Linux / macOS; for `powershell`, at least one of `pwsh` (preferred) or `powershell` must resolve via `$PATH`. When ccgate cannot find the shell at all, the failure surfaces as `command_exit` from `os/exec`'s lookup error.
6. For repeated `provider_auth` even after cache invalidation, the helper itself is producing a credential the provider rejects. Re-run the helper manually with the same shell ccgate would use — `bash -c "$your_command"`, or `pwsh -Command "$your_command"` / `powershell -Command "$your_command"` for `auth.shell: 'powershell'` — and inspect the stdout that reached the SDK.

The full reason list is in [docs/configuration.md](configuration.md#reason-values-for-credential_unavailable).
