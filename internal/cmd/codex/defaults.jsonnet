// ccgate defaults for the OpenAI Codex CLI PermissionRequest hook.
//
// Same shape and philosophy as the Claude Code defaults: the LLM is the
// primary judge; the allow/deny rules below are guidance, not strict
// matchers. Fall through to Codex's own approval prompt when uncertain
// (set fallthrough_strategy=allow|deny in your overrides for fully
// unattended runs — at your own risk).
//
// Codex constraint: at the time of writing, Codex hooks always set
// tool_name="Bash"; the LLM must classify by command shape rather
// than by tool kind, so the rules below are written as Bash patterns.

{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/codex.schema.json',

  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
  },

  // What to do when the LLM is uncertain (returns "fallthrough"):
  //   'ask'   (default): defer to Codex's permission prompt
  //   'allow': auto-allow uncertain operations (use with care; intended for fully autonomous runs)
  //   'deny':  auto-deny uncertain operations (safer default for unattended automation)
  // Only LLM uncertainty is affected; runtime-mode fallthroughs (no API key, etc.) still defer.
  // fallthrough_strategy: 'ask',

  allow: [
    'Read-only Bash: ls, cat, head, tail, less, file, stat, find/grep without -exec/--delete, git status/log/diff/show/branch/remote -v.',
    'Local build/test against project-defined scripts: make, just, mise run, pnpm test, go test, cargo test, etc.',
    'Package install confined to this repo: pnpm/cargo/go install with no global flags.',
    'Git feature-branch operations on non-protected branches.',
  ],

  deny: [
    'Download and Execute: curl|bash, wget|sh, eval "$(curl ...)" against remote URLs. deny_message: Pipeline-to-shell of remote content is unsafe; download, review, then run locally instead.',
    'Direct one-shot remote package execution bypassing project scripts: npx / pnpx / bunx with unfamiliar packages. deny_message: Use the project script (mise / just / make) instead.',
    'sudo or other privilege escalation. deny_message: Privilege escalation is not allowed from the hook context.',
    'rm -rf or mv targeting paths outside the workspace. deny_message: Out-of-repo destructive operations are blocked.',
    'Git destructive: push --force(-with-lease), branch -D on protected branches, push --delete, rebase --root on shared branches. deny_message: Destructive git operations require explicit human action.',
    'Unrestricted network out: nc, ssh, scp, ftp to non-allowlisted hosts. deny_message: Network-out tools are blocked from the hook context.',
  ],

  environment: [
    'Codex Bash-only constraint: tool_name is always "Bash"; classify based on command shape and recent_transcript rather than by tool kind.',
    'Trusted repo: assume the repo is the trust boundary; treat anything outside it as untrusted.',
    'Path scope: when a command targets paths outside cwd (e.g. /etc/, /usr/, ~/.ssh/), treat as out-of-repo and lean toward deny unless clearly read-only and benign.',
  ],
}
