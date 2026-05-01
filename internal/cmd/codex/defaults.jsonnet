// ccgate defaults for the OpenAI Codex CLI PermissionRequest hook.
//
// Same shape and philosophy as the Claude Code defaults: the LLM is the
// primary judge; the allow/deny rules below are guidance, not strict
// matchers. Fall through to Codex's own approval prompt when uncertain
// (set fallthrough_strategy=allow|deny in your overrides for fully
// unattended runs -- at your own risk).
//
// Codex hooks fire for Bash, apply_patch, MCP tool calls, and any other
// surface Codex exposes via PermissionRequest. The rules below are
// written tool-agnostically and reference Bash command shapes only as
// concrete examples; the LLM should classify by tool_name + tool_input
// regardless of which surface delivered the request.

{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/codex.schema.json',

  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    // Alternatives:
    //   name: 'openai',  model: 'gpt-4o-mini',        (env: OPENAI_API_KEY)
    //   name: 'gemini',  model: 'gemini-2.0-flash',    (env: GEMINI_API_KEY)
    // base_url: '...' overrides the provider's API endpoint. Point it
    // at an OpenAI-/Anthropic-compatible proxy (LiteLLM proxy, Azure
    // OpenAI, on-prem gateway, regional endpoint, ...) when you don't
    // want ccgate to hit the vendor directly. Required path for
    // OpenAI-compatible endpoints is `.../v1`; Anthropic-compatible
    // endpoints take the host root and the SDK appends `/v1/messages`.
  },

  // What to do when the LLM is uncertain (returns "fallthrough"):
  //   'ask'   (default): defer to Codex's permission prompt
  //   'allow': auto-allow uncertain operations (use with care; intended for fully autonomous runs)
  //   'deny':  auto-deny uncertain operations (safer default for unattended automation)
  // Only LLM uncertainty is affected; runtime-mode fallthroughs (no API key, etc.) still defer.
  // fallthrough_strategy: 'ask',

  allow: [
    'Read-only operations: Bash inspection commands (ls, cat, head, tail, less, file, stat, find/grep without -exec/--delete, git status/log/diff/show/branch/remote -v), or any tool whose tool_input shape implies pure read (no writes, no network calls with side effects).',
    'Local writes inside the workspace: apply_patch hunks whose target paths are all under cwd / repo_root, edits to project files for editing/refactoring/scaffolding the AI is currently doing. Same bar as Claude Code Edit/Write through ccgate.',
    'Local build/test against project-defined scripts: make, just, mise run, pnpm test, go test, cargo test, etc.',
    'Package install confined to this repo: pnpm/cargo/go install with no global flags.',
    'Git feature-branch operations on non-protected branches.',
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
    'You are replacing the upstream Codex approval prompt. Codex hooks fire only when Codex would otherwise stop and ask the user, which is exactly the prompt the user installed ccgate to skip. Returning fallthrough sends them that prompt anyway, so reserve fallthrough for cases that are genuinely ambiguous (suspect intent, malformed payload, mismatch between description and tool_input, or upstream surface ccgate has no rule for). Default to allow / deny instead -- same bar Claude Code parity asks for.',
    'Codex has no recent_transcript field today. Decide from tool_name + tool_input + cwd alone; if intent is ambiguous, return fallthrough (do NOT invent transcript context).',
  ],
}
