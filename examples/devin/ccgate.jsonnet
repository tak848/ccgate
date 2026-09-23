// Example global Devin CLI config for ccgate.
// Place at ~/.config/devin/ccgate.jsonnet to override the embedded defaults.
// Start from: ccgate devin init > ~/.config/devin/ccgate.jsonnet
//
// This file REPLACES the embedded defaults entirely (allow / deny / environment
// are taken from here, not appended to defaults). Project-local overrides at
// {repo_root}/.devin/ccgate.local.jsonnet still append on top.
//
// Devin hooks fire for exec, edit, write, apply_patch, MCP tool calls, and
// other surfaces; classify by tool_name + the full tool_input JSON, not by
// tool kind alone.

{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/devin.schema.json',

  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    timeout_ms: 40000,
    // Profile auth (Anthropic only): see docs/api-key-helper.md.
    // auth: { type: 'profile', profile: 'ccgate' },                           // reads `ant auth login --profile ccgate` credentials, SDK refreshes
  },

  // What to do when the LLM is uncertain (returns "fallthrough"):
  //   'ask'   (default): defer to Devin's permission prompt
  //   'allow': auto-allow uncertain operations (use with care; intended for fully autonomous runs)
  //   'deny':  auto-deny uncertain operations (safer default for unattended automation)
  // fallthrough_strategy: 'ask',

  allow: [
    'Read-only operations: exec inspection commands (ls, cat, head, tail, less, file, stat, find/grep without -exec/--delete, git status/log/diff/show/branch/remote -v), or any tool whose tool_input shape implies pure read.',
    'Local build/test against project-defined scripts: make, just, mise run, pnpm test, go test, cargo test, etc.',
    'Package install confined to this repo: pnpm/cargo/go install with no global flags.',
    'Git feature-branch operations on non-protected branches.',
  ],

  deny: [
    'Download and Execute: curl|bash, wget|sh, eval against remote URLs.',
    'Direct one-shot remote package execution bypassing project scripts: npx / pnpx / bunx with unfamiliar packages.',
    'sudo or other privilege escalation.',
    'rm -rf or mv targeting paths outside the workspace, or apply_patch hunks that touch paths outside the workspace.',
    'Git destructive: push --force(-with-lease), branch -D on protected branches, push --delete.',
    'Unrestricted network out: nc, ssh, scp, ftp to non-allowlisted hosts.',
  ],
}
