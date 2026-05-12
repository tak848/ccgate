---
name: setup
description: Set up ccgate as a PermissionRequest hook for Claude Code or Codex CLI.
when_to_use: |
  - First-time install of ccgate.
  - User asks to register ccgate as a PermissionRequest hook.
  - settings.json / hooks.json has no ccgate entry yet, and the user wants to add one.
  - Switching ccgate to a different provider (Anthropic / OpenAI / Gemini) for the first time.
---

# ccgate setup

Guide the user through installing ccgate and wiring it into Claude Code or Codex CLI as a PermissionRequest hook.

> [!IMPORTANT]
> When the surrounding Claude Code session is in plan mode, stop at the diff stage for every dotfile change. Do not write to `~/.claude/settings.json`, `~/.codex/hooks.json`, `~/.codex/config.toml`, `~/.claude/ccgate.jsonnet`, or `~/.codex/ccgate.jsonnet`. The user runs the writes after leaving plan mode.

## 1. Inspect current state

Before asking anything, gather the facts:

- `ccgate --version` (PATH / absolute path / not installed)
- `~/.claude/settings.json` for an existing `hooks.PermissionRequest` entry
- `~/.codex/hooks.json` and `~/.codex/config.toml` for the same on the Codex side
- `~/.claude/ccgate.jsonnet` / `~/.codex/ccgate.jsonnet` for a custom provider block

If the user already has ccgate wired up, the right next step is usually `/ccgate:doctor`, not a fresh setup. Confirm with the user before overwriting anything.

## 2. Ask three questions

Use `AskUserQuestion` once with these three questions, in order:

1. **target** — Claude Code / Codex CLI / both.
2. **install method** — `mise` / `aqua` / `go install` / Homebrew / "already on `PATH`". Do not assume the user's shell or version manager.
3. **provider** — Anthropic (Haiku, default) / OpenAI / Gemini / external credential helper.

If the user picked "external credential helper", jump to [§6 External credential helpers](#6-external-credential-helpers) after the install step.

## 3. Install ccgate

For each install method, point at the canonical command from the README and let the user run it. Do not invent flags. Reference: `${CLAUDE_PLUGIN_ROOT}/README.md` (Install section). After install, run `ccgate --version` to confirm.

## 4. Register the PermissionRequest hook

For each target chosen in §2, produce a diff against the current settings/hooks file and show it to the user. Only write after the user accepts the diff. Plan-mode reminder: stop at the diff in plan mode.

### Claude Code

Add to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PermissionRequest": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "ccgate claude"
          }
        ]
      }
    ]
  }
}
```

If `ccgate` is not on `PATH` (e.g. only available through `mise exec`), use the equivalent invocation or an absolute path.

Reference: `${CLAUDE_PLUGIN_ROOT}/docs/claude-code.md` ([claude-code](../../docs/claude-code.md)).

### Codex CLI

Codex hooks live behind `[features] codex_hooks = true` in `~/.codex/config.toml`. Add both pieces:

```toml
[features]
codex_hooks = true

[[hooks.PermissionRequest]]
matcher = ""

[[hooks.PermissionRequest.hooks]]
type    = "command"
command = "ccgate codex"
statusMessage = "ccgate evaluating request"
```

Reference: `${CLAUDE_PLUGIN_ROOT}/docs/codex-cli.md` ([codex-cli](../../docs/codex-cli.md)).

## 5. Provider configuration (if not the default Anthropic Haiku)

If the user chose a non-default provider in §2, write a global `ccgate.jsonnet` (`~/.claude/ccgate.jsonnet` for Claude, `~/.codex/ccgate.jsonnet` for Codex).

> [!IMPORTANT]
> The `provider` block is replaced **atomically** across config layers — there is no per-field merge (see `${CLAUDE_PLUGIN_ROOT}/docs/configuration.md` "How layers compose", section [configuration.md](../../docs/configuration.md)). Always write the full `provider` block: `name`, `model`, `base_url` (if needed), `auth` (if needed), `timeout_ms` (if needed). Writing just one field silently drops every other field from lower layers.

OpenAI example:

```jsonnet
{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/claude.schema.json',
  provider: {
    name: 'openai',
    model: '<openai model name — see docs/providers.md#model-selection>',
  },
}
```

Reference: `${CLAUDE_PLUGIN_ROOT}/docs/providers.md` ([providers](../../docs/providers.md)).

## 6. External credential helpers

If the user picked "external credential helper" in §2, ccgate exposes `provider.auth.type = exec` / `file` / `profile` for refreshable credentials.

This skill does **not** install or configure any specific helper. Instead:

1. Point the user at `${CLAUDE_PLUGIN_ROOT}/docs/api-key-helper.md` ([api-key-helper](../../docs/api-key-helper.md)) for the helper contract.
2. Tell them to follow the docs of whichever helper their organization ships, then come back with the `provider` block that helper produces.
3. When they bring back a partial block, remind them again about atomic replacement (§5) — every field needs to be restated.

## 7. API key setup

For the chosen provider, set the matching environment variable. Reference: `${CLAUDE_PLUGIN_ROOT}/docs/providers.md` "API keys" ([providers#api-keys](../../docs/providers.md#api-keys)).

Do not prescribe how to persist the env var. The user picks `direnv` / `mise` / their shell rc / a per-session export — list these as options if they ask, but do not assume any of them.

If the key is missing, ccgate falls through to the upstream tool's permission prompt. Flipping providers cannot break the hook, but the hook will stop deciding until the key is in place.

## 8. End-to-end verification

There is no `ccgate test` command. To verify the hook is wired up:

1. Open Claude Code (or Codex CLI) in any project.
2. Trigger an operation that hits a PermissionRequest (e.g. ask the assistant to `ls` the repo).
3. Check that a line appears in:
   - Logs: `$XDG_STATE_HOME/ccgate/<target>/ccgate.log` (falls back to `~/.local/state/ccgate/<target>/`)
   - Metrics: `$XDG_STATE_HOME/ccgate/<target>/metrics.jsonl`
4. Run `ccgate <target> metrics` and confirm a row for today appears.

If nothing shows up, hand off to `/ccgate:doctor` for a deeper environment audit.

## What this skill does not do

- It does not maintain `allow` / `deny` rules — that is `/ccgate:tune`.
- It does not diagnose denies, fallthroughs, or 401s — that is `/ccgate:debug`.
- It does not run a general health check — that is `/ccgate:doctor`.
- It does not install organization-specific credential helpers — those ship separately and their docs are the source of truth.
