{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/ccgate.schema.json',

  // This file replaces embedded defaults entirely.
  // Start from: ccgate init > ~/.claude/ccgate.jsonnet

  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    timeout_ms: 40000,
  },

  // Full allow rules (replaces defaults, not appended)
  allow: [
    'Read-Only Operations: Read, Glob, Grep, and other read-only tools.',
    'Local Development: Build, test, lint, format commands in the current repository.',
    'Git Feature Branch: Git operations on non-protected branches.',
    'Package Manager Install: npm install, go mod tidy, pip install, etc.',
    'Draft PR Creation: If the operation creates a pull request AND draft is true in tool_input_raw, allow immediately.',
  ],

  // Full deny rules (replaces defaults, not appended)
  // deny_message is an optional hint -- the LLM adapts it to the specific situation.
  deny: [
    'Download and Execute: Piping downloaded content to a shell (curl|bash, wget|sh, etc.).',
    'Direct Tool Invocation: npx, pnpx, pnpm exec, bunx, etc.',
    'Git Destructive: force push, deleting remote branches, rewriting history.',
    'Out-of-Repo Deletion: rm -rf targeting paths outside the current repository.',
    'Sibling Checkout / Worktree Confusion: When is_worktree is true, deny access to primary_checkout_root.',
    // Optional: add deny_message for custom user-facing messages
    // 'Custom Rule: ... deny_message: Custom explanation shown to Claude when denied.',
  ],

  environment: [
    '**Trusted repo**: The git repository the session started in.',
    '**Current worktree context**: Prefer the current worktree over sibling checkouts.',
  ],
}
