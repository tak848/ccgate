# ccgate

[![CI](https://github.com/tak848/ccgate/actions/workflows/ci.yml/badge.svg)](https://github.com/tak848/ccgate/actions/workflows/ci.yml)
[![release](https://github.com/tak848/ccgate/actions/workflows/release.yml/badge.svg)](https://github.com/tak848/ccgate/releases)

AI コーディングツール向けの **PermissionRequest** フックです。ツール実行の許可判定を LLM (Claude Haiku) に委任し、設定ファイルに記述したルールに基づいて allow / deny / fallthrough を返します。

対応ターゲット:

- **[Claude Code](https://docs.anthropic.com/en/docs/claude-code)** — 安定
- **[OpenAI Codex CLI](https://developers.openai.com/codex/hooks)** — experimental

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

> top-level の `ccgate init` / `ccgate metrics` は実 subcommand ではなく、per-target 形式への 1 行案内を出して exit `2` します。bare `ccgate` (hook 起動) は別経路で、上述の通り動作します。

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

> Codex hooks は upstream で experimental 扱いです。スキーマや挙動が今後変わる可能性があります。

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
| 1 | 組み込みデフォルト (常にベースとして適用) | 同じ |
| 2 | `~/.claude/ccgate.jsonnet` — グローバル (上に重ねる) | `~/.codex/ccgate.jsonnet` — グローバル (同じ) |
| 3 | `{repo_root}/.claude/ccgate.local.jsonnet` — プロジェクトローカル (Git 未追跡のみ、上に重ねる) | `{repo_root}/.codex/ccgate.local.jsonnet` — プロジェクトローカル (同じ) |

3 つの layer はすべて同じ merge ルールで合成されます:

- **list**: `allow` / `deny` / `environment` は値を設定した layer が前の layer から引き継いだ list を **置き換える** (`[]` を書けば空 list に置き換え)。`append_*` 系 (`append_allow` / `append_deny` / `append_environment`) は前の layer の累積 list の **末尾に追加** する。
- **スカラー**: `provider.*` / `log_*` / `metrics_*` / `fallthrough_strategy` はその layer がフィールドを設定していれば per-field で上書き、設定していなければ前の値を保持。

`~/.<target>/ccgate.jsonnet` で `provider.model` だけ書き換えれば embedded の `allow` / `deny` はそのまま残ります。`allow: [...]` を書けば embedded の allow を完全に差し替え (これは v0.6 以前のグローバル設定がすでに行っていた挙動なので、そのまま冪等)。プロジェクトローカル設定は典型的に `append_deny: [...]` / `append_environment: [...]` で追加制限を載せます。
プロジェクトローカル設定は **Git に追跡されていないファイルのみ** 読み込まれます。


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
| `allow`                  | string[]                          | `[]`                                                                            | 許可ルール。設定すると前の layer から引き継いだ list を **完全置換**                                       |
| `deny`                   | string[]                          | `[]`                                                                            | 拒否ルール (mandatory)。`deny_message:` ヒント対応。`allow` と同じく置換                                   |
| `environment`            | string[]                          | `[]`                                                                            | LLM に渡すコンテキスト (信頼レベル、ポリシー等)。`allow` と同じく置換                                       |
| `append_allow`           | string[]                          | `[]`                                                                            | 引き継いだ list の末尾に **追加**。プロジェクトローカル設定で典型的に使用                                  |
| `append_deny`            | string[]                          | `[]`                                                                            | 引き継いだ deny list の末尾に追加                                                                          |
| `append_environment`     | string[]                          | `[]`                                                                            | 引き継いだ environment list の末尾に追加                                                                   |

`<target>` は Claude / Codex どちらの hook が呼ばれたかで `claude` / `codex` になります。`XDG_STATE_HOME` が未設定の場合は `~/.local/state/ccgate/<target>/...` が fallback として使われます。

## デフォルトルール

ccgate は target ごとに組み込みのデフォルトルールを持っています。常にベースとして適用され、その上にグローバル / プロジェクトローカル設定が重なります。

**許可:** 読み取り専用操作、ローカル開発コマンド (project script 経由の build / test)、git フィーチャーブランチ操作、リポジトリ内に閉じたパッケージインストール。

**拒否:** リモートコードのダウンロード実行 (`curl|bash`)、direct one-shot remote package execution (`npx`/`pnpx`/`bunx` 等)、git 破壊的操作 (protected branch 含む)、リポジトリ外の削除、特権昇格。

`ccgate claude init` / `ccgate codex init` でデフォルト設定の全容を確認できます。`init` の出力は **embedded defaults そのもの** = リファレンス文書であって、コピペして使う出発点ではありません。自分のオーバーライドは追加 / 上書きしたい分だけを書く最小限の jsonnet にしてください:

```bash
ccgate claude init           | less                   # Claude embedded defaults を確認
ccgate codex  init           | less                   # Codex も同じ
ccgate claude init -p > .claude/ccgate.local.jsonnet  # プロジェクトローカルのスケルトン
ccgate codex  init -p > .codex/ccgate.local.jsonnet   # Codex も同じ
```

embedded のルールを **削除** したい場合は明示的な reset/override 構文が必要ですが、現状そのような仕組みはありません。ルールと動機を Issue に書いてもらえれば検討します。

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

ログ・メトリクスは `$XDG_STATE_HOME/ccgate/<target>/` 配下 (`XDG_STATE_HOME` 未設定時は `~/.local/state/ccgate/<target>/`) に保存されます:

- `$XDG_STATE_HOME/ccgate/claude/{ccgate.log,metrics.jsonl}` — Claude Code
- `$XDG_STATE_HOME/ccgate/codex/{ccgate.log,metrics.jsonl}` — Codex CLI

両ファイルともサイズベースでローテーションします (`.log.1`, `.jsonl.1`)。

jsonnet で `log_path` / `metrics_path` を明示している場合はその設定が尊重されます。

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
- **embedded default の特定ルールだけを部分削除する手段なし。** layer は list を **完全置換** (`allow: [...]`) するか **末尾追加** (`append_allow: [...]`) するかのどちらかです。embedded の中の 1 ルールだけ消したい場合は、その 1 件を除いた残り全部を `allow:` / `deny:` に書き直すしかありません。
- **Codex hook は upstream で experimental。** スキーマや挙動が変わる可能性があります。ccgate は現在 Codex 側の `permission_mode` を expose せず、transcript JSONL を parse せず、`~/.codex/config.toml` も取り込まず、MCP server 単位の trust hint も適用しません。判定は `tool_name` + `tool_input` + `cwd` のみで行います。

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
