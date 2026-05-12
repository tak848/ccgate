# ccgate

[![CI](https://github.com/tak848/ccgate/actions/workflows/ci.yml/badge.svg)](https://github.com/tak848/ccgate/actions/workflows/ci.yml)
[![release](https://github.com/tak848/ccgate/actions/workflows/release.yml/badge.svg)](https://github.com/tak848/ccgate/releases)

A **PermissionRequest** hook for AI coding tools. It delegates each tool-execution permission decision to an LLM (Claude Haiku) using rules written in a jsonnet configuration file. Rules are **natural-language guidance read by the LLM** — not deterministic jsonnet policy code — so the typical workflow is to describe what should be allowed or denied in prose and let the LLM classify the actual request.

ccgate ships with built-in default rules, so it works out of the box without any configuration.

![ccgate in action: a safe `echo` is allowed while `curl ... | bash` is denied with a deny_message](docs/images/gate.png)

Supported targets:

- **[Claude Code](https://docs.anthropic.com/en/docs/claude-code)**
- **[OpenAI Codex CLI](https://developers.openai.com/codex/hooks)**

[日本語ドキュメント](docs/ja/README.md)

## Install

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

### Homebrew

```bash
brew install tak848/tap/ccgate
```

### go install

```bash
go install github.com/tak848/ccgate@latest
```

### GitHub Releases

Download a binary from [Releases](https://github.com/tak848/ccgate/releases) and place it on your `PATH`.

## Quick start — Claude Code

### 1. Register as a Claude Code hook

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

### 2. API key

Set the API key for your chosen provider. `CCGATE_*_API_KEY` is preferred and overrides the bare variable, so you can keep ccgate's key separate from the AI tool's own key.

| `provider.name` | Preferred                  | Fallback             | Get API key |
|-----------------|----------------------------|----------------------|-------------|
| `anthropic`     | `CCGATE_ANTHROPIC_API_KEY` | `ANTHROPIC_API_KEY`  | <https://platform.claude.com/settings/keys> |
| `openai`        | `CCGATE_OPENAI_API_KEY`    | `OPENAI_API_KEY`     | <https://platform.openai.com/api-keys>      |
| `gemini`        | `CCGATE_GEMINI_API_KEY`    | `GEMINI_API_KEY`     | <https://aistudio.google.com/app/api-keys>  |

That's it — ccgate is now running with its embedded defaults. To customize what is allowed or denied, see [Rule tuning](#rule-tuning); for background on how rules work, see [Concepts](#concepts).

## Quick start — Codex CLI

> [!NOTE]
> Codex hooks live behind the `[features] codex_hooks = true` flag upstream. Treat the [Codex hooks docs](https://developers.openai.com/codex/hooks) as the source of truth before relying on a specific field.

### 1. Register as a Codex hook

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
codex_hooks = true   # Codex hooks live behind this feature flag; keep it set for compatibility.

[[hooks.PermissionRequest]]
matcher = ""

[[hooks.PermissionRequest.hooks]]
type    = "command"
command = "ccgate codex"
statusMessage = "ccgate evaluating request"
```

See [docs/codex.md](docs/codex.md) for the full lookup order, project-local overlays, and a `go run` recipe for in-tree dev builds. Refer to the upstream [Codex hooks documentation](https://developers.openai.com/codex/hooks) for the authoritative schema.

### 2. API key

Same env vars as Claude Code — see the [provider table](#2-api-key).

That's it — ccgate is now running with its embedded defaults. To customize what is allowed or denied, see [Rule tuning](#rule-tuning); for background on how rules work, see [Concepts](#concepts).

## Concepts

ccgate's `allow` / `deny` / `environment` lists are **strings of natural-language guidance** that get embedded into a system prompt and sent to the LLM. They are not patterns matched by a deterministic engine — every PermissionRequest goes through the LLM, and the LLM classifies it as `allow`, `deny`, or `fallthrough` based on the rules plus the request context.

Evaluation flow:

```
AI tool issues a PermissionRequest
  │
  │  stdin: HookInput JSON
  ▼
ccgate
  ├── Load jsonnet config (embedded defaults + your global + project-local)
  ├── Build context (git repo info, referenced paths, recent transcript [Claude only])
  ├── Call the configured LLM (default: Claude Haiku) with structured output
  └── stdout: allow / deny / fallthrough
```

What ccgate puts in front of the LLM (representative fields):

- `tool_name`, `tool_input`, and `tool_input_raw` (the original JSON payload, passed through verbatim).
- `cwd`, `repo_root`, `branch_name`, and worktree info from `gitutil.Context`. The working-tree dirty/clean state is **not** delivered.
- `referenced_paths` — paths extracted from `tool_input` on a best-effort basis. Supported tools: `Read`, `Write`, `Edit`, `MultiEdit`, `Glob`, `Grep`, `Bash`. For `apply_patch` (Codex) and MCP tools, `referenced_paths` is empty; the LLM reads `tool_input_raw` directly to see hunk targets or call arguments.
- Claude-only: `permission_mode` (switches the prompt to plan-mode rules when `"plan"`), `permission_suggestions`, `recent_transcript`, and `settings_permissions` (treated as a hint, not a whitelist).

For the complete input list per target, see [docs/claude.md](docs/claude.md) and [docs/codex.md](docs/codex.md).

## Configuration

### Config file loading order (per target)

| Order | Claude Code | Codex CLI |
|------:|-------------|-----------|
| 1     | Embedded defaults (always applied as the base) | Embedded defaults |
| 2     | `~/.claude/ccgate.jsonnet` — global (layered on top) | `~/.codex/ccgate.jsonnet` — global |
| 3     | `{main_worktree}/.claude/ccgate.local.jsonnet` — main-worktree project-local (untracked only; only when running in a linked git worktree) | `{main_worktree}/.codex/ccgate.local.jsonnet` |
| 4     | `{repo_root}/.claude/ccgate.local.jsonnet` — current-worktree project-local (untracked only) | `{repo_root}/.codex/ccgate.local.jsonnet` |

All layers compose with the same rules:

- **Lists** — `allow` / `deny` / `environment` **replace** the value carried over from earlier layers when the layer sets them (even to `[]`). The `append_*` siblings (`append_allow`, `append_deny`, `append_environment`) **add** entries on top of whatever the earlier layers produced.
- **Scalars** — `log_*`, `metrics_*`, `fallthrough_strategy` are overwritten per-field when the layer sets them, otherwise the earlier value survives.
- **`provider` block** — a layer that writes `provider` **replaces the entire block** (`name` + `model` + `base_url` + `auth` + `timeout_ms`). Layers that omit `provider` inherit the earlier block unchanged. The block is replaced as a unit because the fields are tightly coupled. Important: when a project-local config restates `provider`, it must repeat any `auth` block from the global layer too — otherwise the helper config is silently dropped on that project.

Project-local configs are loaded only when **not tracked by Git**.

Set `disable_load_main_worktree_local_config: true` in layer (1) or (2) to skip layer (3). The flag is honoured only at those two layers — written into (3) or (4) it is ignored. See [docs/configuration.md](docs/configuration.md#where-ccgate-looks-for-config).

### Config fields

| Field                    | Type                              | Default                                                                       | Description                                                                                            |
|--------------------------|-----------------------------------|-------------------------------------------------------------------------------|--------------------------------------------------------------------------------------------------------|
| `provider.name`          | string                            | `"anthropic"`                                                                 | Provider name. One of `"anthropic"`, `"openai"`, `"gemini"`.                                            |
| `provider.model`         | string                            | `"claude-haiku-4-5"`                                                          | Model name. Examples: `claude-haiku-4-5` / `claude-sonnet-4-6` (anthropic), `gpt-5.4-nano-2026-03-17` (openai), `gemini-3-flash-preview` (gemini). When routing through a compatible proxy, use whatever model name the proxy exposes (e.g. `anthropic/claude-haiku-4-5`). |
| `provider.base_url`      | string                            | `""`                                                                          | Override the provider's API base URL. Empty = use the SDK default. Use this to route through an OpenAI- / Anthropic-compatible proxy. |
| `provider.auth`          | object (`{type, ...}`)            | (omit = env var)                                                              | Discriminated union for short-lived / rotating credentials. `type=exec` / `type=file` / `type=profile`. See [docs/api-key-helper.md](docs/api-key-helper.md) for full reference. |
| `provider.timeout_ms`    | int                               | `20000`                                                                       | API timeout (ms). `0` = no timeout.                                                                    |
| `log_path`               | string                            | `$XDG_STATE_HOME/ccgate/<target>/ccgate.log`                                  | Log file path. Supports `~` for home directory.                                                        |
| `log_disabled`           | bool                              | `false`                                                                       | Disable logging entirely                                                                               |
| `log_max_size`           | int                               | `5242880`                                                                     | Max log file size in bytes before rotation (default 5MB). `0` = no rotation.                           |
| `metrics_path`           | string                            | `$XDG_STATE_HOME/ccgate/<target>/metrics.jsonl`                               | Metrics JSONL file path.                                                                               |
| `metrics_disabled`       | bool                              | `false`                                                                       | Disable metrics collection entirely                                                                    |
| `metrics_max_size`       | int                               | `2097152`                                                                     | Max metrics file size in bytes before rotation (default 2MB). `0` = no rotation.                       |
| `fallthrough_strategy`   | `"ask"` / `"allow"` / `"deny"`    | `"ask"`                                                                       | How to resolve LLM uncertainty (`fallthrough`). See [Fallthrough strategy](#fallthrough-strategy). |
| `disable_load_main_worktree_local_config` | bool | `false`                                                                       | In a linked git worktree, skip the main worktree's `ccgate.local.jsonnet`. See [docs/configuration.md](docs/configuration.md#where-ccgate-looks-for-config). |
| `allow`                  | string[]                          | `[]`                                                                          | Allow guidance rules. **Replaces** the value carried over from earlier layers when set.                |
| `deny`                   | string[]                          | `[]`                                                                          | Deny guidance rules (mandatory). Supports inline `deny_message:` hints. Same replace semantics as `allow`. |
| `environment`            | string[]                          | `[]`                                                                          | Context strings passed to the LLM (trust level, policies, etc.). Same replace semantics as `allow`.    |
| `append_allow`           | string[]                          | `[]`                                                                          | Allow guidance rules **appended** on top of the carried-over list. Use this in project-local configs.   |
| `append_deny`            | string[]                          | `[]`                                                                          | Deny guidance rules appended on top of the carried-over list.                                          |
| `append_environment`     | string[]                          | `[]`                                                                          | Environment context appended on top of the carried-over list.                                          |

`<target>` is `claude` or `codex` depending on which hook is invoked. When `XDG_STATE_HOME` is unset, ccgate falls back to `~/.local/state/ccgate/<target>/...`.

## Rule tuning

ccgate already runs safely on its embedded defaults. This section covers how to layer your own `allow` / `deny` / `environment` guidance on top.

### What you can change

- `allow` / `deny` / `environment` (string lists) — **replace** the inherited list when set.
- `append_allow` / `append_deny` / `append_environment` — **append** entries on top of the inherited list. Embedded-default updates ccgate ships in a release flow in automatically when you upgrade.

### Where to put it

- Global: `~/.claude/ccgate.jsonnet` or `~/.codex/ccgate.jsonnet`.
- Project-local: `<repo>/.claude/ccgate.local.jsonnet` or `<repo>/.codex/ccgate.local.jsonnet`, untracked-only.

See [Config file loading order](#config-file-loading-order-per-target) above for how the layers compose.

### Inspecting the embedded defaults

```bash
ccgate claude init           | less                   # Read the embedded Claude defaults.
ccgate codex  init           | less                   # Same for Codex.
ccgate claude init -p > .claude/ccgate.local.jsonnet  # Project-local skeleton you can extend.
ccgate codex  init -p > .codex/ccgate.local.jsonnet   # Same for Codex.
```

### Replace vs append

`append_*` keeps the inherited list and adds to it — embedded-default updates ccgate ships in a release land automatically when you upgrade. `allow:` / `deny:` replaces the inherited list wholesale; you take ownership of keeping your override in line with new embedded defaults, so re-check against `ccgate <target> init` output on each release.

### Writing rules

A rule is one line of natural-language guidance describing the operation, optionally with a `deny_message:` hint at the end (the deny message is shown to the AI when the rule fires). The LLM decides whether the actual request matches. Write at a granularity the LLM can judge from `tool_input` / `tool_input_raw` / `branch_name` / paths / command strings. Information not delivered to the LLM (e.g. the working-tree dirty/clean flag) cannot be used in guidance.

Three example patterns, by which field you write the rule in:

**Add to allow** (`append_allow`):

```jsonnet
{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/claude.schema.json',
  append_allow: [
    // Claude: target_path is in tool_input.file_path / referenced_paths
    'Edit / Write / MultiEdit targeting a Markdown file under repo_root/docs/ is fine; the content review happens elsewhere.',
  ],
}
```

For Codex, write rules against `apply_patch` hunks (visible via `tool_input_raw`):

```jsonnet
{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/codex.schema.json',
  append_allow: [
    'apply_patch whose hunks all target *.md files under repo_root/docs/ is fine; the content review happens elsewhere.',
  ],
}
```

**Add to deny** (`append_deny`):

```jsonnet
{
  append_deny: [
    'Production database access: any psql / mysql connection to a *.prod.* host. deny_message: production access is gated behind the runbook.',
    'Setting production environment variables in the running session. deny_message: configure production via the deployment system, not via shell exports.',
  ],
}
```

**Replace wholesale** (`allow:` / `deny:`):

```jsonnet
{
  // You take ownership of the full lists; new embedded defaults will not flow in automatically.
  allow: [
    'Read-only filesystem inspection inside the repository.',
    'Local development commands using project scripts (build, test, lint).',
  ],
  deny: [
    'Downloading and executing remote code (curl | bash, eval $(curl ...), etc.). deny_message: vet the script first; install it via a package manager or a checked-in script.',
  ],
}
```

The `$schema` line enables editor autocompletion either way.

ccgate registers `std.native('env')(name)` (empty string when undefined) and `std.native('must_env')(name)` (raises a config-load error) as jsonnet helpers. They let any string field pull values from the process environment without ccgate-specific syntax — useful for embedding host names or account IDs into a rule string.

### Iteration workflow

Run `ccgate <target> metrics --details N` after a day or two of real use. The "Top fallthrough commands" / "Top deny commands" drill-downs surface operations a rule could handle. Add one `append_deny` (or `append_allow`) entry, ship it, and re-check the metrics next round.

## Provider と credential

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

Then export the matching API key (`CCGATE_OPENAI_API_KEY` / `CCGATE_GEMINI_API_KEY` — see the [provider table](#2-api-key)). If the key is missing, ccgate falls through to the upstream tool's permission prompt, so flipping providers cannot break the hook.

> [!WARNING]
> Avoid reasoning models (`gpt-5`, `gpt-5-mini`, `gpt-5-nano`, `gpt-5-chat`, `o1*`, `o3*`, `o4-mini`). They reject `temperature=0` (every request fails) and add seconds of chain-of-thought that ccgate's classification doesn't need. Use `gpt-4.1-nano`, `gpt-4o-mini`, or `gpt-5.4-nano-2026-03-17`.

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

### Refreshable credentials

When the credential rotates faster than a static env var can keep up (AWS STS, Vertex ADC, OpenAI-compatible gateways with virtual keys, internal key brokers), use `provider.auth`. It's a discriminated union over three shapes:

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

// Or read credentials from an `ant auth login` profile (Anthropic only;
// the SDK refreshes the access token on its own)
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    auth: {
      type: 'profile',
      profile: 'ccgate',       // matches `ant auth login --profile ccgate`
    },
  },
}
```

The helper / file content is one of:

- **JSON** `{"key":"sk-...","expires_at":"<RFC3339>"}` — for `auth.type=exec`, memoized in `$XDG_CACHE_HOME/ccgate/<target>/` and refreshed early.
- **Plain string** — a single non-empty line, not cached.

`auth.type=profile` is different: ccgate hands the loaded profile to anthropic-sdk-go via `option.WithConfig`, and the SDK's refresh-token loop owns the credential lifecycle. ccgate also calls `option.WithoutEnvironmentDefaults` so a leftover `ANTHROPIC_API_KEY` cannot silently shadow your declared profile.

Resolution order: `provider.auth` (when configured) > `CCGATE_*_API_KEY` > `*_API_KEY`. When `auth` is configured ccgate **does not silently fall back to env vars on failure** — the hook falls through with `kind=credential_unavailable` instead.

See [docs/api-key-helper.md](docs/api-key-helper.md) for the full helper contract, runnable examples, account-aware caching via `auth.cache_key`, browser-based first-run auth, the 401/403 behaviour matrix, and the operational recovery checklist.

## Fallthrough strategy

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
- `allow` — auto-approve uncertain operations. **Riskier**: you are letting ccgate green-light operations the LLM itself was unsure about. Both Claude Code and Codex only deliver `decision.message` on `deny`, so the AI never sees a warning on forced-allow.

Only LLM-driven uncertainty is affected. Truncated/refused API responses, missing API keys, `bypassPermissions`/`dontAsk` mode (Claude only), and `ExitPlanMode` / `AskUserQuestion` (Claude only) continue to defer to the upstream tool regardless.

`ccgate <target> metrics` surfaces how often the override fired through the `F.Allow` / `F.Deny` columns in the daily table (and `forced_allow` / `forced_deny` in JSON output), so you can audit whether the strategy you chose is making decisions you are comfortable with.

## Logging and metrics

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
ccgate codex  metrics --days 7        # codex side
```

The daily table shows per-day counts (Allow, Deny, Fall, F.Allow, F.Deny, Err), automation rate, average latency, and token usage. The "Top fallthrough commands" / "Top deny commands" drill-downs surface which operations you could eliminate by adding a rule.

## Known limitations

- **Plan mode correctness is prompt-only (Claude only).** Under `permission_mode == "plan"`, ccgate relies on the LLM plus prose in the system prompt to (a) reject implementation-side writes and (b) allow read-only queries without requiring an allow-guidance match. Either side can misfire. Tracked in [#37](https://github.com/tak848/ccgate/issues/37).
- **No surgical reset for a single embedded default rule.** A layer can either **replace** a list wholesale (`allow: [...]`) or **append** to it (`append_allow: [...]`). Removing one specific embedded `allow` / `deny` rule while keeping the rest of the embedded list requires re-stating the whole list under `allow:` / `deny:` minus that one entry.
- **No runtime conditional logic in jsonnet.** jsonnet evaluation happens once per hook invocation, at config load time, **before ccgate sees `tool_input`**. So rules cannot branch on `tool_input` / git working-tree state / external command output. Runtime classification is the LLM's job — write guidance in prose and let the LLM judge from the request context. Config-time env reads via `std.native('env')(name)` / `std.native('must_env')(name)` are available for things like embedding a host name into a rule string.

## Documentation

- [docs/claude.md](docs/claude.md) — Claude Code specifics, full HookInput field reference
- [docs/codex.md](docs/codex.md) — Codex CLI specifics, full HookInput field reference
- [docs/configuration.md](docs/configuration.md) — config layering, fallthrough_strategy, metrics, known limits
- [docs/api-key-helper.md](docs/api-key-helper.md) — `provider.auth` reference (helper contract, caching, 401/403 behaviour, recovery checklist)
- [日本語ドキュメント (docs/ja/)](docs/ja/README.md)

## CLI reference

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

Top-level `ccgate init` and `ccgate metrics` are not real subcommands — they print a one-line pointer to the per-target form and exit `2`. The bare `ccgate` hook invocation is a different code path and works as documented above.

## Development

```bash
mise run build    # Build binary
mise run test     # Run tests
mise run vet      # Run go vet
mise run schema   # Regenerate schemas/{claude,codex}.schema.json
```

## License

MIT
