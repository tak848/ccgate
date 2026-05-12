# ccgate -- OpenAI Codex CLI

[日本語版 (docs/ja/codex.md)](ja/codex.md)

Codex-CLI-specific notes for the `ccgate codex` hook.

## Status

- **Upstream schema is the source of truth.** Codex hooks live behind the `[features] codex_hooks = true` flag. Treat the OpenAI [Codex hooks docs](https://developers.openai.com/codex/hooks) as authoritative and re-check before relying on a specific field.
- **Tool-agnostic.** Codex hooks fire for Bash, `apply_patch`, MCP tool calls, and other surfaces. ccgate classifies by `tool_name` + the full `tool_input` JSON, not by tool kind alone.

## Hook registration

Codex CLI lookup order (per OpenAI's [Codex hooks docs](https://developers.openai.com/codex/hooks)):

1. `~/.codex/hooks.json`
2. `~/.codex/config.toml`
3. `<repo>/.codex/hooks.json` (only when the project's `.codex/` layer is trusted)
4. `<repo>/.codex/config.toml` (same trust requirement)

Layers are additive -- a hook registered globally and a hook registered project-local both fire.

### `hooks.json` form (recommended)

```json
{
  "hooks": {
    "PermissionRequest": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "ccgate codex",
            "statusMessage": "ccgate evaluating request"
          }
        ]
      }
    ]
  }
}
```

`"matcher": ""` makes ccgate evaluate every PermissionRequest event Codex emits (Bash, `apply_patch`, MCP tool calls, ...). Restrict by tool name pattern if you only want a subset.

### `config.toml` form

If you want to keep hooks alongside the rest of your Codex config:

```toml
[features]
codex_hooks = true   # Codex hooks live behind this feature flag; keep it set for compatibility.

[[hooks.PermissionRequest]]
matcher = ""

[[hooks.PermissionRequest.hooks]]
type    = "command"
command = "ccgate codex"
statusMessage = "ccgate evaluating request"
```

### Trying a development build without touching dotfiles

Project-local `<repo>/.codex/{hooks.json,config.toml}` only loads when the project is trusted. For an in-tree dev build of ccgate, drop a project-local hooks file (untracked) and point it at `go run`:

```jsonc
// <repo>/.codex/hooks.json (untracked)
{
  "hooks": {
    "PermissionRequest": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "go run /absolute/path/to/ccgate codex",
            "statusMessage": "ccgate (dev) evaluating request"
          }
        ]
      }
    ]
  }
}
```

`go run` build cache makes second-onwards invocations fast. Dotfiles-managed `~/.codex/config.toml` is not touched.

## What ccgate sees in the HookInput

ccgate forwards the full `tool_input` JSON to the LLM verbatim, so MCP arguments and `apply_patch` hunk metadata reach the classifier untouched even when ccgate has no typed field for them. The metrics layer pulls a small parsed view (`command` / `description` / `file_path` / `path` / `pattern`) for the JSONL but never strips the raw payload from the LLM message.

Fields the upstream Codex docs declare and ccgate uses:

- `session_id`
- `transcript_path` (path only; ccgate does not parse the transcript JSONL)
- `cwd`
- `hook_event_name`
- `model` (the AI side's model, e.g. `gpt-5`)
- `turn_id`
- `tool_name` (`Bash`, `apply_patch`, `mcp__<server>__<tool>`, ...)
- `tool_input` (typed view)
- `tool_input_raw` (the original JSON payload, forwarded verbatim — the primary surface for inspecting `apply_patch` hunks and MCP arguments)
- `referenced_paths` (best-effort path extraction from `tool_input`. Supported for `Bash`; `apply_patch` and MCP fall back to reading `tool_input_raw` directly.)

Codex does not deliver Claude's `permission_mode`, `permission_suggestions`, `recent_transcript`, or `settings_permissions`. The system prompt tells the LLM to judge from `tool_name` + `tool_input` + `tool_input_raw` + `cwd` alone, so it does not invent context that isn't there.

## Codex-specific state reference

| Aspect                      | Value |
|-----------------------------|-------|
| Tool surface                | `Bash`, `apply_patch`, MCP (`mcp__<server>__<tool>`). Codex hooks fire for every PermissionRequest event regardless of tool kind. |
| State path                  | `$XDG_STATE_HOME/ccgate/codex/` (falls back to `~/.local/state/ccgate/codex/` when unset). |
| Project-local config        | `{repo_root}/.codex/ccgate.local.jsonnet` (untracked-only, project-trust required). |

## Defaults snapshot

The embedded Codex defaults file (`internal/cmd/codex/defaults.jsonnet`) ships allow / deny / environment guidance tuned to the Codex tool surface. Notable entries:

- `allow`: read-only Bash inspection, in-workspace writes (`apply_patch` hunks under cwd / repo_root, project file edits the AI is doing right now), project-script build/test, repo-confined package install, feature-branch git, MCP tools on user-trusted servers whose side effects stay within the user-authorized scope.
- `deny`: pipe-to-shell of remote content, one-shot remote package execution (`npx` / `pnpx` / `bunx` against unfamiliar packages), `sudo`, out-of-workspace `rm -rf` / `mv` / `apply_patch` hunks, destructive git on protected branches, unrestricted network out (`nc` / `ssh` / `scp` / `ftp` to non-allowlisted hosts), MCP tools advertising destructive side effects without an explicit per-rule allow.
- `environment`: heterogeneous tool surface, trusted-repo boundary, path scope rule. ccgate replaces the upstream approval prompt, so the guidance defaults to allow / deny when it applies and reserves fallthrough for genuinely ambiguous cases. `recent_transcript` is not delivered to Codex hooks, so the prompt tells the LLM not to assume one.

In-workspace `apply_patch` is in `allow` because the user installed ccgate specifically to avoid being prompted for routine in-repo edits; out-of-workspace patch hunks are denied via the existing deny rule. If you want to be more conservative, add a project-local rule that narrows the apply_patch allow scope (e.g. only specific subtrees).
