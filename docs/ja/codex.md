# ccgate — OpenAI Codex CLI

[English version (docs/codex.md)](../codex.md)

`ccgate codex` フック専用のドキュメント。

> このページが埋まるまで、Codex CLI のセットアップは **[ルート README (docs/ja/README.md)](README.md)** を一次情報としてください。本ページは詳細解説のための placeholder です。

## ステータス

- **Experimental.** Codex hooks は upstream で experimental 扱い (2026-04 時点)。スキーマや挙動が予告なく変更される可能性があります。仕様の verify 状況は `.claude/plans/codex-cli-hook-system-piped-badger.md` の Spec Ledger (section A2) で追跡しています。
- **Linux/macOS で検証済み、Windows は未検証。** OpenAI Codex hooks docs には `windows_managed_dir` が一級フィールドとして記載されているため、binary レベルでは block されません。ccgate の Codex flow は Windows で動作未検証 — Windows で使う場合は untested 扱いとしてください。
- **Tool-agnostic。** Codex hooks は Bash、`apply_patch`、MCP tool 呼び出しなど複数の tool surface で発火します。ccgate は `tool_name` + `tool_input` JSON 全体で分類します。

## このページで扱う予定

- `~/.codex/hooks.json` での hook 登録と `codex_hooks` feature flag (config.toml gating)
- `~/.codex/config.toml` (`approval_policy` / `sandbox_mode` / `prefix_rules`) の取り込み — 現時点で **未対応**、follow-up issue (plan L#3, L#4)
- Codex 側 `permission_mode=plan` 検出 (plan L#1)
- Codex transcript JSONL の `recent_transcript` 解析 (plan L#2)
- Claude Code との挙動差分 ([docs/ja/claude.md](claude.md) 参照)
