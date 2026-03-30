{
  ['$schema']: './permission-gate.schema.json',
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    timeout_ms: 40000,
  },
  // log_path: '~/.claude/logs/ccgate.log',
  allow: [
    'Read-Only Operations: GET requests, read-only API calls, or queries that do not modify state.',
    'Local Operations: Read-only work inside the current repository or current worktree.',
    'Draft PR Creation: If the operation creates a pull request AND draft is true in tool_input_raw, allow immediately. If draft is false or absent, fallthrough.',
  ],
  deny: [
    'Git Destructive: force push (--force), deleting remote branches (push --delete), or rewriting published history. Check recent_transcript and tool_input.description — if the user explicitly requested the operation, fallthrough instead of deny. deny_message: git の破壊的操作です。ユーザーの明示的な指示を確認してください。',
    'Sibling Checkout / Worktree Confusion: When is_worktree is true, any access to paths under primary_checkout_root or other sibling checkouts MUST be denied. No exceptions. Do not deliberate. deny_message: 現在のワークツリー外のチェックアウトにアクセスしようとしています。ワークツリー内のパスを使用してください。',
  ],
  environment: [
    '**Trusted repo**: The git repository the session started in.',
    '**Current worktree context**: Prefer the current worktree and its own files over sibling checkouts unless the user clearly asks otherwise.',
  ],
}
