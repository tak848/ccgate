# Short-lived / rotating API keys

[日本語版 (docs/ja/api-key-helper.md)](ja/api-key-helper.md)

When the credential the provider needs rotates faster than a static env var can keep up — AWS STS sessions, Vertex ADC, OpenAI-compatible gateway virtual keys, internal key brokers — ccgate can resolve it through a helper process or a file instead of `*_API_KEY`.

This document is the full reference. The README has the minimum config snippet and a single example; everything else (helper contract, caching semantics, account isolation, security guidance, recovery checklist) lives here.

## Output formats

The helper writes one of two shapes on stdout (or, for `api_key_file`, into the file):

- **JSON**: `{"key":"sk-...","expires_at":"2026-05-04T01:23:45Z"}`. Parsed strictly. `key` is required; `expires_at` is optional. Unknown top-level fields (e.g. broker metadata) are accepted but dropped — only the canonical `{key, expires_at}` reaches the cache or the SDK.
- **Plain string**: a single non-empty line. Returned verbatim.

`expires_at` is RFC3339. Helper output exceeding 64 KiB is rejected as `output_too_large`. The same 64 KiB cap applies to `api_key_file` content.

How caching applies depends on the source:

- For `api_key_command`: JSON with a future `expires_at` is memoized to a per-target cache file (see [Caching](#caching)) and refreshed early via `api_key_refresh_margin`. JSON without `expires_at` and plain string output are accepted but **not cached** — the helper re-runs on every hook invocation.
- For `api_key_file`: ccgate reads the file on every hook invocation and does not maintain an internal cache. The external rotator owns when the credential is refreshed.

## Config

```jsonnet
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    api_key_command: '/usr/local/bin/my-key-broker --provider anthropic',
    api_key_refresh_margin: '60s', // optional, default 30s
    api_key_command_timeout: '5s', // optional, default 5s
  },
}
```

| Field | Type | Default | What it does |
|---|---|---|---|
| `provider.api_key_command` | string | `""` | Shell command run via `/bin/sh -c` whose stdout is the credential. |
| `provider.api_key_file` | string (abs or `~/`) | `""` | File whose contents are the credential. Read on every hook invocation; no internal caching. |
| `provider.api_key_refresh_margin` | duration | `"30s"` | Cache is stale once `now + margin >= expires_at`. `>= 0` (`0s` disables early refresh). |
| `provider.api_key_command_timeout` | duration | `"5s"` | Hot-path upper bound for one helper invocation. `> 0`. |

The `provider` block is replaced atomically across config layers, so a project-local config that restates `provider` must repeat any global `api_key_command` / `api_key_file` — otherwise the helper config is silently dropped on that project.

## Resolution order and platform support

`api_key_command` > `api_key_file` > `CCGATE_*_API_KEY` > `*_API_KEY`.

When a helper or file is configured ccgate will **not** silently fall back to env vars on failure: that would mask the helper bug. Instead the hook falls through with `kind=credential_unavailable` and a reason that pinpoints which step failed (see [docs/configuration.md](configuration.md) for the full reason taxonomy).

`api_key_command` and `api_key_file` are Unix only (Linux, macOS, *BSD). On Windows configuring either field falls through with `reason=unsupported_platform`; users who do not configure either keep using the regular `*_API_KEY` env-var path unchanged. ccgate does **not** silently re-route a configured-but-unsupported helper to env vars.

## Caching

- Path: `$XDG_CACHE_HOME/ccgate/<target>/api_key.<sha256[:16]>.json` (target = `claude` / `codex`). Falls back to `~/.cache/ccgate/<target>/...` when `XDG_CACHE_HOME` is unset.
- Permissions: directory `0700`, file `0600`. ccgate `chmod`s the directory back to `0700` even if it pre-existed at a looser mode.
- Cache content is the canonical `{key, expires_at}` only — extra fields the helper printed (refresh tokens, broker session IDs) are dropped on write so they do not get persisted to disk.
- Atomic rename: temp file is created in the same directory and renamed into place. Cross-filesystem rename pitfalls do not apply.
- Concurrent fires are serialised by `flock` on a sibling lock file (`*.lock`). The lock file is never deleted; its presence is normal.

### Cache key and account isolation

The cache key is built from `(target, provider.name, base_url, api_key_command)` only — environment variables are **not** part of the hash. If your helper depends on `AWS_PROFILE`, `GCLOUD_ACCOUNT`, `OP_ACCOUNT`, etc., a literal `api_key_command: 'aws sts ...'` will share the same cache file across every account.

Two ways to isolate accounts:

- Bake the account into the command string: `api_key_command: 'aws sts assume-role --profile prod ...'`. Different command strings hash to different cache files, so two project-local configs aimed at different accounts stay isolated.
- Use `api_key_file` per account; each account's rotator writes to its own path.

A user-supplied `api_key_cache_key` salt for cases the command-string approach cannot express cleanly is tracked under follow-ups to [#61](https://github.com/tak848/ccgate/issues/61).

## Security guidance

- `api_key_file`: ccgate reads it but does not normalise the mode. Set `chmod 0600 <file>` and put the file under a `chmod 0700` directory yourself.
- `api_key_command`: do **not** put a literal secret into the command string. The string is passed to `/bin/sh -c`, so it appears in `ps`, `/proc/<pid>/cmdline`, audit logs, and shell history. Wrap secret material in a file or keychain that the helper reads internally.
- Helper stderr body is **never** written to `ccgate.log`. ccgate captures stderr internally to bound memory but logs only the byte count and exit error on failure. Re-run the helper manually with `2>&1` if you need to inspect its diagnostic output.
- Provider error response bodies are redacted before they reach `ccgate.log` and `metrics.jsonl`. Both `anthropic-sdk-go` and `openai-go` embed the response body in `Error.Error()`; ccgate replaces that with a short `<provider> API error (status N)` summary so a misbehaving proxy cannot leak credentials through the log.

## Helper contract

The helper must:

- Be **non-interactive** (no TTY input, no browser open; stdin is closed).
- **Not daemonize**: forking past the process group escapes the timeout-kill.
- Write **only the credential** on stdout. Diagnostics belong on stderr — and even there, never put secrets, since some operators capture stderr.
- For plain-string mode: the trimmed stdout must be a single non-empty line. Multi-line output is rejected as `invalid_plain_output`.
- Be **deterministic** for the same `(api_key_command, provider.name, base_url)` triple: two callers with the same config must agree on what the credential is for.

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

Then `chmod 700 ~/bin/ccgate-key-passthrough.sh` and `api_key_command: '~/bin/ccgate-key-passthrough.sh'`. ccgate runs this on every hook invocation (no caching) and forwards the env value to the SDK.

### JSON with expiry: cache through a broker

When a real broker mints time-limited credentials, wrap the response in `{key, expires_at}` so ccgate can cache and refresh just before expiry. Build the JSON with `jq` so a token containing `"`, `\`, or newlines cannot break the payload:

```sh
#!/bin/sh
# ~/bin/ccgate-key-broker.sh
set -eu
TOKEN=$(my-key-broker --provider anthropic) # outputs the API key
# Pick an `expires_at` slightly inside the broker's TTL so the
# refresh_margin (default 30s) has slack.
EXP=$(date -u -v+50M +%FT%TZ 2>/dev/null || date -u -d '+50 minutes' +%FT%TZ)
jq -nc --arg key "$TOKEN" --arg expires_at "$EXP" '{key:$key, expires_at:$expires_at}'
```

`api_key_command: '~/bin/ccgate-key-broker.sh'`. Test the script standalone first (`~/bin/ccgate-key-broker.sh | jq .` should print a JSON object) before handing it to ccgate.

### `api_key_file` rotator: hot-path-free

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

Then `api_key_file: '~/.config/my-broker/anthropic.json'`. ccgate just reads the file on every hook invocation — there is no internal cache to refresh, the rotator owns rotation.

## Provider 401/403 behaviour

When the provider rejects the credential ccgate just used:

- `api_key_command` path: the keystore cache file is unlinked, the hook still falls through on this invocation (no exit 1), and the next invocation re-runs the helper with a fresh credential.
- `api_key_file` path: there is no internal cache to invalidate. The fire falls through, but recovery (writing a fresh credential to the file) is the rotator's job.
- Env-var keys are intentionally **not** routed through this branch. ccgate cannot rotate env vars, so silently swallowing 401/403 would mask user-side configuration errors — they keep going through the regular API-error exit path.

## Differences from AWS `credential_process`

The output shape is intentionally close to AWS `credential_process` so existing helpers can be adapted with a one-line wrapper, but ccgate **memoizes** the helper output to disk while the AWS CLI re-execs on every call. That trade favours hot-path latency (hooks fire dozens of times per session) at the cost of one extra "is this credential actually still valid?" answer in your helper.

If your broker does not want callers to memoize, return JSON without `expires_at` (`{"key":"..."}`) and ccgate will re-exec every time.

## Recovery checklist

When something looks wrong:

1. `tail` `ccgate.log` (`$XDG_STATE_HOME/ccgate/<target>/ccgate.log`) and look for entries with `kind=credential_unavailable`. Read the `reason` and `source` (`command` / `file` / `cache` / `lock`) attributes — they pinpoint which step failed.
2. Run `ccgate <target> metrics` and inspect the **Credential failures** section. It groups failures by `(source, reason)`.
3. If the failure looks cache-related, remove `$XDG_CACHE_HOME/ccgate/<target>/api_key.*.json` to force a refresh. The sibling `*.lock` files are reused — leave them alone.
4. If `expired` keeps appearing, compare the helper's `expires_at` with `date -u`. Clock skew or a broken TTL inside the helper is the usual cause.
5. To reproduce in isolation: `/bin/sh -c "$your_command"` should print exactly the same stdout the helper writes.

The full reason taxonomy (with the difference between metrics fallthrough reasons and log-only cache warnings) is in [docs/configuration.md](configuration.md#reason-values-for-credential_unavailable).
