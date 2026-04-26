---
name: Bug report
about: Report a bug in ccgate (please file security-sensitive issues privately, see SECURITY.md)
labels: bug
---

## What happened

<!-- A short, factual description of the bug. -->

## Reproduction

- ccgate version: <!-- output of `ccgate --version` -->
- Target: <!-- claude / codex / both -->
- OS / arch: <!-- e.g. macOS 15 arm64, Linux x86_64 -->
- Upstream tool version: <!-- e.g. Claude Code 1.2.3, Codex CLI 0.4.0 -->

Minimal steps to reproduce:

1. ...
2. ...

If a `HookInput` payload triggers it, attach a redacted JSON snippet
(strip secrets, file contents, transcript). The payload that ccgate
itself logged at `$XDG_STATE_HOME/ccgate/<target>/ccgate.log` is a
good starting point.

## What you expected

<!-- What ccgate should have done. -->

## What actually happened

<!-- What ccgate did instead. Include the relevant log lines from
$XDG_STATE_HOME/ccgate/<target>/ccgate.log if available. -->

## Notes

<!-- Any other context: custom rules in `~/.claude/ccgate.jsonnet`,
`fallthrough_strategy` value, etc. Optional. -->
