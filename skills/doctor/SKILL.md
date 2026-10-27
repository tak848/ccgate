---
name: doctor
description: Read-only audit of the ccgate environment (binary, version, hook, config layers, provider).
when_to_use: |
  - User wants to verify their ccgate setup end-to-end.
  - "Is ccgate even running?" / "Which config layers are being read?"
  - Checking whether the installed ccgate version is current.
  - Diagnosing worktree config layering confusion (which `.local.jsonnet` is loaded).
  - Verifying Codex hook feature flag and project trust state.
---

# ccgate environment doctor

Read-only audit of the user's ccgate environment. Report findings as a table. **Do not edit any file.** When something needs to be fixed, name the file and the change, and hand the user to `/ccgate:setup` or `/ccgate:tune` for the actual write.

## Checks (run them all, then summarise)

### 1. Binary

- Run `ccgate --version`. Record: not installed / on `PATH` / requires `mise exec` / absolute path.
- If `ccgate --version` fails entirely, the rest of the checks still run on filesystems but skip anything that calls the binary.

### 2. Version freshness

Compare installed version against the latest release.

Try in this order:

1. `WebFetch https://api.github.com/repos/tak848/ccgate/releases/latest` and extract `tag_name`.
2. On 403 or other failure (unauthenticated GitHub API is rate-limited to 60 req/hour/IP), `git ls-remote --tags --refs https://github.com/tak848/ccgate | tail -n 30` and pick the highest semver tag.
3. If both fail, report "latest version unverified" and continue.

Normalisation rules before comparing:

- Strip a leading `v` from both sides (git tags use `v0.9.1`, `plugin.json` and `ccgate --version` may not).
- Treat `dev` / `(devel)` / anything non-semver from `ccgate --version` as "development build" — do not classify as outdated, just report the literal version.
- A trailing pre-release or build suffix on either side disables the "outdated" verdict; report both versions verbatim.

### 3. Hook registration

#### Claude Code

Read `~/.claude/settings.json` and check `hooks.PermissionRequest`. Confirm at least one entry has a `command` that resolves to ccgate (`ccgate`, `ccgate claude`, an absolute path, or a `mise exec` wrapper).

If no entry exists, report "ccgate is not registered as a Claude Code PermissionRequest hook" and point at `/ccgate:setup`.

#### Codex CLI

Two pieces both have to be in place:

1. `[features] codex_hooks = true` in `~/.codex/config.toml`. Without this flag, Codex ignores the hook.
2. A `hooks.PermissionRequest` entry in either `~/.codex/hooks.json` or `~/.codex/config.toml` that resolves to ccgate (`ccgate codex` or equivalent).

Project trust: ccgate inherits Codex's project trust state. If the current project is not trusted, hooks may not fire on operations originating from it. Note this in the report when relevant. Reference: `${CLAUDE_PLUGIN_ROOT}/docs/codex-cli.md` ([codex-cli](../../docs/codex-cli.md)).

### 4. Config layer candidates

For each `<target>` the user uses, walk every candidate and report:

| Path | Loaded as | Exists | Tracked by git | Effective? |
|---|---|---|---|---|

Paths:

- `~/.<target>/ccgate.jsonnet` — global (layer 2)
- `{main_worktree}/.<target>/ccgate.local.jsonnet` — main-worktree project-local (layer 3, **linked-worktree-only**, **untracked-only**)
- `{repo_root}/.<target>/ccgate.local.jsonnet` — current-worktree project-local (layer 4, **untracked-only**)

How to fill the table:

- `Exists` — `stat` the path.
- `Tracked by git` — `git ls-files --error-unmatch <path>` from the appropriate repo root. Tracked files are intentionally ignored by ccgate.
- `Effective` — `Exists && !Tracked` for layer 3 / 4. For layer 3, also confirm the current cwd is a linked worktree (not the main one) and `disable_load_main_worktree_local_config: true` is **not** set in layer 1 or 2.

> [!NOTE]
> `ccgate <target> init` prints the **embedded defaults** only; it is not a merged-config inspector. There is no read-only API for "the config that would actually be evaluated at runtime", so doctor reports candidates and effective layers, not the merged result.

Reference: `${CLAUDE_PLUGIN_ROOT}/docs/configuration.md` "Where ccgate looks for config" ([configuration#where-ccgate-looks-for-config](../../docs/configuration.md#where-ccgate-looks-for-config)).

### 5. Provider sanity (per layer)

For each effective layer file from §4, `Read` it (do **not** evaluate the jsonnet — this is a syntactic hint, not a merged inspect). Look for a `provider` block; if present, sanity-check:

- `name` is one of `anthropic` / `openai` / `gemini` (anything else falls through with `ft_kind=unknown_provider` at runtime).
- `model` is non-empty.
- `base_url`, if set, looks like a URL.
- `auth`, if set, has `type` in `exec` / `file` / `profile` and the matching required fields.

> [!IMPORTANT]
> The `provider` block is replaced **atomically** across layers — a higher layer that sets `provider` drops every field from lower layers. When doctor sees a `provider` block that lacks fields the user expects (e.g. `auth` is missing on a layer that overrides a global helper), call it out.

Reference: `${CLAUDE_PLUGIN_ROOT}/docs/configuration.md` "How layers compose" ([configuration](../../docs/configuration.md)), `${CLAUDE_PLUGIN_ROOT}/docs/api-key-helper.md` ([api-key-helper](../../docs/api-key-helper.md)).

### 6. Metrics / log presence

`ccgate <target> metrics --json` resolves the configured `metrics_path` (which may be customised away from the default). Use it to find out whether metrics are being written.

Default paths (only consulted when `ccgate metrics` is unavailable or fails):

- Logs: `$XDG_STATE_HOME/ccgate/<target>/ccgate.log` (or `~/.local/state/ccgate/<target>/`)
- Metrics: `$XDG_STATE_HOME/ccgate/<target>/metrics.jsonl`

Report: file exists / last modified / empty or not.

### 7. Embedded defaults sanity

Run `ccgate <target> init | head -5`. If output is empty or errors, the binary is broken or the wrong target was passed. (This does **not** validate the user's config; it only checks the embedded defaults are intact.)

## Report shape

Present the report as one summary table plus a follow-up list:

- Each row: one check, one of `OK` / `WARN` / `FAIL` / `INFO`, one-sentence note.
- Below the table: list any concrete next steps with the target skill (e.g. "no Claude hook registered → run `/ccgate:setup`", "OpenAI provider block has no `model` → run `/ccgate:tune` and add the model field").

## What this skill does not do

- It does not edit any file. Every fix is described, not applied.
- It does not draft `append_allow` / `append_deny` rules — that is `/ccgate:tune`.
- It does not explain individual decisions — that is `/ccgate:debug`.
- It does not register the hook from scratch — that is `/ccgate:setup`.
