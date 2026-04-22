# ccgate

[Claude Code](https://docs.anthropic.com/en/docs/claude-code) の **PermissionRequest** フックとして動作する Go バイナリです。
ツール実行の許可判定を LLM (Claude Haiku) に委任し、設定ファイルに記述したルールに基づいて allow / deny / fallthrough を返します。

## 仕組み

```
Claude Code (PermissionRequest hook)
  │
  │  stdin: HookInput JSON
  ▼
ccgate
  ├── 設定読み込み (~/.claude/ccgate.jsonnet)
  ├── コンテキスト構築 (git repo, worktree, paths, transcript)
  ├── Claude Haiku API 呼び出し (Structured Output)
  └── stdout: allow / deny / fallthrough
```

1. Claude Code がツール実行前に `ccgate` を呼び出す
2. `ccgate` は jsonnet 設定の allow/deny ルールをシステムプロンプトに組み込み、ツール情報・git コンテキスト・直近の会話履歴をユーザーメッセージとして Haiku に送信
3. Haiku の判定結果を Claude Code に返す

## インストール

### mise (推奨)

```bash
mise use -g go:github.com/tak848/ccgate
```

### go install

```bash
go install github.com/tak848/ccgate@latest
```

### GitHub Releases

[Releases](https://github.com/tak848/ccgate/releases) からバイナリをダウンロードし、PATH の通った場所に配置してください。

## セットアップ

### 1. 設定ファイルを配置 (オプション)

ccgate はデフォルトの安全ルールを内蔵しているため、設定ファイルなしでも動作します。

カスタマイズする場合はデフォルトを出力して編集:

```bash
ccgate init > ~/.claude/ccgate.jsonnet
```

[example/ccgate.jsonnet](../example/ccgate.jsonnet) も参考にしてください。
`$schema` フィールドでホストされた JSON Schema を参照しているため、エディタ補完が効きます。

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
            "command": "ccgate"
          }
        ]
      }
    ]
  }
}
```

### 3. API キー

環境変数 `CCGATE_ANTHROPIC_API_KEY` または `ANTHROPIC_API_KEY` を設定してください。

## 設定

### 設定ファイルの読み込み順序

1. **組み込みデフォルト** — 内蔵の安全ルール (グローバル設定がない場合のフォールバック)
2. `~/.claude/ccgate.jsonnet` — グローバル設定 (組み込みデフォルトを**完全に置換**)
3. `{repo_root}/ccgate.local.jsonnet` — プロジェクトローカル (Git 未追跡のみ、**追加**)
4. `{repo_root}/.claude/ccgate.local.jsonnet` — プロジェクトローカル (Git 未追跡のみ、**追加**)

グローバル設定が存在する場合、組み込みデフォルトは使用されません。
プロジェクトローカル設定は常にベースに追加されます (allow/deny/environment は追加、provider は上書き)。
プロジェクトローカル設定は **Git に追跡されていないファイルのみ** 読み込まれます。

> **Note:** 「グローバルはデフォルトを置換、プロジェクトローカルは追加のみ」という非対称仕様は既知の課題です (プロジェクト層からルールを狭める/上書きする手段がない)。互換性を壊す破壊的リファクタとして [#38](https://github.com/tak848/ccgate/issues/38) で追跡しています。

### 設定項目

| フィールド | 型 | デフォルト | 説明 |
|-----------|------|-----------|------|
| `provider.name` | string | `"anthropic"` | プロバイダー名。`"anthropic"` のみ対応 |
| `provider.model` | string | `"claude-haiku-4-5"` | モデル名 (例: `claude-haiku-4-5`, `claude-sonnet-4-6`, `claude-opus-4-6`) |
| `provider.timeout_ms` | int | `20000` | API タイムアウト (ms) |
| `log_path` | string | `"~/.claude/logs/ccgate.log"` | ログファイルパス。`~` でホームディレクトリ展開 |
| `log_disabled` | bool | `false` | ログ出力を完全に無効化 |
| `log_max_size` | int | `5242880` | ローテーション閾値 (bytes, デフォルト 5MB) |
| `metrics_path` | string | `"$XDG_STATE_HOME/ccgate/metrics.jsonl"` | メトリクス JSONL のパス。`~` でホームディレクトリ展開 |
| `metrics_disabled` | bool | `false` | メトリクス収集を完全に無効化 |
| `metrics_max_size` | int | `2097152` | ローテーション閾値 (bytes, デフォルト 2MB) |
| `fallthrough_strategy` | `"ask"` / `"allow"` / `"deny"` | `"ask"` | LLM が判定に迷った (`fallthrough`) 際の扱い。[完全自動運転モード](#完全自動運転モード-fallthrough_strategy) 参照 |
| `allow` | string[] | `[]` | 許可ルール (自然言語、LLM が解釈) |
| `deny` | string[] | `[]` | 拒否ルール (mandatory)。`deny_message:` ヒント対応 |
| `environment` | string[] | `[]` | LLM に渡すコンテキスト (信頼レベル、ポリシー等) |

## デフォルトルール

グローバル設定がない場合、ccgate は組み込みのデフォルトルールを使用します:

**許可:** 読み取り専用操作、ローカル開発コマンド、git フィーチャーブランチ操作、パッケージマネージャーのインストール。

**拒否:** リモートコードのダウンロード実行 (curl|bash)、直接ツール実行 (npx, pnpx 等)、git 破壊的操作、リポジトリ外の削除、ワークツリー混同。

`ccgate init` でデフォルト設定の全容を確認できます。カスタマイズする場合:

```bash
ccgate init > ~/.claude/ccgate.jsonnet    # グローバル設定 (デフォルトを置換)
ccgate init -p > ccgate.local.jsonnet     # プロジェクトローカルテンプレート (追加)
```

## 完全自動運転モード (`fallthrough_strategy`)

デフォルトでは、LLM が判定に自信を持てない場合 ccgate は `fallthrough` を返し、Claude Code のインタラクティブ確認画面にフォールバックします。対話セッションでは妥当ですが、スケジューラやボットなど人間が「許可」を押せない環境では処理が止まります。

`fallthrough_strategy` を設定すると、LLM の判定迷いを allow/deny に強制変換できます:

```jsonnet
{
  // 安全側: 迷ったら拒否。無人実行ではこちらを推奨
  fallthrough_strategy: 'deny',
}
```

値:

- `ask` (デフォルト) — Claude Code の確認画面に委ねる (既存の挙動)
- `deny` — 迷ったら自動拒否。deny メッセージには「user に聞くな、別コマンドで回避するな」という指示が含まれるため、実行が止まらず前に進む
- `allow` — 迷ったら自動許可。**危険側**: LLM 自身が判断に迷った操作を無条件に通すことになります。加えて Claude Code の hook 仕様上 `decision.message` は `deny` のときしか Claude に届かないため、強制 allow の際 Claude には警告が渡りません。このトレードオフを理解した上で選択してください

対象は **LLM 判定の fallthrough に限定** です。API 応答の打ち切り/拒否、API キー欠損、`bypassPermissions`/`dontAsk` モード、`ExitPlanMode` / `AskUserQuestion` はいずれも従来通り Claude Code へフォールスルーされます (LLM が呼ばれないケースを `fallthrough_strategy=allow` が黙って自動承認することはありません)。

強制発火した回数は `ccgate metrics` の `F.Allow` / `F.Deny` 列 (JSON では `forced_allow` / `forced_deny`) で確認できるため、選んだ戦略が妥当に機能しているか後から監査できます。

## ログ

デフォルトでは `~/.claude/logs/ccgate.log` に出力されます。5MB でローテーション (`.log.1`)。

ログパスの変更・無効化:

```jsonnet
{
  log_path: '~/my-logs/ccgate.log',
  // log_disabled: true,
}
```

## メトリクス

呼び出しごとに JSONL レコードを記録します (デフォルトは `$XDG_STATE_HOME/ccgate/metrics.jsonl`、2MB でローテーション)。`ExitPlanMode` と `AskUserQuestion` のエントリは監査用に生ログには残りますが、ccgate が評価せずにパススルーするため集計 (`automation_rate` / `Fall` / ツール別 Total / 上位セクション) からは除外されます。集計表示は:

```bash
ccgate metrics                     # 直近 7 日間、TTY テーブル
ccgate metrics --days 30           # 集計範囲を拡張
ccgate metrics --json              # JSON 出力 (機械可読)
ccgate metrics --details 5         # 上位 5 件の fallthrough / deny コマンドを表示
ccgate metrics --details 0         # ドリルダウン節を非表示
```

日次テーブルには Allow / Deny / Fall / F.Allow / F.Deny / Err、自動化率、平均レイテンシ、トークン使用量が並びます。「Top fallthrough commands」「Top deny commands」のドリルダウンを見ると、ルール追加で削減できる操作が特定できます。

メトリクスファイルの移動・無効化:

```jsonnet
{
  metrics_path: '~/my-state/ccgate-metrics.jsonl',
  // metrics_disabled: true,
}
```

## 既知の制約

- **Plan mode の正しさはプロンプトのみに依存。** `permission_mode == "plan"` では、(a) 実装系 write を拒絶する判定と (b) allow guidance に載っていない read-only クエリを許可する判定の両方を、LLM とシステムプロンプトの指示文に委ねています。プロンプトで記述する以上、どちらの方向にも誤判定の余地があり、「一見安全」な write が通ってしまうケース、および新規の read-only コマンドが fallthrough してしまうケースが残ります。[#37](https://github.com/tak848/ccgate/issues/37) で追跡しています。
- **設定ファイル layering の非対称。** `~/.claude/ccgate.jsonnet` は組み込みデフォルトを*置換*するのに対し、プロジェクトローカルは*追加のみ*。プロジェクト層からルールを狭める/上書きする手段がありません。互換性を壊す破壊的リファクタとして [#38](https://github.com/tak848/ccgate/issues/38) で追跡しています。

## 開発

```bash
mise run build    # バイナリビルド
mise run test     # テスト実行
mise run vet      # go vet
```

## ライセンス

MIT
