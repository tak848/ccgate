# ccgate

[![CI](https://github.com/tak848/ccgate/actions/workflows/ci.yml/badge.svg)](https://github.com/tak848/ccgate/actions/workflows/ci.yml)
[![release](https://github.com/tak848/ccgate/actions/workflows/release.yml/badge.svg)](https://github.com/tak848/ccgate/releases)

A [Claude Code](https://docs.anthropic.com/en/docs/claude-code) **PermissionRequest** hook that delegates tool-execution permission decisions to an LLM (Claude Haiku) based on rules defined in a configuration file.

[日本語ドキュメント](docs/README.ja.md)

## How it works

```
Claude Code (PermissionRequest hook)
  │
  │  stdin: HookInput JSON
  ▼
ccgate
  ├── Load config (~/.claude/ccgate.jsonnet)
  ├── Build context (git repo, worktree, paths, transcript)
  ├── Call Claude Haiku API (Structured Output)
  └── stdout: allow / deny / fallthrough
```

1. Claude Code invokes `ccgate` before executing a tool
2. `ccgate` embeds allow/deny rules from the jsonnet config into a system prompt, sends tool info, git context, and recent conversation history to Haiku
3. Returns Haiku's decision to Claude Code

## Installation

### mise (recommended)

```bash
mise use -g go:github.com/tak848/ccgate
```

### go install

```bash
go install github.com/tak848/ccgate@latest
```

### GitHub Releases

Download a binary from [Releases](https://github.com/tak848/ccgate/releases) and place it on your `PATH`.

## Setup

### 1. Create a config file (optional)

ccgate ships with sensible default safety rules. Without any config file, it works out of the box.

To customize, output the defaults and edit:

```bash
ccgate init > ~/.claude/ccgate.jsonnet
```

See [example/ccgate.jsonnet](example/ccgate.jsonnet) for reference.
The `$schema` field points to the hosted JSON Schema for editor autocompletion.

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
            "command": "ccgate"
          }
        ]
      }
    ]
  }
}
```

### 3. API key

Set the `CCGATE_ANTHROPIC_API_KEY` or `ANTHROPIC_API_KEY` environment variable.

## Configuration

### Config file loading order

1. **Embedded defaults** — Built-in safety rules (fallback when no global config exists)
2. `~/.claude/ccgate.jsonnet` — Global config (**replaces** embedded defaults entirely)
3. `{repo_root}/ccgate.local.jsonnet` — Project-local (untracked files only, **appended**)
4. `{repo_root}/.claude/ccgate.local.jsonnet` — Project-local (untracked files only, **appended**)

If a global config file exists, embedded defaults are not used. The global config is the complete base.
Project-local configs always append to the base (allow/deny/environment are appended, provider fields are overwritten).
Project-local configs are only loaded if **not tracked by Git**.

> **Note:** This "global replaces defaults, project-local only appends" asymmetry is a known wart — users cannot narrow or override rules from the project layer today. Tracked as a breaking-change refactor in [#38](https://github.com/tak848/ccgate/issues/38).

### Config fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `provider.name` | string | `"anthropic"` | Provider name. Only `"anthropic"` is supported. |
| `provider.model` | string | `"claude-haiku-4-5"` | Model name (e.g. `claude-haiku-4-5`, `claude-sonnet-4-6`, `claude-opus-4-6`) |
| `provider.timeout_ms` | int | `20000` | API timeout (ms) |
| `log_path` | string | `"~/.claude/logs/ccgate.log"` | Log file path. Supports `~` for home directory. |
| `log_disabled` | bool | `false` | Disable logging entirely |
| `log_max_size` | int | `5242880` | Max log file size in bytes before rotation (default 5MB) |
| `metrics_path` | string | `"$XDG_STATE_HOME/ccgate/metrics.jsonl"` | Metrics JSONL file path. Supports `~` for home directory. |
| `metrics_disabled` | bool | `false` | Disable metrics collection entirely |
| `metrics_max_size` | int | `2097152` | Max metrics file size in bytes before rotation (default 2MB) |
| `fallthrough_strategy` | `"ask"` / `"allow"` / `"deny"` | `"ask"` | How to resolve LLM uncertainty (`fallthrough`). See [Unattended automation](#unattended-automation-fallthrough_strategy). |
| `allow` | string[] | `[]` | Allow guidance rules (natural language, interpreted by the LLM) |
| `deny` | string[] | `[]` | Deny guidance rules (mandatory). Supports inline `deny_message:` hints |
| `environment` | string[] | `[]` | Context strings passed to the LLM (trust level, policies, etc.) |

## Default Rules

When no global config file exists, ccgate uses built-in default rules:

**Allow:** Read-only operations, local development commands, git feature branch operations, package manager installs.

**Deny:** Download-and-execute (curl|bash), direct tool invocation (npx, pnpx, etc.), git destructive operations, out-of-repo deletion, sibling checkout / worktree confusion.

Run `ccgate init` to see the full default configuration. To customize, redirect to a file and edit:

```bash
ccgate init > ~/.claude/ccgate.jsonnet    # Global config (replaces defaults)
ccgate init -p > ccgate.local.jsonnet     # Project-local template (appended)
```

## Unattended automation (`fallthrough_strategy`)

By default, when the LLM is not confident enough to decide, ccgate returns `fallthrough` and Claude Code shows its interactive permission prompt. That is the right behavior for a human-in-the-loop session but blocks schedulers, bots, and any unattended run that has no one to click "approve".

Set `fallthrough_strategy` to force a fixed verdict on LLM uncertainty:

```jsonnet
{
  // Safer: when the LLM is unsure, refuse. Recommended for anything that runs unattended.
  fallthrough_strategy: 'deny',
}
```

Values:

- `ask` (default) — defer to Claude Code's prompt. No behavior change.
- `deny` — auto-refuse uncertain operations. The deny message tells Claude not to re-ask and not to work around the restriction, so the run keeps moving instead of stalling.
- `allow` — auto-approve uncertain operations. **Riskier**: you are letting ccgate green-light operations the LLM itself was unsure about. Also note that Claude Code's hook spec only delivers `decision.message` on `deny`, so Claude never sees a warning on forced-allow. Pick this only if that trade-off is acceptable.

Only LLM-driven uncertainty is affected. Truncated/refused API responses, missing API keys, `bypassPermissions`/`dontAsk` mode, and `ExitPlanMode` / `AskUserQuestion` continue to defer to Claude Code regardless — so `fallthrough_strategy=allow` cannot silently auto-approve a request the LLM never actually classified.

`ccgate metrics` surfaces how often the override fired through the `F.Allow` / `F.Deny` columns in the daily table (and `forced_allow` / `forced_deny` in JSON output), so you can audit whether the strategy you chose is making decisions you are comfortable with.

## Logging

Logs are written to `~/.claude/logs/ccgate.log` by default with 5 MB rotation (`.log.1`).

To change the log path or disable logging:

```jsonnet
{
  log_path: '~/my-logs/ccgate.log',
  // log_disabled: true,
}
```

## Metrics

Every invocation is recorded as a JSONL entry (`$XDG_STATE_HOME/ccgate/metrics.jsonl` by default, 2 MB rotation). `ExitPlanMode` and `AskUserQuestion` entries are kept in the raw log for audit but excluded from the aggregates (`automation_rate`, `Fall`, per-tool totals, top sections) because ccgate passes them through without evaluating. To summarize:

```bash
ccgate metrics                     # last 7 days, TTY table
ccgate metrics --days 30           # wider window
ccgate metrics --json              # machine-readable output
ccgate metrics --details 5         # top-5 fallthrough / deny commands
ccgate metrics --details 0         # suppress the drill-down sections
```

The daily table shows per-day counts (Allow, Deny, Fall, F.Allow, F.Deny, Err), automation rate, average latency, and token usage. The "Top fallthrough commands" / "Top deny commands" drill-downs surface which operations you could eliminate by adding a permission rule.

To move or disable the metrics file:

```jsonnet
{
  metrics_path: '~/my-state/ccgate-metrics.jsonl',
  // metrics_disabled: true,
}
```

## Known limitations

- **Plan mode correctness is prompt-only.** Under `permission_mode == "plan"`, ccgate relies on the LLM plus prose in the system prompt both to (a) reject implementation-side writes and (b) allow read-only queries without requiring an allow-guidance match. Since it is prose, either side can misfire — some "safe-looking" writes may slip through, and some novel read-only commands may fall through to the user. Tracked in [#37](https://github.com/tak848/ccgate/issues/37).
- **Config file layering is asymmetric.** `~/.claude/ccgate.jsonnet` *replaces* embedded defaults while project-local files only *append*. Narrowing / overriding rules from the project layer is not supported today. Tracked as a breaking-change refactor in [#38](https://github.com/tak848/ccgate/issues/38).

## Development

```bash
mise run build    # Build binary
mise run test     # Run tests
mise run vet      # Run go vet
```

## License

MIT
