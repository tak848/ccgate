// Example global Claude Code config for ccgate.
// Place at ~/.claude/ccgate.jsonnet to override the embedded defaults.
// Start from: ccgate claude init > ~/.claude/ccgate.jsonnet
//
// This file REPLACES the embedded defaults entirely (allow / deny / environment
// are taken from here, not appended to defaults). Project-local overrides at
// {repo_root}/.claude/ccgate.local.jsonnet still append on top.

{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/claude.schema.json',

  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    timeout_ms: 40000,
  },

  // What to do when the LLM is uncertain (returns "fallthrough"):
  //   'ask'   (default): defer to Claude Code's permission prompt
  //   'allow': auto-allow uncertain operations (use with care; intended for fully autonomous runs)
  //   'deny':  auto-deny uncertain operations (safer default for unattended automation)
  // fallthrough_strategy: 'ask',

  allow: [
    'Read-Only Operations: Read, Glob, Grep, and other read-only tools.',
    'Local Development: Build, test, lint, format commands in the current repository.',
    'Git Feature Branch: Git operations on non-protected branches.',
    'Package Manager Install: pnpm install, go mod tidy, uv sync, etc.',
    'Draft PR Creation: If the operation creates a pull request AND draft is true in tool_input_raw, allow immediately.',
  ],

  // deny_message is an optional hint -- the LLM adapts it to the specific situation.
  deny: [
    'Download and Execute: Piping downloaded content to a shell (curl|bash, wget|sh, etc.).',
    'Direct Tool Invocation: npx, pnpx, pnpm exec, bunx, etc.',
    'Git Destructive: force push, deleting remote branches, rewriting history.',
    'Out-of-Repo Deletion: rm -rf targeting paths outside the current repository.',
    'Sibling Checkout / Worktree Confusion: When is_worktree is true, deny access to primary_checkout_root.',
  ],

  environment: [
    '**Trusted repo**: The git repository the session started in.',
    '**Current worktree context**: Prefer the current worktree over sibling checkouts.',
  ],
}
