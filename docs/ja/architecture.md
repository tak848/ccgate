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

`internal/cmd/codex/` が各ステップの完全な実装例。主要 shape:

```go
// internal/cmd/codex/codex.go
//go:embed defaults.jsonnet
var defaultsJsonnet string

func LoadOptions() config.LoadOptions {
    home, _ := os.UserHomeDir()
    sd := stateDir() // $XDG_STATE_HOME/ccgate/codex
    return config.LoadOptions{
        GlobalConfigPath:          filepath.Join(home, ".codex", config.BaseConfigName),
        ProjectLocalRelativePaths: []string{filepath.Join(".codex", config.LocalConfigName)},
        EmbedDefaults:             defaultsJsonnet,
        DefaultLogPath:            filepath.Join(sd, "ccgate.log"),
        DefaultMetricsPath:        filepath.Join(sd, "metrics.jsonl"),
    }
}
```

```go
// internal/cmd/codex/prompt.go
p := prompt.Build(prompt.Args{
    TargetName:          "Codex CLI",
    PlanMode:            false,
    HasRecentTranscript: false, // Codex は現状 transcript field を deliver しない
    TargetSection:       codexTargetSection,
    Allow:               cfg.Allow,
    Deny:                cfg.Deny,
    Environment:         cfg.Environment,
    UserPayload:         string(user),
})
```

```go
// internal/cli/codex_cmd.go
type CodexCmd struct {
    Hook    CodexHookCmd    `cmd:"" default:"withargs" name:"hook" help:"Run the Codex CLI hook from stdin (default; same as 'ccgate codex')."`
    Init    CodexInitCmd    `cmd:""                                help:"Output the embedded Codex CLI default configuration."`
    Metrics CodexMetricsCmd `cmd:""                                help:"Show Codex CLI usage metrics."`
}
```

その他の主要ファイル:

- `defaults.jsonnet` (`//go:embed`) は Claude defaults と同じ shape の allow / deny / environment ガイダンス。`defaults_test.go` でルール taxonomy を pin (将来の edit が silent にカテゴリを落とせないように)
- `input.go` は metrics 層が理解できる typed view + LLM 用に raw `tool_input` JSON を保持
- `entry.go` で per-invocation `metrics.Entry` の shape を決定 -- Claude 側と field 互換なので `metrics.PrintReport` がどちらの target も同じく集計可能

## Defaults parity (Claude vs Codex)

両 target とも `allow + deny + environment` ガイダンスを持ちます (project philosophy)。wording が異なるのは、Claude が tool 種別 (Read/Edit/Bash/etc.) で分類する一方、Codex は同じ surface で Bash + apply_patch + MCP を捌くため、Codex defaults は command shape や MCP server trust の言葉で書かれているためです。

意図的な片側だけのカテゴリと理由:

| 側         | カテゴリ                                       | 理由                                                                                                                                |
|------------|------------------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------|
| Claude     | `Sibling Checkout / Worktree Confusion`        | Claude Code は `is_worktree` を surface する。Codex には無いため worktree 混同は Claude 固有の失敗モード。                            |
| Claude     | `Library Source Read`, `Read-Only Operations`  | Claude は Read/Glob/Grep を独立 tool surface として持つ。Codex は同じ操作を Bash 経由で捌くため read-only Bash ルールでカバー。       |
| Claude     | `Draft PR Creation`                            | Claude は `gh pr create` の `draft` flag を `tool_input_raw` で持つ。Codex hook は同 field を surface しない。                         |
| Codex      | `MCP tools whose server is explicitly trusted` | MCP 専用 allow パス。Claude の hook は現状 MCP を ccgate に dispatch しない。                                                          |
| Codex      | `MCP tools that advertise destructive side effects` | 同上 (MCP 専用 deny レーン)。                                                                                                       |
| Codex      | `sudo or other privilege escalation`           | Codex hook は privilege escalation Bash も ccgate に通すので明示。Claude は `~/.claude/settings.json` で同等のカバレッジを持つ。      |
| Codex      | `Unrestricted network out`                     | Codex の auto-approval ladder は `nc` / `ssh` / `scp` / `ftp` まで届くため、Codex 側で deny ルールを前面に出している。                |
| Codex      | `apply_patch` is a write surface (environment) | Codex 固有の tool surface。Claude は `Edit` / `Write` を直接 dispatch するため不要。                                                  |

両方に存在するが wording が異なるカテゴリ (`Download and Execute`, `Direct Tool Invocation` ↔ `Direct one-shot remote package execution bypassing project scripts`, `Git Destructive` ↔ `Git destructive`, `Out-of-Repo Deletion` ↔ `rm -rf or mv targeting paths outside the workspace`) は同じ意図をカバーしています。wording の自動 equality assert は意図的に re-phrase してるため fragile になるので、defaults 変更時はレビュアーが手動でパリティを確認してください。

## Upstream 仕様

ccgate の挙動は各 target の hook upstream docs に従っています。adapter / fixture / 仕様参照を変更する際は、まずこれらを source of truth として確認してください:

- Claude Code hooks: <https://code.claude.com/docs/en/hooks>
- OpenAI Codex hooks: <https://developers.openai.com/codex/hooks>
- OpenAI Codex config reference: <https://developers.openai.com/codex/config-reference>

PR で hook payload の parse 方法や依存 field を変更する場合は、関連 upstream セクションへのリンクを PR description に含めてください (upstream はまだ動くため、レビュアーが現行 docs で再確認できるように)。
