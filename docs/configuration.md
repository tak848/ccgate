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
| Lists: `allow`, `deny`, `environment` | Each layer **appends** its entries on top of the previous result. | Embedded `deny: ["A"]` + global `deny: ["B"]` + project `deny: ["C"]` → final `deny: ["A","B","C"]`. |
| Scalars: `provider.*`, `log_*`, `metrics_*`, `fallthrough_strategy` | Each layer **overwrites** the previous value when it sets the field; layers that omit it leave the previous value untouched. | Embedded `provider.model: "claude-haiku-4-5"` + global `provider: {model: "claude-sonnet-4-6"}` → final `provider.model: "claude-sonnet-4-6"`, embedded provider.name still in effect. |

There is currently no way to **remove** an entry from a list a previous layer added. If you need to drop a specific embedded `allow` / `deny` rule from your overrides, open an issue describing the rule and the motivation. An explicit reset/override syntax is being tracked separately.

> Pre-v0.6 ccgate skipped the embedded defaults whenever a global config existed (the global layer "replaced" instead of layered). v0.6 unified the semantics; see issue [#38](https://github.com/tak848/ccgate/issues/38). If your global config is a copy of `ccgate <target> init`, dedupe it after upgrading -- otherwise rules are applied twice.

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
- `provider.name != "anthropic"` (`non_anthropic`)
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

`ft_kind` is filled when the LLM returned (or the runtime forced) a fallthrough; the value tells you which fallback path fired (`llm`, `api_unusable`, `no_apikey`, `non_anthropic`, `bypass`, `dontask`, `user_interaction`). `forced=true` means `fallthrough_strategy` promoted an LLM `fallthrough` into the recorded `decision`.

### Drill-down sections

`ccgate <target> metrics` adds two sections by default:

- **Top fallthrough commands** -- the most frequent operations that the LLM was unsure about. These are good candidates for a project-local allow / deny rule that lets ccgate skip the LLM round-trip entirely.
- **Top deny commands** -- the most frequent operations the LLM denied. Useful when an automated job keeps trying the same blocked thing -- often a sign that the AI's plan needs a different shape.

Pass `--details 0` to suppress both sections, or `--details N` to limit each to the top N rows.

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
- **No reset/override for individual embedded default rules.** Layered configs can only **add** rules and **overwrite scalars**. Removing a specific embedded `allow` / `deny` rule from a global / project-local config is not supported today.
- **Codex hook is upstream-experimental.** The hook schema may change without notice.
- **Codex `~/.codex/config.toml` ingestion** (`approval_policy`, `sandbox_mode`, `prefix_rules`) is not implemented yet. ccgate decides purely from the hook payload + ccgate config; if Codex's own settings would have rejected something, that signal does not reach the LLM today.
