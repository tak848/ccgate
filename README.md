# ccgate

[![CI](https://github.com/tak848/ccgate/actions/workflows/ci.yml/badge.svg)](https://github.com/tak848/ccgate/actions/workflows/ci.yml)
[![release](https://github.com/tak848/ccgate/actions/workflows/release.yml/badge.svg)](https://github.com/tak848/ccgate/releases)

A **PermissionRequest** hook for AI coding tools that delegates tool-execution permission decisions to an LLM (Claude Haiku) based on rules defined in a configuration file.

Supported targets:

- **[Claude Code](https://docs.anthropic.com/en/docs/claude-code)** — stable
- **[OpenAI Codex CLI](https://developers.openai.com/codex/hooks)** — experimental

[日本語ドキュメント](docs/ja/README.md)

## How it works

```
Claude Code / Codex CLI (PermissionRequest hook)
  │
  │  stdin: HookInput JSON
  ▼
ccgate
  ├── Load config (~/.claude/ccgate.jsonnet  or  ~/.codex/ccgate.jsonnet)
  ├── Build context (git repo, paths, recent transcript [Claude only])
  ├── Call Claude Haiku API (Structured Output)
  └── stdout: allow / deny / fallthrough
```

1. The AI tool invokes `ccgate` before executing a tool.
2. `ccgate` embeds allow/deny rules from the jsonnet config into a system prompt, sends tool info, git context, and (for Claude) recent conversation history to Haiku.
3. Returns Haiku's decision to the AI tool.

## CLI

```
ccgate                         Read HookInput JSON from stdin (Claude Code hook).
                               Equivalent to 'ccgate claude'. Permanent default — never
                               deprecated, so existing ~/.claude/settings.json entries
                               using "command": "ccgate" keep working forever.
ccgate claude                  Same as bare ccgate, explicit form (recommended for new users).
ccgate claude init [-p|-o|-f]  Output the embedded Claude Code defaults.
ccgate claude metrics [...]    Show Claude Code usage metrics.

ccgate codex                   Read HookInput JSON from stdin (Codex CLI hook, experimental).
ccgate codex init [-o|-f]      Output the embedded Codex CLI defaults.
ccgate codex metrics [...]     Show Codex CLI usage metrics.
```

> Top-level `ccgate init` and `ccgate metrics` are not real subcommands — they print a one-line pointer to the per-target form and exit `2`. The bare `ccgate` hook invocation is a different code path and works as documented above.

## Installation

### mise (recommended)

Requires mise `2026.4.20` or later. Earlier releases bundle an aqua registry snapshot from before ccgate was added.

```bash
mise use -g aqua:tak848/ccgate
```

To try ccgate without installing it globally (similar to `npx` / `uvx`):

```bash
mise exec aqua:tak848/ccgate -- ccgate --version
```

If you want to keep this no-install style for the hook itself, set the hook `command` to `mise exec aqua:tak848/ccgate -- ccgate claude` (or `... -- ccgate codex`) in your settings. Each hook invocation pays the launcher startup cost; for day-to-day use, `mise use -g` above is recommended.

### aqua

Via the [aqua](https://aquaproj.github.io/) standard registry (requires registry `v4.498.0` or later — ccgate's first registered version). In an aqua-managed project (run `aqua init` first if you don't have an `aqua.yaml` yet):

```bash
aqua g -i tak848/ccgate
aqua i
```

For a [global aqua config](https://aquaproj.github.io/docs/tutorial/global-config), follow aqua's own tutorial.

### go install

```bash
go install github.com/tak848/ccgate@latest
```

### GitHub Releases

Download a binary from [Releases](https://github.com/tak848/ccgate/releases) and place it on your `PATH`.

## Setup — Claude Code

### 1. Create a config file (optional)

ccgate ships with sensible default safety rules. Without any config file, it works out of the box.

To customize:

```bash
ccgate claude init > ~/.claude/ccgate.jsonnet
```

The `$schema` field points to [`schemas/claude.schema.json`](schemas/claude.schema.json) for editor autocompletion.

### 2. Register as a Claude Code hook

`~/.claude/settings.json`:

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

`"command": "ccgate"` (no subcommand) is also accepted and will keep working forever — bare `ccgate` is the canonical Claude Code hook invocation.

If `ccgate` is not on your `PATH` (e.g. when relying on `mise exec` instead of a global install), set the hook `command` to the equivalent invocation, or use an absolute path to the binary.

### 3. API key

Set the API key for your chosen provider. `CCGATE_*_API_KEY` is preferred and overrides the bare variable, so you can keep ccgate's key separate from the AI tool's own key.

| `provider.name` | Preferred                  | Fallback             | Get API key |
|-----------------|----------------------------|----------------------|-------------|
| `anthropic`     | `CCGATE_ANTHROPIC_API_KEY` | `ANTHROPIC_API_KEY`  | <https://platform.claude.com/settings/keys> |
| `openai`        | `CCGATE_OPENAI_API_KEY`    | `OPENAI_API_KEY`     | <https://platform.openai.com/api-keys>      |
| `gemini`        | `CCGATE_GEMINI_API_KEY`    | `GEMINI_API_KEY`     | <https://aistudio.google.com/app/api-keys>  |

## Setup — Codex CLI (experimental)

> Codex hooks are upstream-experimental. Schema and behavior may change.

### 1. Create a config file (optional)

```bash
ccgate codex init > ~/.codex/ccgate.jsonnet
```

The defaults follow Claude Code parity (allow + deny + environment guidance). Codex hooks fire for Bash, `apply_patch`, MCP tool calls, and other tool surfaces; the rules cover all of them and the system prompt instructs the LLM to classify by `tool_name` + the full `tool_input` JSON, not just Bash command shape.

### 2. Register as a Codex hook

Codex reads hooks from `~/.codex/hooks.json` and `~/.codex/config.toml` (with `<repo>/.codex/{hooks.json,config.toml}` overlays once the project is trusted). Pick whichever fits your setup.

`~/.codex/hooks.json`:

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

`~/.codex/config.toml`:

```toml
[features]
codex_hooks = true   # required: Codex hooks are still experimental and gated behind this feature flag

[[hooks.PermissionRequest]]
matcher = ""

[[hooks.PermissionRequest.hooks]]
type    = "command"
command = "ccgate codex"
statusMessage = "ccgate evaluating request"
```

See [docs/codex.md](docs/codex.md) for the full lookup order, project-local overlays, and a `go run` recipe for in-tree dev builds. Refer to the upstream [Codex hooks documentation](https://developers.openai.com/codex/hooks) for the authoritative schema.

### 3. API key

Same env vars as Claude Code — see the [provider table](#3-api-key).

## Configuration

### Config file loading order (per target)

| Order | Claude Code | Codex CLI |
|------:|-------------|-----------|
| 1     | Embedded defaults (always applied as the base) | Embedded defaults (same) |
| 2     | `~/.claude/ccgate.jsonnet` — global (layered on top) | `~/.codex/ccgate.jsonnet` — global (same) |
| 3     | `{repo_root}/.claude/ccgate.local.jsonnet` — project-local (untracked only, layered on top) | `{repo_root}/.codex/ccgate.local.jsonnet` — project-local (same) |

All three layers compose with the same rules:

- **Lists** — `allow` / `deny` / `environment` **replace** the value carried over from earlier layers when the layer sets them (even to `[]`). The `append_*` siblings (`append_allow`, `append_deny`, `append_environment`) **add** entries on top of whatever the earlier layers produced.
- **Scalars** — `provider.*`, `log_*`, `metrics_*`, `fallthrough_strategy` are overwritten per-field when the layer sets them, otherwise the earlier value survives.

So `~/.<target>/ccgate.jsonnet` that only sets `provider.model` keeps every embedded `allow` / `deny` rule. A `~/.<target>/ccgate.jsonnet` that writes `allow: [...]` swaps the embedded allow list for its own (this is what most pre-v0.6 global configs already did, so it stays idempotent). Project-local configs typically use `append_deny: [...]` / `append_environment: [...]` to add restrictions on top of the inherited base.

Project-local configs are loaded only when **not tracked by Git**.


### Config fields

| Field                    | Type                              | Default                                                                       | Description                                                                                            |
|--------------------------|-----------------------------------|-------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------|
| `provider.name`          | string                            | `"anthropic"`                                                                 | Provider name. One of `"anthropic"`, `"openai"`, `"gemini"`.                                            |
| `provider.model`         | string                            | `"claude-haiku-4-5"`                                                          | Model name. Examples: `claude-haiku-4-5` / `claude-sonnet-4-6` (anthropic), `gpt-5.4-nano-2026-03-17` (openai), `gemini-3-flash-preview` (gemini). |
| `provider.timeout_ms`    | int                               | `20000`                                                                       | API timeout (ms). `0` = no timeout.                                                                    |
| `log_path`               | string                            | `$XDG_STATE_HOME/ccgate/<target>/ccgate.log`                                  | Log file path. Supports `~` for home directory.                                                        |
| `log_disabled`           | bool                              | `false`                                                                       | Disable logging entirely                                                                               |
| `log_max_size`           | int                               | `5242880`                                                                     | Max log file size in bytes before rotation (default 5MB). `0` = no rotation.                           |
| `metrics_path`           | string                            | `$XDG_STATE_HOME/ccgate/<target>/metrics.jsonl`                               | Metrics JSONL file path.                                                                               |
| `metrics_disabled`       | bool                              | `false`                                                                       | Disable metrics collection entirely                                                                    |
| `metrics_max_size`       | int                               | `2097152`                                                                     | Max metrics file size in bytes before rotation (default 2MB). `0` = no rotation.                       |
| `fallthrough_strategy`   | `"ask"` / `"allow"` / `"deny"`    | `"ask"`                                                                       | How to resolve LLM uncertainty (`fallthrough`). See [Unattended automation](#unattended-automation-fallthrough_strategy). |
| `allow`                  | string[]                          | `[]`                                                                          | Allow guidance rules. **Replaces** the value carried over from earlier layers when set.                |
| `deny`                   | string[]                          | `[]`                                                                          | Deny guidance rules (mandatory). Supports inline `deny_message:` hints. Same replace semantics as `allow`. |
| `environment`            | string[]                          | `[]`                                                                          | Context strings passed to the LLM (trust level, policies, etc.). Same replace semantics as `allow`.    |
| `append_allow`           | string[]                          | `[]`                                                                          | Allow guidance rules **appended** on top of the carried-over list. Use this in project-local configs.   |
| `append_deny`            | string[]                          | `[]`                                                                          | Deny guidance rules appended on top of the carried-over list.                                          |
| `append_environment`     | string[]                          | `[]`                                                                          | Environment context appended on top of the carried-over list.                                          |

`<target>` is `claude` or `codex` depending on which hook is invoked. When `XDG_STATE_HOME` is unset, ccgate falls back to `~/.local/state/ccgate/<target>/...`.

### Switching to OpenAI / Gemini

Set `provider.name` (and optionally `provider.model`) in any layer:

```jsonnet
{
  provider: {
    name: 'openai',
    model: 'gpt-5.4-nano-2026-03-17',
  },
}
```

Then export the matching API key (`CCGATE_OPENAI_API_KEY` / `CCGATE_GEMINI_API_KEY` — see the [provider table](#3-api-key)). If the key is missing, ccgate falls through to the upstream tool's permission prompt, so flipping providers cannot break the hook.

## Default Rules

ccgate ships built-in default rules per target. They are always applied as the base; your global / project-local configs layer on top.

**Allow:** Read-only operations, local development commands (build / test against project scripts), git feature-branch operations, package-manager installs scoped to the repo.

**Deny:** Download-and-execute (`curl|bash`), direct one-shot remote package execution (`npx`/`pnpx`/`bunx` etc.), git destructive operations on protected branches, out-of-repo deletion, privilege escalation.

Run `ccgate claude init` / `ccgate codex init` to inspect the full default configuration. The `init` output is the **embedded defaults** -- a reference document, not the starting template. For your own overrides, write a minimal jsonnet that adds / overrides only what you need:

```bash
ccgate claude init           | less                   # Read the embedded Claude defaults.
ccgate codex  init           | less                   # Same for Codex.
ccgate claude init -p > .claude/ccgate.local.jsonnet  # Project-local skeleton you can extend.
ccgate codex  init -p > .codex/ccgate.local.jsonnet   # Same for Codex.
```

Need to drop one of the embedded default rules? That requires an explicit reset/override syntax which does not exist yet -- open an issue describing the rule and your motivation.

## Unattended automation (`fallthrough_strategy`)

By default, when the LLM is not confident enough to decide, ccgate returns `fallthrough` and the AI tool shows its interactive permission prompt. That is the right behavior for a human-in-the-loop session but blocks schedulers, bots, and any unattended run.

Set `fallthrough_strategy` to force a fixed verdict on LLM uncertainty:

```jsonnet
{
  // Safer: when the LLM is unsure, refuse. Recommended for anything that runs unattended.
  fallthrough_strategy: 'deny',
}
```

Values:

- `ask` (default) — defer to the upstream tool's prompt. No behavior change.
- `deny` — auto-refuse uncertain operations. The deny message tells the AI not to re-ask and not to work around the restriction, so the run keeps moving instead of stalling.
- `allow` — auto-approve uncertain operations. **Riskier**: you are letting ccgate green-light operations the LLM itself was unsure about. Both Claude Code and Codex only deliver `decision.message` on `deny`, so the AI never sees a warning on forced-allow. Pick this only if that trade-off is acceptable.

Only LLM-driven uncertainty is affected. Truncated/refused API responses, missing API keys, `bypassPermissions`/`dontAsk` mode (Claude only), and `ExitPlanMode` / `AskUserQuestion` (Claude only) continue to defer to the upstream tool regardless — so `fallthrough_strategy=allow` cannot silently auto-approve a request the LLM never actually classified.

`ccgate <target> metrics` surfaces how often the override fired through the `F.Allow` / `F.Deny` columns in the daily table (and `forced_allow` / `forced_deny` in JSON output), so you can audit whether the strategy you chose is making decisions you are comfortable with.

## Logging & metrics

Logs and metrics live under `$XDG_STATE_HOME/ccgate/<target>/` (or `~/.local/state/ccgate/<target>/` when `XDG_STATE_HOME` is unset):

- `$XDG_STATE_HOME/ccgate/claude/{ccgate.log,metrics.jsonl}` — Claude Code
- `$XDG_STATE_HOME/ccgate/codex/{ccgate.log,metrics.jsonl}` — Codex CLI

Both files rotate on size (`.log.1`, `.jsonl.1`).

Override paths in jsonnet are respected — set `log_path` / `metrics_path` to put them anywhere.

```bash
ccgate claude metrics                 # last 7 days, TTY table
ccgate claude metrics --days 30       # wider window
ccgate claude metrics --json          # machine-readable output
ccgate claude metrics --details 5     # top-5 fallthrough / deny commands
ccgate claude metrics --details 0     # suppress the drill-down sections
ccgate codex  metrics --days 7        # same shape, codex side
```

The daily table shows per-day counts (Allow, Deny, Fall, F.Allow, F.Deny, Err), automation rate, average latency, and token usage. The "Top fallthrough commands" / "Top deny commands" drill-downs surface which operations you could eliminate by adding a permission rule.

## Known limitations

- **Plan mode correctness is prompt-only (Claude only).** Under `permission_mode == "plan"`, ccgate relies on the LLM plus prose in the system prompt to (a) reject implementation-side writes and (b) allow read-only queries without requiring an allow-guidance match. Either side can misfire. Tracked in [#37](https://github.com/tak848/ccgate/issues/37).
- **No surgical reset for a single embedded default rule.** A layer can either **replace** a list wholesale (`allow: [...]`) or **append** to it (`append_allow: [...]`). Removing one specific embedded `allow` / `deny` rule while keeping the rest of the embedded list requires re-stating the whole list under `allow:` / `deny:` minus that one entry.
- **Codex hook is upstream-experimental.** Schema and behavior may change. ccgate does not currently expose `permission_mode` from Codex, parse the Codex transcript JSONL, ingest `~/.codex/config.toml`, or apply MCP-server-specific trust hints; classification runs from `tool_name` + `tool_input` + `cwd` only.

## Documentation

- [docs/claude.md](docs/claude.md) — Claude Code specifics
- [docs/codex.md](docs/codex.md) — Codex CLI specifics
- [docs/configuration.md](docs/configuration.md) — config layering, fallthrough_strategy, metrics, known limits
- [日本語ドキュメント (docs/ja/)](docs/ja/README.md)

## Development

```bash
mise run build    # Build binary
mise run test     # Run tests
mise run vet      # Run go vet
mise run schema   # Regenerate schemas/{claude,codex}.schema.json
```

## License

MIT
