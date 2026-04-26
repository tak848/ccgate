# ccgate — OpenAI Codex CLI

[日本語版 (docs/ja/codex.md)](ja/codex.md)

Codex-CLI-specific notes for the `ccgate codex` hook.

> Until this guide is filled in, the **[root README](../README.md)** is the source of truth for Codex CLI setup. This page exists so deep-dive material has a stable URL to grow into.

## Status

- **Experimental.** Codex hooks are upstream-experimental as of 2026-04. Schema and behavior may change without notice; treat the OpenAI [Codex hooks docs](https://developers.openai.com/codex/hooks) as the source of truth and re-verify before relying on a specific field.
- **Tested on Linux/macOS; Windows untested.** Upstream Codex docs list `windows_managed_dir` as a first-class config field, so Windows is not blocked at the binary level. ccgate's Codex flow has not been exercised on Windows -- treat any usage there as untested.
- **Tool-agnostic.** Codex hooks fire for Bash, `apply_patch`, MCP tool calls, and other surfaces. ccgate classifies by `tool_name` + the full `tool_input` JSON, not by tool kind alone.

## Topics planned for this page

- Hook registration in `~/.codex/hooks.json` and the `codex_hooks` feature flag (config.toml gating)
- `~/.codex/config.toml` (`approval_policy` / `sandbox_mode` / `prefix_rules`) — currently **not** ingested by ccgate; tracked as follow-up issues (see plan L#3, L#4)
- `permission_mode=plan` detection for Codex (plan L#1)
- Codex transcript JSONL parsing for the `recent_transcript` context (plan L#2)
- Differences from Claude Code (see [docs/claude.md](claude.md))
