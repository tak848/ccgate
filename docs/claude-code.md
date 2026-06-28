# ccgate -- Claude Code

[日本語版 (docs/ja/claude-code.md)](ja/claude-code.md)

Claude-Code-specific notes for the `ccgate claude` hook.

## Hook registration

ccgate plugs into the Claude Code [PermissionRequest hook](https://code.claude.com/docs/en/hooks) event. Add an entry to `~/.claude/settings.json`:

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

`"command": "ccgate"` (no subcommand) is equivalent to `"command": "ccgate claude"`. The explicit form reads better when Claude and Codex hooks live in the same dotfiles.

A wide-open `"matcher": ""` makes ccgate evaluate every PermissionRequest. Restrict by tool kind via the same matcher field if you want a narrower hook (e.g. `"matcher": "Bash|Edit|Write"`).

## Bare `ccgate` (no args + stdin pipe)

`ccgate` with no args reading from stdin is identical to `ccgate claude` and is part of the supported invocation surface.

If you launch `ccgate` from a terminal with no stdin pipe, it prints a usage banner and exits `0`. Only when stdin is a pipe (= the AI tool is feeding it a HookInput JSON) does it run the hook.

## What ccgate sees in the HookInput

Claude Code delivers the standard PermissionRequest payload (see the upstream [hooks reference](https://code.claude.com/docs/en/hooks)). ccgate reads:

- `tool_name`: routes early-return for user-interaction tools (`ExitPlanMode`, `AskUserQuestion`) -- those always fall through to Claude Code's prompt, ccgate never decides for them.
- `tool_input`: forwarded to the LLM as a typed object. The metrics layer captures `command` / `file_path` / `path` / `pattern` only.
- `tool_input_raw`: the original `tool_input` JSON, forwarded verbatim. Use this to inspect fields that the typed view omits (e.g. nested MCP arguments).
- `referenced_paths`: paths extracted from `tool_input` on a best-effort basis. Supported tools: `Read`, `Write`, `Edit`, `MultiEdit`, `Glob`, `Grep`, `Bash`. Other tools (MCP, user-interaction tools) produce an empty list -- the LLM still sees the raw payload via `tool_input_raw`.
- `permission_mode`: switches the system prompt to plan-mode rules when `"plan"`. `"bypassPermissions"` and `"dontAsk"` short-circuit ccgate to fallthrough.
- `cwd`: feeds the git context builder (`gitutil.RepoRoot`, branch, worktree). Working-tree dirty/clean state is not delivered.
- `transcript_path`: the recent-transcript loader reads up to N tail entries to give the LLM user-intent context.
- `permission_suggestions`: forwarded to the LLM as background.
- `settings_permissions`: ccgate reads `~/.claude/settings.json` separately and surfaces the user's own static allow / deny patterns to the LLM as a hint, not as a whitelist requirement (see "Why settings.json patterns are a hint" below).

## `--add-dir` / `additionalDirectories` and ccgate's trust boundary

Claude Code's `--add-dir` flag and `permissions.additionalDirectories` setting expand what Claude Code can access. They do not make ccgate treat those directories as trusted automatically: the PermissionRequest payload does not include the active add-dir list, so ccgate only sees `cwd`, git context, and the paths present in the current `tool_input`.

If an extra directory matters to the policy decision, describe it in `append_environment`. Otherwise a repo-external path may still look like boundary-crossing access to the default rules and can be denied or fall through for confirmation.

Read-only reference directory:

```jsonnet
{
  append_environment: [
    'Additional Claude directory /path/to/shared-lib is read-only reference material. Reads are expected; writes, build/install commands, and deletion there are outside the trusted repo.',
  ],
}
```

Same trusted workspace:

```jsonnet
{
  append_environment: [
    'Additional Claude directory /path/to/shared-lib is part of the same trusted workspace as this repo. Treat reads and local development commands there as expected, unless a deny rule otherwise matches.',
  ],
}
```

## Plan mode

`permission_mode == "plan"` switches the system prompt's decision rules to:

- `allow`: side-effect-free operations, OR edits to the plan file Claude designated. For compound shell commands (`|`, `&&`, `||`, `;`), every subcommand must independently satisfy that bar.
- `deny`: any side effect on project / production / shared state.
- `fallthrough`: side-effect status genuinely ambiguous.

Allow guidance does NOT promote write operations to allow in plan mode. The deny guidance still applies and can override read-only operations too.

This is purely prompt-driven, so there is no hard guarantee.

## How `recent_transcript` is used

`recent_transcript` carries the most recent user messages and tool calls from the transcript JSONL. The system prompt tells the LLM:

- If the user explicitly requested the operation in the recent transcript, prefer `allow` or `fallthrough` over `deny`.
- An explicit user request can only escalate `deny` to `fallthrough`, never to `allow`. Deny guidance still wins.

This is the only signal in the prompt that lets the LLM say "the deny rule matches but the user clearly asked for this, so let the user confirm via Claude Code's prompt instead of refusing outright".

## Why `settings.json` patterns are a hint, not a whitelist

`settings_permissions` is what's in your `~/.claude/settings.json` `permissions.allow / deny`. Claude Code matches those static patterns **before** invoking the PermissionRequest hook -- so by design every request that reaches ccgate did **not** auto-match an allow pattern. Common reasons:

- Composite constructs like `$(...)` substitutions or pipelines slip past literal matchers.
- MCP tools without a static matcher.
- The user only listed allow patterns for the simplest invocations and let everything else flow to the hook.

Treating `settings_permissions.allow` as a whitelist requirement therefore breaks the hook's normal operation. ccgate uses it as a hint about user preferences only -- a request can still be `allow`-ed by the LLM even when it does not appear in `settings_permissions.allow`.

## Claude-specific HookInput / state reference

| Aspect                      | Value |
|-----------------------------|-------|
| Tool surface                | `Bash`, `Read`, `Write`, `Edit`, `MultiEdit`, `Glob`, `Grep`, MCP, user-interaction tools (`ExitPlanMode`, `AskUserQuestion`) |
| `permission_mode` values    | `default` / `acceptEdits` / `plan` / `bypassPermissions` / `dontAsk`. `plan` switches the system prompt; `bypassPermissions` / `dontAsk` short-circuit ccgate to fallthrough. |
| `recent_transcript`         | Loaded from `transcript_path` and forwarded to the LLM as user-intent context (see "How `recent_transcript` is used" above). |
| `settings_permissions`      | Forwarded as a hint -- see "Why `settings.json` patterns are a hint" above. |
| `permission_suggestions`    | Forwarded verbatim. |
| State path                  | `$XDG_STATE_HOME/ccgate/claude/` (falls back to `~/.local/state/ccgate/claude/` when unset). |
| Project-local config        | `{repo_root}/.claude/ccgate.local.jsonnet` (untracked-only). |

## Limitations

- **Plan mode is prompt-only.** Under `permission_mode == "plan"`, ccgate relies on the LLM plus prose in the system prompt to (a) reject implementation-side writes and (b) allow read-only queries without requiring an allow-guidance match. Either side can misfire.
- **No surgical reset for a single embedded default rule.** A layer either replaces a list wholesale (`allow: [...]`) or appends to it (`append_allow: [...]`); removing one specific embedded entry while keeping the rest requires re-stating the whole list under `allow` / `deny` minus that one entry.
- **No deterministic short-circuit on `settings.json` deny patterns.** ccgate routes every Claude Code PermissionRequest through the LLM; literal `settings.json` deny matches do not exit ccgate early.
