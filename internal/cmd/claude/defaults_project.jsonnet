{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/claude.schema.json',

  // Project-local ccgate configuration.
  // This file adds restrictions on top of the global config.
  // Place as: {repo_root}/.claude/ccgate.local.jsonnet
  // (root-level {repo_root}/ccgate.local.jsonnet was removed in v0.6.0;
  //  for the Codex CLI hook use {repo_root}/.codex/ccgate.local.jsonnet)
  // IMPORTANT: Must NOT be tracked by git (add to .gitignore).

  // Force the LLM's "fallthrough" decisions to a fixed allow/deny.
  // Useful for fully autonomous runs (schedulers, bots) that cannot block on a prompt.
  //   fallthrough_strategy: 'deny',  // safer: refuse anything the LLM is unsure about
  //   fallthrough_strategy: 'allow', // riskier: auto-approve everything the LLM is unsure about
  //                                  // (Claude Code only delivers hook messages on deny, so
  //                                  //  Claude will not see any warning when this fires.)

  // `allow` / `deny` / `environment` REPLACE the value carried over
  // from earlier layers (embedded defaults + ~/.claude/ccgate.jsonnet).
  // For project-local overrides you almost always want to ADD on top
  // instead -- use the `append_*` keys below.

  append_deny: [
    // Add project-specific deny rules here.
    // Examples:
    // 'Network Access: Deny curl, wget, or HTTP requests to external services. deny_message: Network access is restricted in this project.',
    // 'Script Execution: Deny running shell scripts (.sh, .bash) from this repository. deny_message: Script execution is restricted in this project.',
  ],

  append_environment: [
    // Describe the project context for the LLM.
    // Examples:
    // '**Untrusted repository**: Apply strict security policies.',
    // '**Production repository**: Deny any operations that could affect production.',
  ],
}
