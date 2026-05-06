{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/claude.schema.json',

  // Default safety rules for ccgate.
  // Inspired by Claude Code Auto Mode defaults.
  // To customize, write either:
  //   - ~/.claude/ccgate.jsonnet (global), or
  //   - <repo>/.claude/ccgate.local.jsonnet (project-local, untracked-only)
  // and at either layer use append_* to add on top of what's
  // inherited (picks up future ccgate quality updates automatically),
  // or allow / deny / environment to replace the inherited list
  // wholesale. See the README "Setup -- Claude Code" for examples.

  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    // Alternatives:
    //   name: 'openai',  model: 'gpt-4o-mini',        (env: OPENAI_API_KEY)
    //   name: 'gemini',  model: 'gemini-2.0-flash',    (env: GEMINI_API_KEY)
    // base_url:  route through an OpenAI-/Anthropic-compatible proxy.
    //            See README "Routing through a compatible proxy".
    // timeout_ms: API timeout in ms, default 20000.
    // auth: short-lived / rotating credentials. Discriminated
    //   by auth.type:
    //     auth: { type: 'exec', command: '/usr/local/bin/my-broker --provider anthropic' }
    //     auth: { type: 'file', path: '~/.config/my-broker/anthropic.json' }
    //   The provider block is replaced atomically across config layers,
    //   so a project-local config that restates `provider` must repeat
    //   the auth block. See docs/api-key-helper.md for the full helper
    //   contract, examples, and recovery checklist.
  },

  // What to do when the LLM is uncertain (returns "fallthrough"):
  //   'ask'   (default): defer to Claude Code's permission prompt
  //   'allow': auto-allow uncertain operations (use with care; intended for fully autonomous runs)
  //   'deny':  auto-deny uncertain operations (safer default for unattended automation)
  // Only LLM uncertainty is affected; bypassPermissions / dontAsk / missing API key still defer.
  // fallthrough_strategy: 'ask',

  allow: [
    'Read-Only Operations: Read, Glob, Grep, and other read-only tools that do not modify state.',
    'Local Development: Build, test, lint, format commands in the current repository.',
    'Git Feature Branch: Git operations on non-protected branches (not main, master, release/*, prod).',
    'Package Manager Install: Package manager commands (npm install, go mod tidy, pip install, etc.) in the current repository.',
  ],

  // deny_message is an example hint -- the LLM should adapt it to the specific situation.
  // It helps the LLM understand WHY the rule exists and what kind of feedback to give.
  deny: [
    'Download and Execute: Piping downloaded content to a shell (curl|bash, wget|sh, etc.), or executing remote scripts without review. deny_message: Downloading and executing remote code is not allowed.',
    'Direct Tool Invocation: Running tools directly via npx, pnpx, pnpm exec, bunx, etc. instead of using project-defined scripts. deny_message: Use project-defined scripts instead.',
    'Git Destructive: force push (--force, --force-with-lease), deleting remote branches (push --delete), or rewriting published history. Check recent_transcript -- if the user explicitly requested the operation, fallthrough instead of deny. deny_message: Destructive git operation detected. Confirm with user.',
    'Out-of-Repo Deletion: rm -rf or destructive file operations targeting paths outside the current repository (check referenced_paths against repo_root). Deletion within the repository (node_modules, dist, build artifacts) is fine. deny_message: Deletion outside the repository is not allowed.',
    'Sibling Checkout / Worktree Confusion: When is_worktree is true, any access to paths under primary_checkout_root or other sibling checkouts MUST be denied. deny_message: Accessing paths outside the current worktree is not allowed.',
  ],

  environment: [
    '**Trusted repo**: The git repository the session started in.',
  ],
}
