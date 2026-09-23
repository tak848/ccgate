---
name: debug
description: Explain why ccgate produced a specific decision (deny / fallthrough / 401).
when_to_use: |
  - User asks "why did ccgate deny this" / "why did ccgate fall through".
  - Provider auth is failing with 401 / 403 and the user wants to know which credential stage broke.
  - A specific command is consistently denied and the user wants the reason before changing rules.
  - User wonders whether plan mode / bypassPermissions / dontAsk affected the decision.
---

# ccgate decision debug

Help the user understand **why** ccgate emitted a specific decision. This skill explains and proposes — it does not edit `ccgate.jsonnet`. Once the user wants a concrete edit, hand off to `/ccgate:tune`.

## 1. Pull the relevant metrics entries

Start from the aggregated metrics. Default `<target>` is `claude`; ask if both targets are in play:

```bash
ccgate <target> metrics --json --details 10
```

The JSON has top fallthrough / top deny commands. If the user has a specific timestamp, tool, or command in mind, drop into the raw entries:

```bash
tail -n 200 "$XDG_STATE_HOME/ccgate/<target>/metrics.jsonl"
```

(`$XDG_STATE_HOME` falls back to `~/.local/state` when unset.) If the user redirected `metrics_path` in their config, `ccgate <target> metrics --json` is the only safe way to find it — the raw file may not be at the default path. The file may also be rotated (`metrics_max_size`); the most recent N lines are still in `metrics.jsonl`.

Each entry has the shape documented in `${CLAUDE_PLUGIN_ROOT}/docs/configuration.md` "JSON entry schema" ([configuration#metrics-output](../../docs/configuration.md#metrics-output)). Key fields:

- `decision` — `allow` / `deny` / `fallthrough`
- `ft_kind` — `""` / `llm` / `api_unusable` / `no_apikey` / `credential_unavailable` / `unknown_provider` / `bypass` / `dontask` / `user_interaction`
- `forced` — `true` when `fallthrough_strategy` promoted an LLM `fallthrough` into the final `decision`
- `reason` — free-form text from the LLM (or a classifier when `ft_kind=credential_unavailable`)
- `credential_source` — set only when `ft_kind=credential_unavailable`
- `perm_mode` — Claude target only

## 2. Branch by the user's complaint

### "It denied something I asked for"

1. Find the entry. Surface `decision`, `reason`, `tool_input.command` (or `file_path` / `path` / `pattern`), and `perm_mode`.
2. Read the user's `ccgate.jsonnet` deny list (global + project-local) and identify which guidance line the LLM was likely matching against. If it is an embedded default, point at `ccgate <target> init` output.
3. If the user wants to change the outcome, hand off to `/ccgate:tune` (add to `append_allow`, or rewrite the matching `append_deny`).

### "It fell through to the upstream prompt"

Look at `ft_kind`:

| `ft_kind` | What it means | Where to send the user |
|---|---|---|
| `llm` | LLM was unsure. `reason` carries the LLM's justification. | `/ccgate:tune` to add an `append_allow` or `append_deny` that disambiguates the case. |
| `credential_unavailable` | Credential resolution failed. `credential_source` (`exec` / `file` / `cache` / `lock` / `profile`) plus `reason` (`command_exit` / `json_parse` / `expired` / `file_missing` / `provider_auth` / etc.) identifies the stage. | `${CLAUDE_PLUGIN_ROOT}/docs/api-key-helper.md` "Recovery checklist" ([api-key-helper#recovery-checklist](../../docs/api-key-helper.md#recovery-checklist)). |
| `no_apikey` | The provider's API key env var is unset. | `${CLAUDE_PLUGIN_ROOT}/docs/providers.md` "API keys" ([providers#api-keys](../../docs/providers.md#api-keys)). |
| `unknown_provider` | `provider.name` is not `anthropic` / `openai` / `gemini`. | Have the user re-check the `provider.name` in their `ccgate.jsonnet`. |
| `api_unusable` | Provider API returned a truncated / refused response. | Common causes: wrong `provider.base_url`, model that does not accept structured output or `temperature=0`, transient provider outage. See `${CLAUDE_PLUGIN_ROOT}/docs/providers.md` "Model selection" ([providers#model-selection](../../docs/providers.md#model-selection)). |
| `bypass` | Claude `permission_mode == "bypassPermissions"`. ccgate intentionally does not decide here. | This is expected behaviour. Explain and stop. |
| `dontask` | Claude `permission_mode == "dontAsk"`. Same as `bypass`. | Same. |
| `user_interaction` | Claude `tool_name` is `ExitPlanMode` or `AskUserQuestion`. ccgate never decides for these. | Same. |

When `forced=true`, the entry's final `decision` was promoted from an LLM `fallthrough` by `fallthrough_strategy`. If the user does not want that to happen on uncertainty, point at `${CLAUDE_PLUGIN_ROOT}/docs/configuration.md` "fallthrough_strategy" ([configuration#fallthrough_strategy](../../docs/configuration.md#fallthrough_strategy----choosing-what-to-do-on-llm-uncertainty)).

### "I'm seeing 401 / 403 from the provider"

This shows up as `ft_kind=credential_unavailable` with `reason=provider_auth`. Reaction differs by `auth.type`:

- `exec` — ccgate invalidates the cache; the next invocation re-runs the helper.
- `file` — ccgate falls through; the external rotator must rewrite the credential file.
- `profile` — ccgate falls through; the SDK refresh-token loop owns the credential.

If the user is on a static env-var key, ccgate cannot rotate it for them — they have to update the env. Reference: `${CLAUDE_PLUGIN_ROOT}/docs/api-key-helper.md` ([api-key-helper](../../docs/api-key-helper.md)).

### "Plan mode is behaving differently than I expected"

Look at `perm_mode` on the entry:

- `plan` — ccgate switches its system prompt to plan-mode rules. Write operations should be denied; read-only queries should be allowed without an explicit allow-guidance match. This is **prompt-only**, with no hard guarantee — either side can misfire.
- `bypassPermissions` / `dontAsk` — ccgate short-circuits to fallthrough; the upstream tool is in charge.

Reference: `${CLAUDE_PLUGIN_ROOT}/docs/claude-code.md` "Plan mode" ([claude-code#plan-mode](../../docs/claude-code.md#plan-mode)).

## 3. Hand off to tune (if the user wants an edit)

Once the root cause is clear and the fix is a guidance change, summarise the proposed change in one line and invoke `/ccgate:tune` with that summary. Do not write the edit yourself in this skill.

## What this skill does not do

- It does not register the PermissionRequest hook — that is `/ccgate:setup`.
- It does not write `append_allow` / `append_deny` edits — that is `/ccgate:tune`.
- It does not audit the broader environment (binary / version / hook / layer) — that is `/ccgate:doctor`.
