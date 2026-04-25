# ccgate — Configuration

[日本語版 (docs/ja/configuration.md)](ja/configuration.md)

Cross-target configuration reference: layering, `fallthrough_strategy`, metrics, known limits.

> The **[root README](../README.md)** carries the canonical config field table, layering order, and `fallthrough_strategy` walkthrough today. This page is reserved for material that exceeds README scope (multi-target authoring, advanced overrides, troubleshooting recipes). Until then, the README is the source of truth.

Topics planned:

- `LoadOptions` — what each ccgate target reads, in order
- Sharing rules between Claude Code and Codex CLI without duplicating the jsonnet
- `fallthrough_strategy` decision tree and audit recipe
- Metrics output schema (JSONL fields, aggregation rules) — useful for piping into Grafana / Datadog
