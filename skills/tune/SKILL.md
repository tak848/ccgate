---
name: tune
description: Refine ccgate allow / deny rules from recent metrics.
when_to_use: |
  - User wants to add or adjust allow / deny rules in ccgate.jsonnet.
  - "ccgate falls through too much" / "ccgate keeps denying the same command" type complaints.
  - User reviewed `ccgate <target> metrics --details N` and wants a concrete edit proposal.
  - User wants to add a `deny_message:` hint to an existing deny rule.
---

# ccgate rule tuning

Refine the user's ccgate guidance lists (`allow` / `deny` / `environment`, or their `append_*` siblings) using the last N days of metrics as the signal.

> [!IMPORTANT]
> When the surrounding Claude Code session is in plan mode, stop at the diff stage. Do not write to any `ccgate.jsonnet` / `ccgate.local.jsonnet` until plan mode is off.

## 1. Pull recent metrics

```bash
ccgate <target> metrics --json --details 5
```

`<target>` is `claude` or `codex`. The output has top fallthrough commands and top deny commands aggregated over the configured window (default 7 days).

If the JSON output is empty or the user reports a custom `metrics_path`, ask them which `<target>` they want tuned — do not silently assume `claude`.

## 2. Read the top entries

For each top entry, extract:

- `tool` (`Bash` / `Edit` / `Read` / MCP tool / `apply_patch` / etc.)
- representative `command` / `file_path` / `pattern`
- count of fallthrough / deny in the window

Use these to decide whether the right move is:

- `append_allow` — recurring fallthrough on a safe operation the LLM is unsure about.
- `append_deny` (with `deny_message`) — recurring user requests for something that should be refused and the LLM keeps falling through.
- Rewrite an existing rule — current `allow` / `deny` guidance is too vague or too sharp.

## 3. Decide the edit target layer

Inspect the candidate layer paths, in load order:

| Order | Claude target | Codex target |
|------:|---------------|--------------|
| 2 | `~/.claude/ccgate.jsonnet` (global) | `~/.codex/ccgate.jsonnet` |
| 3 | `{main_worktree}/.claude/ccgate.local.jsonnet` (linked worktree only, untracked-only) | `{main_worktree}/.codex/ccgate.local.jsonnet` |
| 4 | `{repo_root}/.claude/ccgate.local.jsonnet` (untracked-only) | `{repo_root}/.codex/ccgate.local.jsonnet` |

For each candidate the user names, check:

- Does the file exist?
- Is the path git-tracked (`git ls-files --error-unmatch <path>` succeeds)?

> [!WARNING]
> Project-local `ccgate.local.jsonnet` files are loaded **only when not tracked by git**. If the chosen layer is tracked, the edit will not take effect. Surface the warning and ask the user whether to switch to a global layer or move the file to untracked.

Reference: `${CLAUDE_PLUGIN_ROOT}/docs/configuration.md` "Where ccgate looks for config" ([configuration](../../docs/configuration.md)).

## 4. Read the existing config

Once the layer is decided, read it. Note whether it currently uses replace-style fields (`allow:` / `deny:`) or append-style (`append_allow:` / `append_deny:`). The two have different semantics:

- `allow:` / `deny:` replace the entire list from earlier layers (including embedded defaults).
- `append_allow:` / `append_deny:` add to the carried-over list.

Reference: `${CLAUDE_PLUGIN_ROOT}/docs/rule-tuning.md` "Replace vs append" ([rule-tuning](../../docs/rule-tuning.md#3-replace-vs-append-ie-copy-paste-or-not)).

Default to `append_*` unless the user is already running a replace-style config or explicitly asks to curate the full list.

## 5. Draft the edit

Write one or more new rule lines. Each rule is one line of natural-language guidance.

- For `append_deny` entries, **always** include a trailing `deny_message:`. The deny_message is the hint shown to the AI when the rule fires; without it, the AI sees a bare refusal and may keep trying.
- For `append_allow` entries, write at a granularity the LLM can judge from `tool_input` / `tool_input_raw` / `referenced_paths` / `branch_name` / the command string.

Example shapes (Claude target):

```jsonnet
{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/claude.schema.json',
  append_allow: [
    'Edit / Write / MultiEdit targeting Markdown files under repo_root/docs/ is fine; content review happens elsewhere.',
  ],
  append_deny: [
    'Setting production environment variables in the running session. deny_message: configure production via the deployment system, not via shell exports.',
  ],
}
```

Reference: `${CLAUDE_PLUGIN_ROOT}/docs/rule-tuning.md` "Writing rules" ([rule-tuning](../../docs/rule-tuning.md#4-writing-rules)).

## 6. If touching the `provider` block

> [!IMPORTANT]
> The `provider` block is replaced **atomically** across layers. There is no per-field merge. Always write the full block — `name`, `model`, `base_url` (if needed), `auth` (if needed), `timeout_ms` (if needed). Writing just one field silently drops every other field from lower layers.

Reference: `${CLAUDE_PLUGIN_ROOT}/docs/configuration.md` "How layers compose" ([configuration](../../docs/configuration.md)).

## 7. Apply the edit

Show the diff. If the session is in plan mode, stop here.

Otherwise apply the edit, then suggest the next metrics cycle (re-run `ccgate <target> metrics --details N` in a day or two and check whether the targeted commands moved out of the top entries).

## What this skill does not do

- It does not explain **why** a specific decision was made — that is `/ccgate:debug`.
- It does not check binary, version, hook registration, or layer routing — that is `/ccgate:doctor`.
- It does not register the PermissionRequest hook from scratch — that is `/ccgate:setup`.
