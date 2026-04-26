# Security policy

## Supported versions

ccgate is pre-1.0 and only the most recently released version on the `main` branch receives security fixes. Older minor lines (e.g. `v0.5.x` after `v0.6.0` ships) do not get backports unless someone steps up to maintain them; please update to the latest release before reporting.

## Reporting a vulnerability

ccgate runs as a `PermissionRequest` hook for AI coding tools. A bug in
ccgate that causes it to **incorrectly allow** a destructive operation
(or otherwise leak secrets through prompts / metrics) is treated as a
security issue and not a normal bug.

If you find one, please **report privately** via one of:

- GitHub's [private vulnerability reporting](https://github.com/tak848/ccgate/security/advisories/new)
  (preferred — tracks the report alongside the repo)
- Or contact the maintainer directly through the address listed on the
  GitHub profile [@tak848](https://github.com/tak848)

Please do **not** open a public issue for security-sensitive reports.

We aim to acknowledge new reports within **7 days** on a best-effort basis (this is a single-maintainer project; busier weeks may take longer). The acknowledgement will include a rough estimate of when a fix or mitigation can land.

## What counts as in-scope

- ccgate emitting `allow` for a command/tool input that the embedded
  defaults explicitly call out as deny / unsafe (regression of the
  default rules).
- The system prompt or metrics output leaking secrets that ccgate had
  access to but did not intend to forward (`tool_input.content`,
  `tool_input.content_updates`, environment variable values, etc.).
- ccgate writing log / metrics files with permissions that other local
  users can read or modify.
- Hook-input parsing crashing in a way that lets a malicious payload
  bypass classification entirely.

## What is out of scope

- The LLM (Claude Haiku) producing a wrong allow / deny decision on a
  legitimately ambiguous request — that is the philosophy of the
  three-tier safety net (LLM judgment + `fallthrough_strategy=ask` +
  user responsibility), not a vulnerability.
- The upstream tool (Claude Code, Codex CLI) doing something unsafe
  outside ccgate's hook surface. Report those upstream.
- Bugs in the user's own `~/.claude/ccgate.jsonnet` or
  `~/.codex/ccgate.jsonnet` overrides.

## Disclosure

Once a fix is ready we'll publish a release with a CHANGELOG entry that
credits the reporter (with consent).
