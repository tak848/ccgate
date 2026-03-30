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

### 1. Create a config file

Write your rules in `~/.claude/ccgate.jsonnet`.
See [example/ccgate.jsonnet](example/ccgate.jsonnet) for reference.

```jsonnet
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    timeout_ms: 40000,
  },
  allow: [
    'Read-Only Operations: ...',
  ],
  deny: [
    'Git Destructive: force push, deleting remote branches, ...',
  ],
  environment: [
    '**Trusted repo**: The git repository the session started in.',
  ],
}
```

Place `ccgate.schema.json` in the same directory for editor autocompletion.

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

Set the `CC_AUTOMODE_ANTHROPIC_API_KEY` or `ANTHROPIC_API_KEY` environment variable.

## Configuration

### Config file loading order

1. `~/.claude/ccgate.jsonnet` — Base config
2. `{repo_root}/ccgate.local.jsonnet` — Project-local (untracked files only)
3. `{repo_root}/.claude/ccgate.local.jsonnet` — Project-local (untracked files only)

Later files merge into earlier ones (allow/deny/environment are appended, provider fields are overwritten).
Project-local configs are only loaded if **not tracked by Git**.

### Config fields

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `provider.name` | string | `"anthropic"` | Provider name. Only `"anthropic"` is supported. |
| `provider.model` | string | `"claude-haiku-4-5"` | Model name (e.g. `claude-haiku-4-5`, `claude-sonnet-4-6`, `claude-opus-4-6`) |
| `provider.timeout_ms` | int | `20000` | API timeout (ms) |
| `log_path` | string | `"~/.claude/logs/ccgate.log"` | Log file path. Supports `~` for home directory. |
| `log_disabled` | bool | `false` | Disable logging entirely |
| `log_max_size` | int | `5242880` | Max log file size in bytes before rotation (default 5MB) |
| `allow` | string[] | `[]` | Allow rules |
| `deny` | string[] | `[]` | Deny rules (mandatory) |
| `environment` | string[] | `[]` | Environment context |

## Logging

Logs are written to `~/.claude/logs/ccgate.log` by default with 5 MB rotation (`.log.1`).

To change the log path or disable logging:

```jsonnet
{
  log_path: '~/my-logs/ccgate.log',
  // log_disabled: true,
}
```

## Development

```bash
mise run build    # Build binary
mise run test     # Run tests
mise run vet      # Run go vet
```

## License

MIT
