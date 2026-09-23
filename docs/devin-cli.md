# ccgate -- Devin

[日本語版 (docs/ja/devin-cli.md)](ja/devin-cli.md)

Devin-CLI-specific notes for the `ccgate devin` hook.

## Requirements

- Devin CLI hooks (`PreToolUse`, `PermissionRequest`, ...). See [Devin's hooks docs](https://docs.devin.ai/cli/extensibility/hooks/overview) for the upstream payload schema.
- **Tool-agnostic.** Devin hooks fire for `exec`, `edit`, `write`, `apply_patch`, MCP tool calls (`mcp__<server>__<tool>`), and other surfaces. ccgate classifies by `tool_name` + the full `tool_input` JSON, not by tool kind alone.

## Hook registration

Devin CLI reads hooks from (project level) `.devin/hooks.v1.json`, the `"hooks"` key of `.devin/config.json` / `.devin/config.local.json`, and (user level) the `"hooks"` key of `~/.config/devin/config.json`. `.claude/settings.json`-style hook files are also picked up automatically.

### `.devin/hooks.v1.json` form (recommended)

```json
{
  "PermissionRequest": [
    {
      "matcher": "",
      "hooks": [
        {
          "type": "command",
          "command": "ccgate devin"
        }
      ]
    }
  ]
}
```

`"matcher": ""` makes ccgate evaluate every PermissionRequest event Devin emits (`exec`, `edit`, `apply_patch`, MCP tool calls, ...). Restrict by tool name regex if you only want a subset (e.g. `"^exec$"`).

For a user-level registration, nest the same object under the `"hooks"` key of `~/.config/devin/config.json`:

```json
{
  "hooks": {
    "PermissionRequest": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "ccgate devin"
          }
        ]
      }
    ]
  }
}
```

> [!NOTE]
> If `~/.claude/settings.json` already wires `ccgate` (bare) or `ccgate claude` as a PermissionRequest hook, Devin picks that entry up too — and ccgate's Claude-shaped output is accepted by Devin's Claude-compat layer. Registering `ccgate devin` under `.devin/` in addition makes the same request fire *both* hooks; pick one per project.

### Trying a development build without touching dotfiles

For an in-tree dev build of ccgate, drop a project-local hooks file and point it at `go run`:

```jsonc
// <repo>/.devin/hooks.v1.json
{
  "PermissionRequest": [
    {
      "matcher": "",
      "hooks": [
        {
          "type": "command",
          "command": "go run /absolute/path/to/ccgate devin"
        }
      ]
    }
  ]
}
```

`go run` build cache makes second-onwards invocations fast.

## What ccgate sees in the HookInput

ccgate forwards the full `tool_input` JSON to the LLM verbatim, so MCP arguments and `apply_patch` hunk metadata reach the classifier untouched even when ccgate has no typed field for them. The metrics layer pulls a small parsed view (`command` / `description` / `file_path` / `path` / `pattern`) for the JSONL but never strips the raw payload from the LLM message.

Fields ccgate reads from the Devin HookInput:

- `session_id`
- `prompt_id` (per-turn correlation id)
- `tool_use_id`
- `hook_event_name`
- `tool_name` (`exec`, `edit`, `write`, `apply_patch`, `mcp__<server>__<tool>`, ...)
- `tool_input` (typed view)
- `tool_input_raw` (the original JSON payload, forwarded verbatim — the primary surface for inspecting `apply_patch` hunks and MCP arguments)
- `referenced_paths` (best-effort path extraction from `tool_input`. Supported for `read`, `write`, `edit`, `glob`, `grep`, `exec`; `apply_patch` and MCP fall back to reading `tool_input_raw` directly.)

**No `cwd` on the wire.** Devin's PermissionRequest payload does not carry `cwd`. ccgate substitutes the `DEVIN_PROJECT_DIR` environment variable Devin sets for hook processes, falling back to the hook's own working directory.

The Devin system prompt tells the LLM to judge from `tool_name` + `tool_input` + `tool_input_raw` + `cwd`, so it does not invent context that isn't present in the HookInput.

## What ccgate emits

On a decision, ccgate prints Devin's flat decision object to stdout:

```json
{"decision": "approve"}
{"decision": "block", "reason": "..."}
```

No output means fallthrough — Devin shows its own permission prompt.

## Devin-specific state reference

| Aspect                      | Value |
|-----------------------------|-------|
| Tool surface                | `exec`, `edit`, `write`, `apply_patch`, MCP (`mcp__<server>__<tool>`), and other tools Devin gates. Devin hooks fire for every PermissionRequest event regardless of tool kind. |
| Global config               | `~/.config/devin/ccgate.jsonnet` (`%APPDATA%\devin\ccgate.jsonnet` on Windows). |
| State path                  | `$XDG_STATE_HOME/ccgate/devin/` (falls back to `~/.local/state/ccgate/devin/` when unset). |
| Project-local config        | `{repo_root}/.devin/ccgate.local.jsonnet` (untracked-only). |

## Embedded defaults

Run `ccgate devin init | less` to read the full allow / deny / environment guidance compiled into the binary. For how to extend or replace these defaults, see [docs/rule-tuning.md](rule-tuning.md).
