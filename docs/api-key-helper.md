# Refreshable credentials

[日本語版 (docs/ja/api-key-helper.md)](ja/api-key-helper.md)

When the credential the provider needs rotates faster than a static env var can keep up — AWS STS sessions, Vertex ADC, OpenAI-compatible gateway virtual keys, internal key brokers — ccgate resolves it through `provider.auth` instead of `*_API_KEY`.

`provider.auth` has three modes:

- **`type: 'exec'`**: ccgate runs a shell command and uses its stdout as the credential.
- **`type: 'file'`**: ccgate reads a file written by an external rotator.
- **`type: 'profile'`**: Anthropic only. ccgate hands an `ant auth login` profile to anthropic-sdk-go and the SDK owns the refresh loop. See [Profile-based authentication](#profile-based-authentication-anthropic-only).

(`*_API_KEY` env var is a separate path used when `provider.auth` is omitted — it's not one of the `auth` modes.)

`provider.name="codex-oauth"` is separate from this mechanism: it uses Codex app-server's ChatGPT login cache under `provider.codex_home`, not `provider.auth` or API keys.

The shell that runs an `exec` helper is selected per-config via `auth.shell` (default `bash`); see [Helper contract](#helper-contract) for the shells ccgate currently spawns.

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
| `auth.type` | `"exec"` / `"file"` / `"profile"` | (required when `auth` is set) | Resolution mode. `profile` is Anthropic-only; see [Profile-based authentication](#profile-based-authentication-anthropic-only). |
| `auth.command` | string | `""` | (`exec` only, required) Shell command; stdout is the credential. |
| `auth.shell` | `"bash"` / `"powershell"` | `"bash"` | (`exec` only) Shell used to run `auth.command`. `powershell` tries `pwsh` first, falls back to `powershell`. |
| `auth.path` | string | `$XDG_STATE_HOME/ccgate/<target>/auth_key.json` | (`file` only) Credential file path. Omit to use the default. |
| `auth.profile` | string | `""` | (`profile` only) Anthropic profile name (the value `ant auth login --profile <name>` writes to `<config_dir>/credentials/<name>.json`). Empty/omitted lets the SDK resolve `$ANTHROPIC_PROFILE` → `<config_dir>/active_config` → `"default"`. |
| `auth.refresh_margin_ms` | int (ms) | `60000` | Treat credentials as expired this many ms before `expires_at`. `0` disables. (`exec` / `file` only — `profile` delegates refresh to the SDK.) |
| `auth.timeout_ms` | int (ms) | `30000` | Hard cap on one Resolve call. `> 0`. (`exec` / `file` only.) |
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
- Avoid literal secrets in `auth.command`. The string is passed to the configured shell (`bash -c <command>`, or `pwsh -Command <command>` / `powershell -Command <command>` when `auth.shell: 'powershell'`) and shows up in process listings and shell history. Read secrets from a file or keychain inside the helper instead.

ccgate exports `CCGATE_API_KEY_RESOLUTION=1` into the helper environment so a wrapper helper can detect recursive invocation. All other environment variables (including `*_API_KEY`) are inherited. Stdin is closed; the helper cannot read from the parent terminal.

### Browser-based first-run auth

Helpers like `gcloud auth print-access-token`, `aws sso login`, or an internal SSO key broker open a browser on first run, complete OAuth / SAML in the browser, then print the credential on stdout. Subsequent runs read a cached refresh token and are silent.

This pattern is supported. The default `auth.timeout_ms` (`30000`) covers most non-interactive helpers; for browser-based first-run flows that involve the user clicking through a consent screen, raise it to e.g. `120000` so the first Permission Request after a long idle does not surface as `reason=timeout`.

## Profile-based authentication (Anthropic only)

Anthropic provider only. The official `ant` CLI (`ant auth login` for browser-based OAuth, `ant profile activate <name>` to switch the active profile) writes credentials to `<config_dir>/credentials/<name>.json` (mode 0600). `<config_dir>` defaults to `~/.config/anthropic`. The Anthropic profile resolution chain (`$ANTHROPIC_PROFILE` → `<config_dir>/active_config` → `"default"`) is shared between the Go SDK, Claude Code, and the Claude Agent SDK ([wif-reference doc](https://platform.claude.com/docs/en/api/authentication/wif-reference)). The Anthropic SDK refreshes the access token itself using the stored refresh token; ccgate stays out of the credential path entirely and only disables the SDK's static-credential env autoload (`ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN`) so a leftover env var does not silently shadow your declared profile. Workload Identity Federation is supported only via a profile config that declares `authentication.type=oidc_federation`.

### Quick start

```sh
# Install the `ant` CLI by your preferred method (e.g. `mise use -g aqua:anthropics/anthropic-cli`,
# `aqua g -i anthropics/anthropic-cli`, or a direct download from the upstream release page).
ant auth login --profile ccgate         # opens a browser, writes ~/.config/anthropic/credentials/ccgate.json
# add to ccgate.jsonnet:
#   provider: { ..., auth: { type: 'profile', profile: 'ccgate' } }
# tail -f $XDG_STATE_HOME/ccgate/<target>/ccgate.log
#   → expect: rg 'credential source selected.*source=profile.*profile_name_set=true'
```

> [!IMPORTANT]
> **`ant auth login` retargets `<config_dir>/active_config` regardless of profile name.** ant rewrites the active pointer to whatever `--profile <name>` you pass (or to `default` when `--profile` is omitted) on every login. Because Anthropic profile resolution is shared with Claude Code and the Claude Agent SDK, that retarget can shift Claude Code from your subscription onto pay-as-you-go API billing if it follows the active pointer. So:
>
> - Use a non-default profile name (e.g. `ccgate`) and declare it explicitly in ccgate.jsonnet, so a stray `ant auth login` (no `--profile`) creating `default` does not silently take over.
> - After every `ant auth login`, restore the active pointer to where you want it. Two options:
>   - `rm <config_dir>/active_config` to clear the pointer entirely (the SDK then resolves to `default`).
>   - `ant profile activate <previous-profile-name>` if you have a profile you actively use.
> - To bind a profile to a different org / workspace, ant rejects re-login on a profile that already has one bound. Delete `<config_dir>/configs/<name>.json` and run `ant auth login --profile <name>` again.

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
# Print a JSON line: {"key": "...", "expires_at": "<RFC3339>"}
# Replace `my-broker-expiry-rfc3339` with whatever produces an RFC3339 timestamp for "now + helper lifetime".
set -eu
TOKEN=$(my-key-broker --provider anthropic)
EXP=$(my-broker-expiry-rfc3339)
jq -nc --arg key "$TOKEN" --arg expires_at "$EXP" '{key:$key, expires_at:$expires_at}'
```

```jsonnet
auth: { type: 'exec', command: '~/bin/ccgate-key-broker.sh' }
```

Test the script standalone first (`~/bin/ccgate-key-broker.sh | jq .` should print a JSON object) before handing it to ccgate.

### External rotator (no helper exec on the hook path)

When you want zero exec cost on the hook, schedule an external rotator to write the same JSON shape and have it land at `auth.path` via an **atomic replace** (`write tmp` → `chmod 0600 tmp` → `rename tmp <auth.path>`). The rotator owns the refresh schedule; ccgate just reads the file on each hook invocation.

```jsonnet
auth: { type: 'file', path: '~/.config/my-broker/anthropic.json' }
```

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

`auth.path` reads are bounded by `auth.timeout_ms` (default 30000) just like the exec branch — a stalled file source surfaces as `reason=timeout` instead of blocking the hook. Local regular files are still strongly recommended (network or virtual file sources will time out reliably but each fire pays a `timeout_ms` wait); a per-user local path is the cheapest option.

ccgate emits a `slog.Warn` when `auth.path` *or* the cache file is readable by anyone other than the current user, or is owned by a different user. The check is a best-effort security nudge, not policy enforcement: it inspects only the well-known "world-readable" indicators reachable from the filesystem and does not compute effective access. Keep the file user-only readable inside a user-only directory.

## Provider 401/403 behaviour

When the provider rejects the credential ccgate just used, the HTTP status alone determines the reaction.

| HTTP status         | `auth.type=exec`                  | `auth.type=file`               | `auth.type=profile`            | env var |
|---------------------|-----------------------------------|--------------------------------|--------------------------------|---------|
| 401 / 403           | invalidate cache, fallthrough     | fallthrough (no cache)         | fallthrough                    | exit 1  |
| 5xx / 429 / network | exit 1                            | exit 1                         | exit 1                         | exit 1  |

On `auth.type=profile`, the SDK refresh-token loop owns the credential, so ccgate has no cache to invalidate. The env-var path exits 1 on 401/403 because ccgate cannot rotate env vars; swallowing the rejection would hide a user-side configuration error.

To opt out of caching, return JSON without `expires_at` (or plain string) and the helper re-runs every fire.

## Recovery checklist

1. `tail $XDG_STATE_HOME/ccgate/<target>/ccgate.log` and look for entries with `kind=credential_unavailable`. The `reason` and `source` (`exec` / `file` / `cache` / `lock`) attributes pinpoint the failed step.
2. Run `ccgate <target> metrics` and check the **Credential failures** section, which groups failures by `(source, reason)`.
3. For `cache_parse` / `cache_read` / `cache_write` (log-only warnings), remove `$XDG_CACHE_HOME/ccgate/<target>/api_key.*.json` to force a refresh. Leave the sibling `*.lock` files alone.
4. For `expired`, compare the helper's `expires_at` with `date -u`. Clock skew or a broken TTL inside the helper is the usual cause.
5. For `command_exit` on a fresh setup, check whether the configured `auth.shell` binary is on `$PATH`. For `auth.shell: 'bash'`, `bash` must resolve; for `auth.shell: 'powershell'`, at least one of `pwsh` (preferred) or `powershell` must resolve. When ccgate cannot find the shell, the failure surfaces as `command_exit` from `os/exec`'s lookup error.
6. For repeated `provider_auth` even after cache invalidation, the helper itself is producing a credential the provider rejects. Re-run the helper manually with the same shell ccgate would use — `bash -c "$your_command"`, or `pwsh -Command "$your_command"` / `powershell -Command "$your_command"` for `auth.shell: 'powershell'` — and inspect the stdout that reached the SDK.
7. For `profile_load` (`auth.type=profile`), the slog `error_class` field narrows the cause:
   - `profile_config_missing`: run `ant auth login --profile <name>` to create the profile.
   - `profile_config_parse` / `profile_config_invalid`: restore `<config_dir>/configs/<name>.json` to mode `0644`, or delete it and re-run `ant auth login --profile <name>`.
   - `credentials_missing`: run `ant auth login --profile <name>` to publish credentials.
   - `credentials_stat_failed`: the credentials file or its parent dir failed `os.Stat` for a reason other than "missing" (typically permissions). Restore mode 0700 on `<config_dir>/credentials/`.
8. After any `ant auth login`, confirm `<config_dir>/active_config` with `ant auth status`. If Claude Code starts using an unexpected profile, either `rm <config_dir>/active_config` (clears the pointer) or `ant profile activate <previous-profile>`.

The full reason list is in [docs/configuration.md](configuration.md#reason-values-for-credential_unavailable).
