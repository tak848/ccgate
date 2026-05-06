# ccgate -- Configuration

[日本語版 (docs/ja/configuration.md)](ja/configuration.md)

Cross-target configuration reference. The [root README](../README.md) lists the field table and quick-start; this page goes into the layering rules, the fallthrough decision tree, and the metrics output schema.

## Where ccgate looks for config

ccgate evaluates three layers, in order, per target. Every layer composes with the same merge semantics (see "How layers compose" below):

1. **Embedded defaults.** Compiled into the binary. Always applied as the base. Inspect with `ccgate <target> init`.
2. **Global config**, layered on top of the embedded defaults if present:
   - Claude Code: `~/.claude/ccgate.jsonnet`
   - Codex CLI:   `~/.codex/ccgate.jsonnet`
3. **Project-local overrides**, layered on top of (1)+(2). Tracked files are ignored (see "Why tracked files are skipped" below):
   - Claude Code: `{repo_root}/.claude/ccgate.local.jsonnet`
   - Codex CLI:   `{repo_root}/.codex/ccgate.local.jsonnet`

`{repo_root}` is the git repo root, resolved via `git rev-parse --show-toplevel` from the hook's `cwd`. Outside a git repo the `cwd` itself is used.


### How layers compose

| Field group | Merge behavior | Example |
|---|---|---|
| Lists: `allow`, `deny`, `environment` | A layer that sets the field **replaces** the carried-over list (even with `[]`). A layer that omits the field leaves the carried-over list untouched. | Embedded `allow: ["A","B"]` + global `allow: ["X"]` → final `allow: ["X"]`. |
| Lists: `append_allow`, `append_deny`, `append_environment` | A layer that sets the field **appends** its entries to whatever the previous layers produced. | Embedded `deny: ["A"]` + project `append_deny: ["P"]` → final `deny: ["A","P"]`. |
| Scalars: `log_*`, `metrics_*`, `fallthrough_strategy` | A layer **overwrites** the value per-field when it sets it; layers that omit a field leave the previous value untouched. | Embedded `log_max_size: 5MB` + global `log_max_size: 10MB` → final `log_max_size: 10MB`. |
| Block: `provider` (every `provider.*` field, including `name` / `model` / `base_url` / `auth` / `timeout_ms`) | A layer that writes `provider` **replaces the entire block**; layers that omit `provider` leave it untouched. Per-field merge would let stale fields from a lower layer (e.g. a proxy `base_url`, or a helper `auth.command`) leak into a different provider when a higher layer switches `name`. | Embedded `provider: {name: anthropic, model: haiku}` + global `provider: {name: openai, model: gpt-5.4-nano-2026-03-17}` → final `provider: {name: openai, model: gpt-5.4-nano-2026-03-17}`. To bump only the model, restate the whole block: `provider: {name: anthropic, model: claude-sonnet-4-6}`. When a global layer sets `auth`, any project-local `provider` override must repeat the whole `auth` block or the helper is silently dropped on that project. |

`allow` and `append_allow` (same for the other lists) can coexist in the same layer: the replace runs first, then the append stacks onto the result. Use the pattern when you want to **swap** the embedded list for a curated one and **also** add a couple of project-specific extras: `{ allow: ['only this base'], append_allow: ['plus this project rule'] }`.

> Pre-v0.6 ccgate skipped the embedded defaults whenever a global config existed (the global layer "replaced" instead of layered). v0.6 makes embedded defaults the always-present base and uses explicit `append_*` for opt-in extension; see issue [#38](https://github.com/tak848/ccgate/issues/38). Pre-v0.6 global configs (which already used `allow:` / `deny:` to fully replace) keep their behavior with no edits. Pre-v0.6 project-local configs that used `allow:` / `deny:` / `environment:` to **add** restrictions need to rename those keys to `append_allow:` / `append_deny:` / `append_environment:` -- otherwise they now wholesale replace the inherited list.

### Why tracked files are skipped

Project-local configs intentionally **only load when they are not tracked by git**. The intent is to let individual contributors layer their own restrictions on top of an ergonomic shared baseline without sneaking team-wide policy into the repo via the local-config path.

If you want repo-wide policy that everyone gets, ship it in your own fork's embedded defaults, in your team's `~/.claude/ccgate.jsonnet` distribution (e.g. via a dotfiles bootstrap), or push individual contributors to add the same `.local.jsonnet` themselves.

## `fallthrough_strategy` -- choosing what to do on LLM uncertainty

The LLM returns one of: `allow`, `deny`, `fallthrough`. `fallthrough` is the LLM saying "I am not confident enough to decide; defer to the upstream tool's prompt". For human-in-the-loop sessions that is the right behavior -- the user clicks approve. For unattended runs (schedulers, bots, agentic loops), waiting for a click means the run stalls.

`fallthrough_strategy` picks how ccgate resolves an LLM-returned `fallthrough`:

| Value     | Behavior                                                                                            | When to choose                                                            |
|-----------|-----------------------------------------------------------------------------------------------------|---------------------------------------------------------------------------|
| `ask`     | Default. Pass through to the upstream tool's permission prompt (Claude Code / Codex).               | Interactive sessions.                                                     |
| `deny`    | Auto-deny. The deny message tells the AI not to re-ask and not to attempt workarounds.              | Unattended runs that should fail safely instead of waiting for approval.  |
| `allow`   | Auto-allow.                                                                                         | Fully autonomous runs where you accept the risk that the LLM was unsure.  |

**`allow` is riskier than it looks.** The hook spec on both Claude Code and Codex CLI only delivers `decision.message` to the AI when behavior is `deny`. Forced-allow messages are silently dropped, so the AI never sees a "ccgate auto-approved this; proceed with care" warning. Pick `allow` only when that trade-off is acceptable.

### What `fallthrough_strategy` does NOT cover

Only LLM-driven uncertainty is affected. The runtime-mode fallthroughs continue to defer to the upstream tool regardless of strategy:

- API call truncated or refused (`api_unusable`)
- No API key set (`no_apikey`)
- `provider.name` is not one of `anthropic` / `openai` / `gemini` (`unknown_provider`)
- Claude `permission_mode == "bypassPermissions"` or `"dontAsk"`
- Claude `tool_name` in `{ExitPlanMode, AskUserQuestion}` (user-interaction tools)

This is intentional: `allow` is meant to keep autonomous runs moving when the LLM hesitated, not to silently auto-approve a request the LLM never actually classified.

You can audit how often each strategy fired through the metrics output (see below): the `forced_allow` / `forced_deny` columns count exactly the cases where `fallthrough_strategy` flipped an LLM `fallthrough` into a fixed verdict.

## Metrics output

Every invocation appends a JSON line to `$XDG_STATE_HOME/ccgate/<target>/metrics.jsonl` (rotated on size). `ccgate <target> metrics` aggregates the file and prints either a TTY table or a JSON document.

### CLI

```bash
ccgate claude metrics                  # last 7 days, TTY table
ccgate claude metrics --days 30        # wider window
ccgate claude metrics --json           # machine-readable output
ccgate claude metrics --details 5      # top-5 fallthrough / deny commands
ccgate claude metrics --details 0      # suppress the drill-down sections
ccgate codex  metrics --days 7         # same shape, codex side
```

### Daily table columns

| Column      | Meaning                                                                                                  |
|-------------|----------------------------------------------------------------------------------------------------------|
| `Date`      | Day boundary in the local timezone.                                                                      |
| `Total`     | Number of invocations counted toward the day. `ExitPlanMode` / `AskUserQuestion` are excluded.            |
| `Allow`     | Decisions that resulted in `allow` (LLM-clear or forced).                                                |
| `Deny`      | Decisions that resulted in `deny` (LLM-clear or forced).                                                 |
| `Fall`      | Decisions that resulted in `fallthrough` and were not promoted to allow/deny.                            |
| `F.Allow`   | Subset of `Allow` that was promoted from an LLM `fallthrough` by `fallthrough_strategy=allow`.            |
| `F.Deny`    | Subset of `Deny` promoted by `fallthrough_strategy=deny`.                                                |
| `Err`       | Invocations that ended in an error (parse failure, panic, API failure not handled by `Unusable`).        |
| `Auto%`     | `(Allow + Deny) / Total`. Higher means more decisions resolved without falling back to the upstream prompt. |
| `Avg(ms)`   | Mean elapsed time per invocation (ccgate's wall-clock around `DecidePermission`).                        |
| `Tokens`    | Sum of input / output tokens reported by the Anthropic API for the day.                                  |

### JSON entry schema (one line per invocation)

```json
{
  "ts": "2026-04-26T12:34:56.789Z",
  "sid": "session-abc",
  "tool": "Bash",
  "perm_mode": "default",
  "decision": "allow",
  "ft_kind": "",
  "forced": false,
  "reason": "Read-only inspection inside repo; matches allow guidance.",
  "credential_source": "",
  "deny_msg": "",
  "model": "claude-haiku-4-5",
  "in_tok": 4321,
  "out_tok": 87,
  "elapsed_ms": 612,
  "error": "",
  "tool_input": {
    "command": "ls -la"
  }
}
```

`ft_kind` is filled when the LLM returned (or the runtime forced) a fallthrough; the value tells you which fallback path fired (`llm`, `api_unusable`, `no_apikey`, `credential_unavailable`, `unknown_provider`, `bypass`, `dontask`, `user_interaction`). `forced=true` means `fallthrough_strategy` promoted an LLM `fallthrough` into the recorded `decision`.

`credential_source` is set only when `ft_kind=credential_unavailable`. It carries the keystore stage that produced (or failed to produce) the credential — currently `exec` / `file` / `cache` / `lock` (matching `auth.type=exec` for the helper-exec path) — so the same `reason` can be grouped by where it actually broke. The set is open: future credential paths (e.g. an `OAuth refresh` stage, a Windows native backend) may add new values, so consumers parsing this field should treat it as a free-form short string and tolerate unknown values rather than enum-validate it.

The `reason` field meaning depends on `ft_kind`:

- `ft_kind=llm`: free-form text the LLM emitted to justify its fallthrough.
- `ft_kind=credential_unavailable`: a secret-free classifier from the table below.

#### Reason values for `credential_unavailable`

| Reason                  | Meaning                                                                                                |
|-------------------------|--------------------------------------------------------------------------------------------------------|
| `command_exit`          | `auth.command` exited non-zero.                                                                        |
| `json_parse`            | Helper / file produced JSON that failed strict parsing or had no `key`.                                |
| `invalid_expiration`    | Helper / file JSON parsed but `expires_at` was not RFC3339.                                            |
| `empty_output`          | Plain-string output was empty after trim.                                                              |
| `invalid_plain_output`  | Plain-string output had internal newlines (multi-line is rejected).                                    |
| `expired`               | `expires_at` was already in the past, or remaining TTL was below `auth.refresh_margin_ms`, at read time.  |
| `file_missing`          | `auth.path` did not exist.                                                                             |
| `file_read`             | `auth.path` exists but failed to read (permissions, FS error).                                         |
| `timeout`               | `auth.command` exceeded `auth.timeout_ms`.                                                              |
| `output_too_large`      | Helper stdout exceeded the 64 KiB limit.                                                               |
| `lock_timeout`          | flock retry budget exhausted while peers were refreshing.                                              |
| `lock_error`            | flock syscall returned a non-EWOULDBLOCK error (broken lock subsystem; helper exec is skipped).        |
| `cache_unavailable`     | Cache directory cannot be created / `chmod`'d. Treated as fail-fast (helper exec is skipped) because without the sibling lock file we cannot prevent concurrent helpers from racing the broker. |
| `provider_auth`         | Provider rejected the credential with **HTTP 401 or 403**. `auth.type=exec` invalidates the cache so the next fire re-runs the helper; `auth.type=file` falls through (no cache to clear); env-var keys are **not** routed here because ccgate cannot rotate env vars and swallowing the rejection would hide user-side misconfiguration. |

`credential_unavailable` is therefore wider than just "credential resolution failed": it also covers "provider received and rejected the credential" (401 / 403).

#### Log-only credential warnings (not in metrics)

The cache layer recovers from these without falling through, so they are emitted as `slog.Warn` only and never appear in metrics:

- `cache_parse` — corrupt cache JSON; unlinked, helper re-runs.
- `cache_read` — cache read error; unlinked, helper re-runs.
- `cache_write` — cache write / atomic-rename failed; fresh key returned uncached.

### Drill-down sections

`ccgate <target> metrics` adds three sections by default:

- **Top fallthrough commands** -- the most frequent operations that the LLM was unsure about. These are good candidates for a project-local allow / deny rule that lets ccgate skip the LLM round-trip entirely.
- **Top deny commands** -- the most frequent operations the LLM denied. Useful when an automated job keeps trying the same blocked thing -- often a sign that the AI's plan needs a different shape.
- **Credential failures** -- aggregated from `ft_kind=credential_unavailable` entries, grouped by `(source, reason)`. Tool input is intentionally ignored here (every fire during a credential outage carries the same source/reason regardless of the tool the user invoked). Cache-layer warnings do not appear here; check `ccgate.log` for those.

Pass `--details 0` to suppress the fallthrough / deny sections, or `--details N` to limit each to the top N rows.

### Disabling, redirecting, rotation

```jsonnet
{
  // Move the metrics file
  metrics_path: '~/my-state/ccgate-claude-metrics.jsonl',
  // Disable metrics entirely
  // metrics_disabled: true,
  // Default rotation threshold: 2MB
  // metrics_max_size: 5 * 1024 * 1024,
}
```

The same fields exist for the log file (`log_path`, `log_disabled`, `log_max_size`, default 5MB). All four `_max_size` fields treat `0` as "no rotation".

## Known limitations

- **Plan mode (Claude only) is prompt-only.** Under `permission_mode == "plan"`, ccgate relies on the LLM plus prose in the system prompt to (a) reject implementation-side writes and (b) allow read-only queries without an explicit allow-guidance match. Either side can misfire. Tracked in [#37](https://github.com/tak848/ccgate/issues/37).
- **No surgical reset for a single embedded default rule.** A layer either replaces a list wholesale or appends to it; removing one specific embedded entry while keeping the rest requires re-stating the whole list under `allow` / `deny` minus that one entry.
- **Codex hook schema may change.** Codex hooks live behind upstream's `features.codex_hooks = true` flag and are still evolving.
- **Codex `~/.codex/config.toml` ingestion** (`approval_policy`, `sandbox_mode`, `prefix_rules`) is not implemented yet. ccgate decides purely from the hook payload + ccgate config; if Codex's own settings would have rejected something, that signal does not reach the LLM today.
