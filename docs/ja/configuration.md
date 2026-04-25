# ccgate — Configuration

[English version (docs/configuration.md)](../configuration.md)

target 横断の設定リファレンス: layering、`fallthrough_strategy`、metrics、既知の制約。

> 設定項目テーブル、layering 順序、`fallthrough_strategy` の解説は **[ルート README (docs/ja/README.md)](README.md)** を一次情報としてください。本ページは README に収まりきらない情報 (multi-target authoring、advanced overrides、トラブルシューティング) のための placeholder です。

このページで扱う予定:

- `LoadOptions` — 各 ccgate target が読むファイル順
- Claude Code と Codex CLI の jsonnet を重複させずに共有するパターン
- `fallthrough_strategy` の決定木と監査レシピ
- メトリクス出力スキーマ (JSONL フィールド、集計ルール) — Grafana / Datadog 等への配管に有用
