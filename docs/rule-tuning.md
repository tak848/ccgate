# ccgate -- Rule tuning

[日本語版 (docs/ja/rule-tuning.md)](ja/rule-tuning.md)

Once provider setup and hook registration are done, the next thing to learn is how to write your own `allow` / `deny` / `append_*` guidance. This page walks through that flow end to end so you do not have to cross-reference other files mid-task.

## 1. Inspect the embedded defaults

```bash
ccgate claude init | less                                # Read the embedded Claude defaults.
ccgate codex  init | less                                # Same for Codex.
ccgate claude init -p > .claude/ccgate.local.jsonnet     # Project-local skeleton you can extend.
ccgate codex  init -p > .codex/ccgate.local.jsonnet      # Same for Codex.
```

The `-p` skeleton ships with commented-out provider and `fallthrough_strategy` examples, so it is easier to start from than an empty file.

## 2. Where to put the file

- Global: `~/.claude/ccgate.jsonnet` or `~/.codex/ccgate.jsonnet`.
- Project-local: `<repo>/.claude/ccgate.local.jsonnet` or `<repo>/.codex/ccgate.local.jsonnet`, untracked-only.

For layer composition see [docs/configuration.md](configuration.md#where-ccgate-looks-for-config). At a glance: embedded defaults → global → main-worktree project-local → current-worktree project-local, with later layers stacked on top.

## 3. Replace vs append (i.e. copy-paste or not)

| Field shape | Relationship to embedded defaults |
|-------------|-----------------------------------|
| `append_allow` / `append_deny` / `append_environment` | Embedded defaults from the running binary are kept; your entries are appended. |
| `allow:` / `deny:` / `environment:` | Embedded defaults from the running binary are dropped; only your list is in effect. |

If you replace wholesale, use `ccgate <target> init` as the current embedded-list reference and reconcile manually.

There is no surgical "remove one specific embedded rule" path. `append_*` only adds; to omit one embedded entry, restate the whole list under `allow:` / `deny:` minus that one entry.

## 4. Writing rules

A rule is one line of natural-language guidance describing the operation. Add a `deny_message:` at the end and that string is shown to the AI when the rule fires.

The LLM does the actual classification, so write at a granularity the LLM can judge from `tool_input` / `tool_input_raw` / `branch_name` / paths / command strings. Information that ccgate never delivers to the LLM (e.g. the working-tree dirty/clean flag) cannot be used in guidance. The representative input list is in the README's [Concepts](../README.md#concepts) section.

Three patterns, by which field you write the rule in:

### Add to allow (`append_allow`)

Claude:

```jsonnet
{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/claude.schema.json',
  append_allow: [
    // The LLM can read target_path from tool_input.file_path / referenced_paths.
    'Edit / Write / MultiEdit targeting a Markdown file under repo_root/docs/ is fine; the content review happens elsewhere.',
  ],
}
```

Codex (the LLM reads `apply_patch` hunk targets from `tool_input_raw`):

```jsonnet
{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/codex.schema.json',
  append_allow: [
    'apply_patch whose hunks all target *.md files under repo_root/docs/ is fine; the content review happens elsewhere.',
  ],
}
```

### Add to deny (`append_deny`)

```jsonnet
{
  append_deny: [
    'Production database access: any psql / mysql connection to a *.prod.* host. deny_message: production access is gated behind the runbook.',
    'Setting production environment variables in the running session. deny_message: configure production via the deployment system, not via shell exports.',
  ],
}
```

### Replace wholesale (`allow:` / `deny:`)

```jsonnet
{
  // You own the full lists; the embedded defaults are not in effect.
  allow: [
    'Read-only filesystem inspection inside the repository.',
    'Local development commands using project scripts (build, test, lint).',
  ],
  deny: [
    'Downloading and executing remote code (curl | bash, eval $(curl ...), etc.). deny_message: vet the script first; install it via a package manager or a checked-in script.',
  ],
}
```

The `$schema` line enables editor autocompletion for either shape.

### Embedding env-derived values

When a rule string needs to embed a host name, an account ID, or any other environment-derived value, use the jsonnet native helpers:

- `std.native('env')(name)` — returns `""` when unset.
- `std.native('must_env')(name)` — raises a config-load error when unset.

These run **once per hook invocation, at config-load time**, before ccgate sees `tool_input`. There is no runtime branch-on-`tool_input` mechanism — the LLM does the runtime classification.

## 5. Iteration workflow

After a day or two of real use, run `ccgate <target> metrics --details N`. The "Top fallthrough commands" / "Top deny commands" drill-downs surface operations a rule could handle. Add one `append_deny` (or `append_allow`) entry, ship it, and re-check the metrics next round.

Metrics column meanings, the JSON output schema, and the credential-failure aggregation live in [docs/configuration.md#metrics-output](configuration.md#metrics-output).

## See also

- [docs/configuration.md](configuration.md) — Layer / merge rules / full field reference / metrics / fallthrough_strategy details
- [docs/api-key-helper.md](api-key-helper.md) — `provider.auth` (refreshable credentials, helper contract, 401/403 behaviour, recovery checklist)
- [docs/claude.md](claude.md) — Claude Code-specific HookInput fields
- [docs/codex.md](codex.md) — Codex CLI-specific HookInput fields
