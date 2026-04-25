# ccgate — Claude Code

[日本語版 (docs/ja/claude.md)](ja/claude.md)

Claude-Code-specific notes for the `ccgate claude` hook.

> Until this guide is filled in, the **[root README](../README.md)** is the source of truth for Claude Code setup, configuration, and known limitations. This dedicated page exists so deep-dive material has a stable URL to grow into.

Topics planned for this page (see follow-up issues for status):

- Hook registration in `~/.claude/settings.json` (covered in README today)
- Plan mode behavior (`permission_mode == "plan"`) and its known limits ([#37](https://github.com/tak848/ccgate/issues/37))
- `permission_suggestions` semantics and how ccgate uses them
- `recent_transcript` parsing and the user-intent escalation rule
- Differences from Codex CLI (see [docs/codex.md](codex.md))

## Bare `ccgate` (no args + stdin pipe)

`ccgate` with no args reading from stdin is identical to `ccgate claude`. This invocation is the canonical Claude Code hook command and will keep working forever — existing `~/.claude/settings.json` entries using `"command": "ccgate"` are not touched by the v0.6 multi-target refactor.
