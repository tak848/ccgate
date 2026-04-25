# ccgate — Architecture

[English version (docs/architecture.md)](../architecture.md)

パッケージ構成と新 target 追加手順。

## レイアウト (v0.6)

```
ccgate/
├── main.go                      # cli.Run への委譲のみ
├── schemas/                     # 公開 JSON schema (commit 済み)
│   ├── claude.schema.json
│   └── codex.schema.json
├── scripts/genschema/           # 上記の generator
└── internal/
    ├── cli/                     # kong subcommand 結線
    ├── cmd/
    │   ├── claude/              # Claude Code hook (Run / Init / Metrics)
    │   └── codex/               # Codex CLI hook (Run / Init / Metrics)
    ├── prompt/                  # 共通 prompt builder (target-aware section)
    ├── llm/                     # 共通 type (Provider / Prompt / Output / Decision)
    │   └── anthropic/           # Provider 実装
    ├── config/                  # 共通 LoadOptions + jsonnet merge
    ├── metrics/                 # 共通 writer / reader / report
    ├── gate/                    # claude 専用の legacy orchestration (リファクタ中)
    ├── hookctx/                 # claude 専用 HookInput / settings / transcript
    └── gitutil/                 # 共通 git helper
```

> `internal/gate/` と `internal/hookctx/` は現時点で `cmd/claude/` から使われています。これらを `cmd/claude/` 配下に物理移管するのは follow-up issue として、v0.6 PR の境界を保っています。

## 新 target の追加手順

1. `internal/cmd/<target>/` を作成し、`Run`, `Init`, `Metrics`, `LoadOptions()` を実装
2. `defaults.jsonnet` を embed (Claude Code parity: allow + deny + environment)
3. `internal/cli/<target>_cmd.go` で kong-bound subcommand struct を定義し、`internal/cli/cli.go` の dispatch エントリを追加
4. `mise run schema` で per-target schema を生成 (target が異なる Config struct を持つ場合は `scripts/genschema/main.go` を拡張)
5. `docs/<target>.md` + `docs/ja/<target>.md` を 1:1 で追加 (en/ja ミラー)

> コードスニペット入りの完全な手順は今後追記予定。現時点では `internal/cmd/codex/` を実装例として参照してください。

## Spec Ledger

target ごとの仕様 verify 状況は v0.6 plan ファイル (`.claude/plans/codex-cli-hook-system-piped-badger.md`、section A2) で管理しています。adapter / docs / fixture / 仕様参照を伴うコードを変更したら、同じ PR 内で関連する Spec Ledger 行を更新してください。
