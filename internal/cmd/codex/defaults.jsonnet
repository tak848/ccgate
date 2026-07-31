// ccgate defaults for the OpenAI Codex CLI PermissionRequest hook.
//
// These rules are natural-language guidance for the LLM, not deterministic matchers.
// The LLM is the primary judge; allow/deny lists shape its decision boundary.
// Fall through to Codex's own approval prompt when uncertain (set
// fallthrough_strategy=allow|deny in your overrides for fully unattended
// runs -- at your own risk).
//
// Codex hooks fire for Bash, apply_patch, MCP tool calls, and any other
// surface Codex exposes via PermissionRequest. The rules below are
// written tool-agnostically and reference Bash command shapes only as
// concrete examples; the LLM should classify by tool_name + tool_input
// regardless of which surface delivered the request.
//
// To customize, write either:
//   - ~/.codex/ccgate.jsonnet (global), or
//   - <repo>/.codex/ccgate.local.jsonnet (project-local, untracked-only)
// and at either layer use append_* to add entries on top of the
// embedded list, or allow / deny / environment to replace the list
// wholesale. See https://github.com/tak848/ccgate#rule-tuning for examples.

{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/codex.schema.json',

  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    // Alternatives:
    //   name: 'openai',  model: 'gpt-5.6-luna',          (env: OPENAI_API_KEY)
    //   name: 'gemini',  model: 'gemini-3.5-flash-lite', (env: GEMINI_API_KEY)
    // base_url:  route through an OpenAI-/Anthropic-compatible proxy.
    //            See https://github.com/tak848/ccgate/blob/main/docs/providers.md#base_url-and-compatible-proxies
    // timeout_ms: API timeout in ms, default 20000.
    // auth: short-lived / rotating credentials. Discriminated
    //   by auth.type:
    //     auth: { type: 'exec', command: '/usr/local/bin/my-broker --provider anthropic' }
    //     auth: { type: 'file', path: '~/.config/my-broker/anthropic.json' }
    //     auth: { type: 'profile', profile: 'ccgate' }                           // anthropic-only; reads `ant auth login --profile ccgate` credentials, SDK refreshes
    //   The provider block is replaced atomically across config layers,
    //   so a project-local config that restates `provider` must repeat
    //   the auth block. See https://github.com/tak848/ccgate/blob/main/docs/api-key-helper.md
    //   for the full helper contract, examples, and recovery checklist.
  },

  // What to do when the LLM is uncertain (returns "fallthrough"):
  //   'ask'   (default): defer to Codex's permission prompt
  //   'allow': auto-allow uncertain operations (use with care; intended for fully autonomous runs)
  //   'deny':  auto-deny uncertain operations (safer default for unattended automation)
  // Only LLM uncertainty is affected; runtime-mode fallthroughs (no API key, etc.) still defer.
  // fallthrough_strategy: 'ask',

  // disable_load_main_worktree_local_config: true,
  // When ccgate runs inside a linked git worktree (`git worktree add`),
  // it reads <main_worktree>/.codex/ccgate.local.jsonnet (untracked
  // only) before the current worktree's local config. Set this to
  // true to skip the main worktree and read only the current
  // worktree's local config. Evaluated before project-local configs;
  // values written in project-local config are ignored.

  allow: [
    'Read-only operations: Bash inspection commands (ls, cat, head, tail, less, file, stat, find/grep without -exec/--delete, git status/log/diff/show/branch/remote -v), or any tool whose tool_input shape implies pure read (no writes, no network calls with side effects).',
    'Local writes inside the workspace: apply_patch hunks whose target paths are all under cwd / repo_root, edits to project files for editing/refactoring/scaffolding the AI is currently doing. Workspace-internal writes for the active coding task are in scope; writes that escape cwd / repo_root or that match a deny rule are not.',
    'Local build/test against project-defined scripts: make, just, mise run, pnpm test, go test, cargo test, etc.',
    'Package install confined to this repo: pnpm/cargo/go install with no global flags.',
    'Git feature-branch operations on non-protected branches. For switch -c / checkout -b, the target branch is in the command; context.branch_name is the pre-command branch.',
    'MCP tools whose server is explicitly trusted by the user and whose side effects are confined to the user-authorized scope (read APIs, project-scoped writes).',
  ],

  deny: [
    'Download and Execute: curl|bash, wget|sh, eval "$(curl ...)" against remote URLs. deny_message: Pipeline-to-shell of remote content is unsafe; download, review, then run locally instead.',
    'Direct one-shot remote package execution bypassing project scripts: npx / pnpx / bunx with unfamiliar packages. deny_message: Use the project script (mise / just / make) instead.',
    'sudo or other privilege escalation. deny_message: Privilege escalation is not allowed from the hook context.',
    'rm -rf or mv targeting paths outside the workspace, or apply_patch hunks that touch paths outside the workspace. deny_message: Out-of-repo destructive operations are blocked.',
    'Git destructive: push --force(-with-lease), branch -D on protected branches, push --delete, rebase --root on shared branches. deny_message: Destructive git operations require explicit human action.',
    'Unrestricted network out: nc, ssh, scp, ftp to non-allowlisted hosts. deny_message: Network-out tools are blocked from the hook context.',
    'MCP tools that advertise destructive side effects (delete, drop, force-push, send-message, post-comment, etc.) without explicit per-rule allow. deny_message: Destructive MCP tool not allowed without an explicit project-local rule. Ask the user to add one.',
  ],

  environment: [
    'Tool surface: Codex hooks fire for Bash, apply_patch, MCP tool calls, and other tool kinds. Classify by tool_name + tool_input shape rather than assuming a single surface.',
    'Trusted repo: assume the repo is the trust boundary; treat anything outside it (other directories, remote endpoints, MCP servers not explicitly trusted) as untrusted.',
    'Path scope: when a tool_input targets paths outside cwd (e.g. /etc/, /usr/, ~/.ssh/), treat as out-of-repo and lean toward deny unless clearly read-only and benign.',
    'Codex hooks fire only when Codex would otherwise stop and ask the user, which is the prompt the user installed ccgate to skip. Returning fallthrough sends them that prompt anyway, so reserve fallthrough for cases that are genuinely ambiguous (suspect intent, malformed payload, mismatch between description and tool_input, or a tool surface ccgate has no rule for). Default to allow / deny when guidance clearly applies.',
    'Codex HookInput does not carry a recent_transcript field. Decide from tool_name + tool_input + cwd; if intent is ambiguous, return fallthrough (do NOT invent transcript context).',
  ],
}
