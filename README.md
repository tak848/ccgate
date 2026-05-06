# ccgate

[![CI](https://github.com/tak848/ccgate/actions/workflows/ci.yml/badge.svg)](https://github.com/tak848/ccgate/actions/workflows/ci.yml)
[![release](https://github.com/tak848/ccgate/actions/workflows/release.yml/badge.svg)](https://github.com/tak848/ccgate/releases)

A **PermissionRequest** hook for AI coding tools that delegates tool-execution permission decisions to an LLM (Claude Haiku) based on rules defined in a configuration file.

![ccgate in action: a safe `echo` is allowed while `curl ... | bash` is denied with a deny_message](docs/images/gate.png)

Supported targets:

- **[Claude Code](https://docs.anthropic.com/en/docs/claude-code)**
- **[OpenAI Codex CLI](https://developers.openai.com/codex/hooks)**

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

ccgate codex                   Read HookInput JSON from stdin (Codex CLI hook).
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

Customize at either layer; both follow the same merge rules:

- `~/.claude/ccgate.jsonnet` — your global config across every Claude Code session.
- `<repo>/.claude/ccgate.local.jsonnet` — project-local override (untracked-only, see [Configuration](docs/configuration.md#where-ccgate-looks-for-config)). Layered on top of the global config.

Either file can use one (or both) of these shapes:

- **Add to the inherited list** (`append_allow` / `append_deny` / `append_environment`). Keeps the embedded baseline + anything earlier layers added; quality improvements ccgate ships in future releases land automatically.

  ```jsonnet
  {
    ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/claude.schema.json',
    append_deny: [
      'Production database access: any psql / mysql connection to a *.prod.* host. deny_message: production access is gated behind the runbook.',
    ],
  }
  ```

- **Replace the inherited list wholesale** (`allow:` / `deny:` set the lists, not append). Maximum control; you take ownership of keeping your `allow` / `deny` in line with future ccgate releases. `ccgate claude init | less` prints the embedded defaults you can copy-paste from.

A common pattern is to keep the global config close to the embedded defaults (just `append_deny` for personal preferences) and put project-specific guardrails in the project-local file. The two layers are independent: a project may `append_*` even if the global file replaces the lists, or vice versa.

The `$schema` line enables editor autocompletion either way.

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

To route through an OpenAI- or Anthropic-compatible proxy (LiteLLM proxy, Azure OpenAI, on-prem gateway, ...), set `provider.base_url` and use the matching native provider — see [Routing through a compatible proxy](#routing-through-a-compatible-proxy).

## Setup — Codex CLI

> Codex hooks themselves are still experimental upstream and live behind `features.codex_hooks = true`; their schema may change. Treat the [Codex hooks docs](https://developers.openai.com/codex/hooks) as the source of truth before relying on a specific field.

### 1. Create a config file (optional)

ccgate ships sensible defaults for Codex too. Same merge rules as the Claude side: customize at either `~/.codex/ccgate.jsonnet` (global) or `<repo>/.codex/ccgate.local.jsonnet` (project-local, untracked-only), and at either layer use `append_*` to add on top of what's inherited or `allow:` / `deny:` to replace the list wholesale.

```jsonnet
{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/codex.schema.json',
  append_deny: [
    'Production database access: any psql / mysql connection to a *.prod.* host. deny_message: production access is gated behind the runbook.',
  ],
}
```

`ccgate codex init | less` prints the embedded defaults if you want to see what you would be replacing.

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
codex_hooks = true   # required: Codex hooks live behind this feature flag

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
- **Scalars** — `log_*`, `metrics_*`, `fallthrough_strategy` are overwritten per-field when the layer sets them, otherwise the earlier value survives.
- **`provider` block** — a layer that writes `provider` **replaces the entire block** (`name` + `model` + `base_url` + `auth` + `timeout_ms`, i.e. every `provider.*` field). Layers that omit `provider` inherit the earlier block unchanged. The block is replaced as a unit because the fields are tightly coupled (different `name` typically means a different `model` namespace and `base_url`); per-field merge would let stale settings from a lower layer leak through. Important: when a project-local config restates `provider`, it must repeat any `auth` block from the global layer too — otherwise the helper config is silently dropped on that project.

So `~/.<target>/ccgate.jsonnet` that wants to bump just the model still has to restate the whole `provider` block (e.g. `provider: {name: 'anthropic', model: 'claude-sonnet-4-6'}`). A `~/.<target>/ccgate.jsonnet` that writes `allow: [...]` swaps the embedded allow list for its own (this is what most pre-v0.6 global configs already did, so it stays idempotent). Project-local configs typically use `append_deny: [...]` / `append_environment: [...]` to add restrictions on top of the inherited base.

Project-local configs are loaded only when **not tracked by Git**.


### Config fields

| Field                    | Type                              | Default                                                                       | Description                                                                                            |
|--------------------------|-----------------------------------|-------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------|
| `provider.name`          | string                            | `"anthropic"`                                                                 | Provider name. One of `"anthropic"`, `"openai"`, `"gemini"`.                                            |
| `provider.model`         | string                            | `"claude-haiku-4-5"`                                                          | Model name. Examples: `claude-haiku-4-5` / `claude-sonnet-4-6` (anthropic), `gpt-5.4-nano-2026-03-17` (openai), `gemini-3-flash-preview` (gemini). When routing through a compatible proxy, use whatever model name the proxy exposes (e.g. `anthropic/claude-haiku-4-5`). |
| `provider.base_url`      | string                            | `""`                                                                          | Override the provider's API base URL. Empty = use the SDK default. Use this to route through an OpenAI- / Anthropic-compatible proxy (LiteLLM proxy, Azure OpenAI, on-prem gateway, regional endpoint, ...). |
| `provider.auth`          | object (`{type, ...}`)            | (omit = env var)                                                              | Discriminated union for short-lived / rotating credentials. `type=exec` (run a shell command), `type=file` (read a rotator-managed file). See [docs/api-key-helper.md](docs/api-key-helper.md) for full reference. |
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

> **Avoid reasoning models** (`gpt-5`, `gpt-5-mini`, `gpt-5-nano`, `gpt-5-chat`, `o1*`, `o3*`, `o4-mini`): they reject `temperature=0` (every request fails) and add seconds of chain-of-thought that ccgate's classification doesn't need. Use `gpt-4.1-nano`, `gpt-4o-mini`, or `gpt-5.4-nano-2026-03-17`.

### Routing through a compatible proxy

ccgate uses each provider SDK's standard chat / messages endpoint, so it works against any **OpenAI- or Anthropic-compatible** endpoint — including [LiteLLM proxy](https://docs.litellm.ai/docs/proxy/quick_start), Azure OpenAI, on-prem gateways, and regional endpoints. Pick the protocol the proxy speaks and set `provider.base_url`.

`provider.base_url` is passed verbatim to the underlying SDK's `WithBaseURL`, so the path you write follows that SDK's convention — **not** something ccgate normalizes:

| `provider.name` | Underlying SDK | Default base URL                | What you put in `base_url`           |
|-----------------|----------------|---------------------------------|--------------------------------------|
| `openai`        | `openai-go`    | `https://api.openai.com/v1/`    | host **+ `/v1`** (SDK appends `chat/completions`) |
| `anthropic`     | `anthropic-sdk-go` | `https://api.anthropic.com/` | host root only (SDK appends `/v1/messages`) |
| `gemini`        | `openai-go` against Gemini's OpenAI-compat endpoint | `https://generativelanguage.googleapis.com/v1beta/openai/` | host **+ `/v1beta/openai`** if overriding |

**OpenAI-compatible endpoint** (e.g. LiteLLM proxy's `/v1/chat/completions`):

```jsonnet
{
  provider: {
    name: 'openai',
    model: 'anthropic/claude-haiku-4-5', // whatever the proxy exposes
    base_url: 'https://your-proxy.example/v1',
  },
}
```

Export the proxy's API key as `CCGATE_OPENAI_API_KEY`. The trailing `/v1` is required because the OpenAI SDK appends `/chat/completions` directly to the base URL.

**Anthropic-compatible endpoint** (e.g. LiteLLM proxy's `/v1/messages`):

```jsonnet
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    base_url: 'https://your-proxy.example',
  },
}
```

Export the proxy's API key as `CCGATE_ANTHROPIC_API_KEY`. The Anthropic SDK appends `/v1/messages` itself, so the base URL stops at the host root.

### Short-lived / rotating API keys

When the credential rotates faster than a static env var can keep up (AWS STS, Vertex ADC, OpenAI-compatible gateways with virtual keys, internal key brokers), use `provider.auth`. It's a discriminated union over two shapes — pick the one that matches your setup:

```jsonnet
// Run a shell helper to mint a credential on demand
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    auth: {
      type: 'exec',
      command: '/usr/local/bin/my-key-broker --provider anthropic',
    },
  },
}

// Or have an external rotator write the credential to a file
// (path optional; defaults to $XDG_STATE_HOME/ccgate/<target>/auth_key.json)
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    auth: {
      type: 'file',
      path: '~/.config/my-broker/anthropic.json',
    },
  },
}
```

The helper / file content is one of:

- **JSON** `{"key":"sk-...","expires_at":"<RFC3339>"}` — for `auth.type=exec`, memoized in `$XDG_CACHE_HOME/ccgate/<target>/` and refreshed early.
- **Plain string** — a single non-empty line, not cached.

Resolution order: `provider.auth` (when configured) > `CCGATE_*_API_KEY` > `*_API_KEY`. When `auth` is configured ccgate **does not silently fall back to env vars on failure** — the hook falls through with `kind=credential_unavailable` instead.

ccgate also registers `std.native('env')(name)` (returns empty string for undefined) and `std.native('must_env')(name)` (raises a config-load error) as Jsonnet helpers, so any string field can pull values from the environment without ccgate-specific syntax.

See [docs/api-key-helper.md](docs/api-key-helper.md) for the full helper contract, runnable examples, account-aware caching via `auth.cache_key`, browser-based first-run auth, the 401/403 behaviour matrix, and the operational recovery checklist.
## Default Rules

ccgate ships built-in default rules per target. They are always applied as the base; your global / project-local configs layer on top.

**Allow:** Read-only operations, local development commands (build / test against project scripts), git feature-branch operations, package-manager installs scoped to the repo.

**Deny:** Download-and-execute (`curl|bash`), direct one-shot remote package execution (`npx`/`pnpx`/`bunx` etc.), git destructive operations on protected branches, out-of-repo deletion, privilege escalation.

Run `ccgate claude init` / `ccgate codex init` to inspect the full embedded defaults. ccgate updates these defaults occasionally for quality reasons; users on the `append_*` style pick those up automatically, while users who replaced the lists wholesale (`allow:` / `deny:`) need to re-check their override against the new defaults:

```bash
ccgate claude init           | less                   # Read the embedded Claude defaults.
ccgate codex  init           | less                   # Same for Codex.
ccgate claude init -p > .claude/ccgate.local.jsonnet  # Project-local skeleton you can extend.
ccgate codex  init -p > .codex/ccgate.local.jsonnet   # Same for Codex.
```

Removing a single embedded rule while keeping the rest is not supported today; replace the whole list with `allow:` / `deny:` and drop the rule from your copy.

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
- **Codex hook schema may change.** Codex hooks live behind upstream's `features.codex_hooks = true` flag and are still evolving. ccgate does not currently expose `permission_mode` from Codex, parse the Codex transcript JSONL, ingest `~/.codex/config.toml`, or apply MCP-server-specific trust hints; classification runs from `tool_name` + `tool_input` + `cwd` only.

## Documentation

- [docs/claude.md](docs/claude.md) — Claude Code specifics
- [docs/codex.md](docs/codex.md) — Codex CLI specifics
- [docs/configuration.md](docs/configuration.md) — config layering, fallthrough_strategy, metrics, known limits
- [docs/api-key-helper.md](docs/api-key-helper.md) — `provider.auth` reference (helper contract, caching, security guidance, 401/403 behaviour, recovery checklist)
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
