# ccgate

[![CI](https://github.com/tak848/ccgate/actions/workflows/ci.yml/badge.svg)](https://github.com/tak848/ccgate/actions/workflows/ci.yml)
[![release](https://github.com/tak848/ccgate/actions/workflows/release.yml/badge.svg)](https://github.com/tak848/ccgate/releases)

AI コーディングツール向けの **PermissionRequest** フックです。ツール実行の許可判定を LLM (Claude Haiku) に委任し、設定ファイルに記述したルールに基づいて allow / deny / fallthrough を返します。

対応ターゲット:

- **[Claude Code](https://docs.anthropic.com/en/docs/claude-code)** — 安定
- **[OpenAI Codex CLI](https://developers.openai.com/codex/hooks)** — experimental。Linux/macOS で検証済み、Windows は未検証 (block はされない)

[English README](../../README.md)

## 仕組み

```
Claude Code / Codex CLI (PermissionRequest hook)
  │
  │  stdin: HookInput JSON
  ▼
ccgate
  ├── 設定読み込み (~/.claude/ccgate.jsonnet  または  ~/.codex/ccgate.jsonnet)
  ├── コンテキスト構築 (git repo, paths, recent transcript [Claude のみ])
  ├── Claude Haiku API 呼び出し (Structured Output)
  └── stdout: allow / deny / fallthrough
```

1. AI ツールがツール実行前に `ccgate` を呼び出す
2. `ccgate` は jsonnet 設定の allow/deny ルールをシステムプロンプトに組み込み、ツール情報・git コンテキスト・(Claude のみ) 直近の会話履歴を Haiku に送信
3. Haiku の判定結果を AI ツールに返す

## CLI

```
ccgate                         stdin から HookInput JSON を読み込む (Claude Code hook)。
                               'ccgate claude' と等価。**永続的なデフォルト挙動** で、deprecation 予定なし。
                               既存の ~/.claude/settings.json の "command": "ccgate" 設定はそのまま動作し続ける。
ccgate claude                  bare ccgate と完全等価 (新規ユーザー向け推奨表記)
ccgate claude init [-p|-o|-f]  Claude Code 用の埋込デフォルトを出力
ccgate claude metrics [...]    Claude Code のメトリクス集計

ccgate codex                   stdin から HookInput JSON を読み込む (Codex CLI hook、experimental)
ccgate codex init [-o|-f]      Codex CLI 用の埋込デフォルトを出力
ccgate codex metrics [...]     Codex CLI のメトリクス集計
```

> `ccgate init` / `ccgate metrics` (top-level) は **v0.6.0 で廃止** されました。代わりに `ccgate claude init` / `ccgate claude metrics` (または codex 版) を使用してください。bare `ccgate` (hook 起動) は影響ありません。

## インストール

### mise (推奨)

mise `2026.4.20` 以降が必要です。このリリースから、同梱の aqua registry に ccgate が含まれます。

```bash
mise use -g aqua:tak848/ccgate
```

ccgate をグローバルに登録せず一度だけ試したい場合 (`npx` / `uvx` 相当):

```bash
mise exec aqua:tak848/ccgate -- ccgate --version
```

そのまま hook としても no-install で使い続けたい場合は、設定の hook `command` を `mise exec aqua:tak848/ccgate -- ccgate claude` (または `... -- ccgate codex`) に書き換えてください。hook 呼び出しごとに launcher の起動コストが乗るため、常用するなら上の `mise use -g` の方を推奨します。

### aqua

[aqua](https://aquaproj.github.io/) 標準 registry 経由 (registry `v4.498.0` 以降が必要 — ccgate が初めて登録された version)。aqua 管理下のプロジェクトで (`aqua.yaml` がない場合は `aqua init` を先に走らせる):

```bash
aqua g -i tak848/ccgate
aqua i
```

[グローバル aqua 設定](https://aquaproj.github.io/docs/tutorial/global-config) に入れる場合は aqua 公式チュートリアルに従ってください。

### go install

```bash
go install github.com/tak848/ccgate@latest
```

### GitHub Releases

[Releases](https://github.com/tak848/ccgate/releases) からバイナリをダウンロードし、PATH の通った場所に配置してください。

## セットアップ — Claude Code

### 1. 設定ファイルを配置 (オプション)

ccgate はデフォルトの安全ルールを内蔵しているため、設定ファイルなしでも動作します。

カスタマイズする場合:

```bash
ccgate claude init > ~/.claude/ccgate.jsonnet
```

`$schema` フィールドで [`schemas/claude.schema.json`](../../schemas/claude.schema.json) を参照しているため、エディタ補完が効きます。

### 2. Claude Code の hooks に登録

`~/.claude/settings.json`:

```json
{
  "hooks": {
    "PermissionRequest": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "ccgate claude"
          }
        ]
      }
    ]
  }
}
```

`"command": "ccgate"` (subcommand なし) でも永続的に動作します。bare `ccgate` は Claude Code hook の正規呼び出し方法です。

`ccgate` が PATH に通っていない場合は、hook の `command` を等価な呼び出し (例: `mise exec aqua:tak848/ccgate -- ccgate claude`) または絶対パスに書き換えてください。

### 3. API キー

環境変数 `CCGATE_ANTHROPIC_API_KEY` または `ANTHROPIC_API_KEY` を設定してください。

## セットアップ — Codex CLI (experimental)

> Codex hooks は upstream で experimental 扱い (2026-04 時点)。スキーマや挙動が今後変わる可能性があります。ccgate の Codex 対応は Linux/macOS で検証済み。OpenAI Codex hooks docs には `windows_managed_dir` が一級フィールドとして記載されているので、Windows も binary レベルでは block されません。ただし ccgate の Codex flow は Windows で動作未検証なので、Windows で使う場合は untested 扱いとしてください。

### 1. 設定ファイルを配置 (オプション)

```bash
ccgate codex init > ~/.codex/ccgate.jsonnet
```

defaults は Claude Code と同じ思想 (allow + deny + environment)。Codex hooks は Bash、`apply_patch`、MCP tool 呼び出しなど複数の tool surface で発火し、ccgate のルールは全 surface を対象にしています。system prompt は LLM に「`tool_name` + `tool_input` の JSON 全体を見て分類せよ」と指示します。

### 2. Codex hook として登録

[Codex hooks ドキュメント](https://developers.openai.com/codex/hooks) を参照して `PermissionRequest` hook の `command` に ccgate を指定してください。Codex のバージョンによっては `~/.codex/config.toml` で hooks の feature flag を有効化する必要があります。

### 3. API キー

Claude Code と同じ環境変数 (`CCGATE_ANTHROPIC_API_KEY` / `ANTHROPIC_API_KEY`) を共有します。

## 設定

### 設定ファイルの読み込み順序 (target ごと)

| 順序 | Claude Code | Codex CLI |
|----:|-------------|-----------|
| 1 | 組み込みデフォルト (グローバル設定がない場合のフォールバック) | 同じ |
| 2 | `~/.claude/ccgate.jsonnet` — グローバル (組み込みデフォルトを**完全に置換**) | `~/.codex/ccgate.jsonnet` — グローバル (同じ) |
| 3 | `{repo_root}/.claude/ccgate.local.jsonnet` — プロジェクトローカル (Git 未追跡のみ、**追加**) | `{repo_root}/.codex/ccgate.local.jsonnet` — プロジェクトローカル (同じ) |

グローバル設定が存在する場合、組み込みデフォルトは**使われません**。グローバル設定が完全なベースです。
プロジェクトローカル設定は常にベースに**追加**されます (allow/deny/environment は append、provider 系は overwrite)。
プロジェクトローカル設定は **Git に追跡されていないファイルのみ** 読み込まれます。

> v0.6 の変更: `{repo_root}/ccgate.local.jsonnet` (root 直下、target ambiguous) は読み込まれなくなりました。`{repo_root}/.claude/ccgate.local.jsonnet` (または `.codex/...`) に rename して同等の挙動を維持してください。

### 設定項目

| フィールド               | 型                                | デフォルト                                                                       | 説明                                                                                                       |
|--------------------------|-----------------------------------|---------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------|
| `provider.name`          | string                            | `"anthropic"`                                                                   | プロバイダー名。`"anthropic"` のみ対応                                                                     |
| `provider.model`         | string                            | `"claude-haiku-4-5"`                                                            | モデル名 (例: `claude-haiku-4-5`, `claude-sonnet-4-6`, `claude-opus-4-6`)                                  |
| `provider.timeout_ms`    | int                               | `20000`                                                                         | API タイムアウト (ms)。`0` = タイムアウトなし                                                              |
| `log_path`               | string                            | `$XDG_STATE_HOME/ccgate/<target>/ccgate.log`                                    | ログファイルパス。`~` でホームディレクトリ展開                                                             |
| `log_disabled`           | bool                              | `false`                                                                         | ログ出力を完全に無効化                                                                                     |
| `log_max_size`           | int                               | `5242880`                                                                       | ローテーション閾値 (bytes, デフォルト 5MB)。`0` = ローテーションなし                                       |
| `metrics_path`           | string                            | `$XDG_STATE_HOME/ccgate/<target>/metrics.jsonl`                                 | メトリクス JSONL のパス                                                                                    |
| `metrics_disabled`       | bool                              | `false`                                                                         | メトリクス収集を完全に無効化                                                                               |
| `metrics_max_size`       | int                               | `2097152`                                                                       | ローテーション閾値 (bytes, デフォルト 2MB)。`0` = ローテーションなし                                       |
| `fallthrough_strategy`   | `"ask"` / `"allow"` / `"deny"`    | `"ask"`                                                                         | LLM が判定に迷った (`fallthrough`) 際の扱い。[完全自動運転モード](#完全自動運転モード-fallthrough_strategy) 参照 |
| `allow`                  | string[]                          | `[]`                                                                            | 許可ルール (自然言語、LLM が解釈)                                                                          |
| `deny`                   | string[]                          | `[]`                                                                            | 拒否ルール (mandatory)。`deny_message:` ヒント対応                                                         |
| `environment`            | string[]                          | `[]`                                                                            | LLM に渡すコンテキスト (信頼レベル、ポリシー等)                                                            |

`<target>` は Claude / Codex どちらの hook が呼ばれたかで `claude` / `codex` になります。pre-v0.6 の ccgate は両ファイルを `$XDG_STATE_HOME/ccgate/` 直下に書いていましたが、その path は後方互換のため `ccgate claude metrics` から引き続き読まれます。

## デフォルトルール

グローバル設定がない場合、ccgate は組み込みのデフォルトルールを使用します (target ごと):

**許可:** 読み取り専用操作、ローカル開発コマンド (project script 経由の build / test)、git フィーチャーブランチ操作、リポジトリ内に閉じたパッケージインストール。

**拒否:** リモートコードのダウンロード実行 (`curl|bash`)、direct one-shot remote package execution (`npx`/`pnpx`/`bunx` 等)、git 破壊的操作 (protected branch 含む)、リポジトリ外の削除、特権昇格。

`ccgate claude init` / `ccgate codex init` でデフォルト設定の全容を確認できます。カスタマイズする場合:

```bash
ccgate claude init    > ~/.claude/ccgate.jsonnet           # グローバル, claude (デフォルトを置換)
ccgate claude init -p > .claude/ccgate.local.jsonnet       # プロジェクトローカルテンプレート, claude (追加)
ccgate codex  init    > ~/.codex/ccgate.jsonnet            # グローバル, codex
```

## 完全自動運転モード (`fallthrough_strategy`)

デフォルトでは、LLM が判定に自信を持てない場合 ccgate は `fallthrough` を返し、上流ツール (Claude Code / Codex CLI) のインタラクティブ確認画面にフォールバックします。対話セッションでは妥当ですが、スケジューラやボットなど人間が「許可」を押せない環境では処理が止まります。

`fallthrough_strategy` を設定すると、LLM の判定迷いを allow/deny に強制変換できます:

```jsonnet
{
  // 安全側: 迷ったら拒否。無人実行ではこちらを推奨
  fallthrough_strategy: 'deny',
}
```

値:

- `ask` (デフォルト) — 上流ツールの確認画面に委ねる (既存の挙動)
- `deny` — 迷ったら自動拒否。deny メッセージには「user に聞くな、別コマンドで回避するな」という指示が含まれるため、実行が止まらず前に進む
- `allow` — 迷ったら自動許可。**危険側**: LLM 自身が判断に迷った操作を無条件に通すことになります。Claude Code / Codex とも `decision.message` は `deny` のときしか AI に届かないため、強制 allow の際 AI には警告が渡りません

対象は **LLM 判定の fallthrough に限定** です。API 応答の打ち切り/拒否、API キー欠損、`bypassPermissions`/`dontAsk` モード (Claude のみ)、`ExitPlanMode` / `AskUserQuestion` (Claude のみ) はいずれも従来通り上流ツールにフォールスルーされます。

強制発火した回数は `ccgate <target> metrics` の `F.Allow` / `F.Deny` 列 (JSON では `forced_allow` / `forced_deny`) で確認できるため、選んだ戦略が妥当に機能しているか後から監査できます。

## ログ・メトリクス

ログ・メトリクスは `$XDG_STATE_HOME/ccgate/<target>/` 配下に保存されます:

- `$XDG_STATE_HOME/ccgate/claude/{ccgate.log,metrics.jsonl}` — Claude Code
- `$XDG_STATE_HOME/ccgate/codex/{ccgate.log,metrics.jsonl}` — Codex CLI

両ファイルともサイズベースでローテーションします (`.log.1`, `.jsonl.1`)。

`ccgate claude metrics` は pre-v0.6 ccgate が書いていた `$XDG_STATE_HOME/ccgate/metrics.jsonl` も併せて読み込むため、既存ユーザーは過去のメトリクス履歴も継続して参照できます。jsonnet で `log_path` / `metrics_path` を明示している場合はその設定が尊重されます。

```bash
ccgate claude metrics                 # 直近 7 日間、TTY テーブル
ccgate claude metrics --days 30       # 集計範囲を拡張
ccgate claude metrics --json          # JSON 出力 (機械可読)
ccgate claude metrics --details 5     # 上位 5 件の fallthrough / deny コマンドを表示
ccgate claude metrics --details 0     # ドリルダウン節を非表示
ccgate codex  metrics --days 7        # codex 側、同じシェイプ
```

日次テーブルには Allow / Deny / Fall / F.Allow / F.Deny / Err、自動化率、平均レイテンシ、トークン使用量が並びます。「Top fallthrough commands」「Top deny commands」のドリルダウンを見ると、ルール追加で削減できる操作が特定できます。

## 既知の制約

- **Plan mode の正しさはプロンプトのみに依存 (Claude のみ)。** `permission_mode == "plan"` では、(a) 実装系 write を拒絶する判定と (b) allow guidance に載っていない read-only クエリを許可する判定の両方を、LLM とシステムプロンプトの指示文に委ねています。プロンプトで記述する以上、どちらの方向にも誤判定の余地があります。[#37](https://github.com/tak848/ccgate/issues/37) で追跡しています。
- **設定ファイル layering の非対称。** グローバル設定は組み込みデフォルトを*置換*するのに対し、プロジェクトローカルは*追加のみ*。プロジェクト層からルールを狭める/上書きする手段がありません。互換性を壊す破壊的リファクタとして [#38](https://github.com/tak848/ccgate/issues/38) で追跡しています。
- **Codex hook は upstream で experimental。** スキーマや挙動が変わる可能性があります。richer なフィールド (`permission_mode`、`recent_transcript` 解析、`~/.codex/config.toml` 取り込み、MCP server 単位の trust hint) は follow-up issue で追跡しています。
- **Codex hook の Windows は未検証。** ccgate の Codex 対応は Linux/macOS でのみ動作確認しています。OpenAI Codex hooks docs には `windows_managed_dir` が一級フィールドとして記載されているため、binary レベルでは block しませんが、ccgate の Codex flow が Windows で動くかは保証していません。

## ドキュメント

- [docs/ja/claude.md](claude.md) — Claude Code 固有
- [docs/ja/codex.md](codex.md) — Codex CLI 固有
- [docs/ja/configuration.md](configuration.md) — 設定 layering、fallthrough_strategy、metrics、既知の制約
- [English documentation (docs/)](../claude.md)

## 開発

```bash
mise run build    # バイナリビルド
mise run test     # テスト実行
mise run vet      # go vet
mise run schema   # schemas/{claude,codex}.schema.json を再生成
```

## ライセンス

MIT
