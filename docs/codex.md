# ccgate — OpenAI Codex CLI

[日本語版 (docs/ja/codex.md)](ja/codex.md)

Codex-CLI-specific notes for the `ccgate codex` hook.

> Until this guide is filled in, the **[root README](../README.md)** is the source of truth for Codex CLI setup. This page exists so deep-dive material has a stable URL to grow into.

## Status

- **Experimental.** Codex hooks are upstream-experimental as of 2026-04. Schema and behavior may change without notice; ccgate tracks each verified spec entry in `.claude/plans/codex-cli-hook-system-piped-badger.md` (Spec Ledger, section A2).
- **Linux/macOS only.** Codex hooks are upstream-disabled on Windows; `ccgate codex` exits with a pointer to the Codex docs.
- **Bash-only assumption.** Codex hooks always set `tool_name="Bash"` today, so ccgate's defaults classify by command shape rather than tool kind.

## Topics planned for this page

- Hook registration in `~/.codex/hooks.json` and the `codex_hooks` feature flag (config.toml gating)
- `~/.codex/config.toml` (`approval_policy` / `sandbox_mode` / `prefix_rules`) — currently **not** ingested by ccgate; tracked as follow-up issues (see plan L#3, L#4)
- `permission_mode=plan` detection for Codex (plan L#1)
- Codex transcript JSONL parsing for the `recent_transcript` context (plan L#2)
- Differences from Claude Code (see [docs/claude.md](claude.md))
