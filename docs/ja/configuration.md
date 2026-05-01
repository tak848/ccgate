# ccgate -- Configuration

[English version (docs/configuration.md)](../configuration.md)

target 横断の設定リファレンス。[ルート README (docs/ja/README.md)](README.md) は field 一覧とクイックスタートを扱います。本ページは layering ルール、fallthrough の決定木、メトリクス出力スキーマを掘り下げます。

## ccgate が config を探す場所

ccgate は target ごとに 3 layer を順に評価します。すべての layer は同じ merge セマンティクスで合成されます (後述「layer の合成ルール」参照):

1. **埋込デフォルト**: バイナリに同梱。常にベースとして適用。`ccgate <target> init` で確認可能
2. **グローバル設定**: 存在すれば埋込デフォルトの上に重ねる:
   - Claude Code: `~/.claude/ccgate.jsonnet`
   - Codex CLI:   `~/.codex/ccgate.jsonnet`
3. **プロジェクトローカル**: (1)+(2) の上に重ねる。tracked file は無視される (後述「tracked file が無視される理由」):
   - Claude Code: `{repo_root}/.claude/ccgate.local.jsonnet`
   - Codex CLI:   `{repo_root}/.codex/ccgate.local.jsonnet`

`{repo_root}` は git repo root で、hook の `cwd` から `git rev-parse --show-toplevel` で解決します。git repo 外では `cwd` 自体が使われます。


### layer の合成ルール

| field 群 | merge 動作 | 例 |
|---|---|---|
| list: `allow` / `deny` / `environment` | 値を設定した layer が前の layer から引き継いだ list を **置き換える** (`[]` でも置換)。設定していない layer は前の値を保持 | embedded `allow: ["A","B"]` + global `allow: ["X"]` → 最終 `allow: ["X"]` |
| list: `append_allow` / `append_deny` / `append_environment` | 値を設定した layer が前の layer の累積 list の **末尾に追加** | embedded `deny: ["A"]` + project `append_deny: ["P"]` → 最終 `deny: ["A","P"]` |
| スカラー: `log_*` / `metrics_*` / `fallthrough_strategy` | 各 layer が値を設定していれば per-field で **overwrite**、設定していなければ前の値を保持 | embedded `log_max_size: 5MB` + global `log_max_size: 10MB` → 最終 `log_max_size: 10MB` |
| ブロック: `provider` (`name` / `model` / `base_url` / `timeout_ms`) | `provider` を書いた layer は **block 全体を置換**、書かなかった layer はそのまま継承。per-field merge にすると、下位 layer の proxy 用 `base_url` が `name` を切り替えただけの上位 layer に残る等の不整合が起きるため | embedded `provider: {name: anthropic, model: haiku}` + global `provider: {name: openai, model: gpt-5.4-nano-2026-03-17}` → 最終 `provider: {name: openai, model: gpt-5.4-nano-2026-03-17}`。model だけ変えたい場合は `provider: {name: anthropic, model: claude-sonnet-4-6}` のように block 全体を書き直す |

`allow` と `append_allow` (他 list も同じ) は同じ layer に共存可能 — 先に置換、その結果に対して append が積まれる。embedded の list を厳選版に **差し替えつつ** プロジェクト固有のルールを **追加** したいときに使います: `{ allow: ['only this base'], append_allow: ['plus this project rule'] }`。

> v0.6 以前の ccgate はグローバル設定が存在すると埋込デフォルトをスキップしていました (グローバル層が「置換」していた)。v0.6 では embedded を常にベースとして適用しつつ、明示的な opt-in 拡張として `append_*` を導入しています。詳細は [#38](https://github.com/tak848/ccgate/issues/38) を参照。v0.6 以前のグローバル設定 (もともと `allow:` / `deny:` で完全置換していた) は無編集で同じ挙動になります。v0.6 以前のプロジェクトローカル設定で `allow:` / `deny:` / `environment:` を **追加** 目的で使っていた人だけ、`append_allow:` / `append_deny:` / `append_environment:` への rename が必要です (そのままだと累積 list を完全置換してしまいます)。

### tracked file が無視される理由

プロジェクトローカル設定は意図的に **git で tracked されていない場合のみ load** します。これは「個人 contributor が共有ベースラインの上に自分の制限を重ねる」用途を想定しているためで、ローカル設定経由でチーム全体ポリシーを repo に密かに混入させない狙いです。

repo 全体に効くポリシーが必要なら、自前 fork の埋込デフォルトに含める / チームで `~/.claude/ccgate.jsonnet` を dotfiles bootstrap で配布する / 個別に各 contributor が `.local.jsonnet` を作る、いずれかを選んでください。

## `fallthrough_strategy` -- LLM 判定迷い時の挙動

LLM は `allow` / `deny` / `fallthrough` のいずれかを返します。`fallthrough` は LLM が「自信を持って判定できないので、上流ツールの確認 prompt に委ねる」という意思表示です。対話セッションでは妥当 (ユーザーが「許可」を押す) ですが、無人実行 (スケジューラ・ボット・autonomous loop) では「許可」を押す人がいないので処理が止まります。

`fallthrough_strategy` は ccgate が LLM の `fallthrough` をどう resolve するかを決めます:

| 値        | 挙動                                                                                                  | 選ぶ場面                                                                          |
|-----------|-------------------------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------|
| `ask`     | デフォルト。上流ツール (Claude Code / Codex) の確認 prompt にそのまま流す                              | 対話セッション                                                                     |
| `deny`    | 自動拒否。deny メッセージが「user に聞くな、別コマンドで回避するな」と AI に指示する                    | 無人実行で「許可待ちで止まる」より「失敗で抜ける」を選びたいとき                    |
| `allow`   | 自動許可                                                                                              | 完全自律実行で「LLM が迷ったケースも進めたい」リスクを受容できるとき                |

**`allow` は見た目より危険です**。Claude Code / Codex とも、hook 仕様上 `decision.message` は `behavior=deny` のときしか AI に届きません。強制 allow のメッセージは silent に drop されるので、AI には「ccgate が auto approve した、注意して進めて」のような警告が見えません。このトレードオフを理解した上で選択してください。

### `fallthrough_strategy` の対象**外**

対象は LLM 判定の `fallthrough` のみ。runtime mode の fallthrough は strategy に関わらず上流ツールへ deferred されます:

- API 応答が truncate / refused された (`api_unusable`)
- API キー未設定 (`no_apikey`)
- `provider.name` が `anthropic` / `openai` / `gemini` のいずれでもない (`unknown_provider`)
- Claude `permission_mode == "bypassPermissions"` または `"dontAsk"`
- Claude `tool_name` が `{ExitPlanMode, AskUserQuestion}` (ユーザーインタラクション専用 tool)

これは意図的: `allow` は「LLM が躊躇したら自律実行を進める」用途であり、「LLM が判定すらしてないリクエストを silent に通す」用途ではありません。

各 strategy がどれだけ発火したかは metrics 出力で監査可能 (後述)。`forced_allow` / `forced_deny` 列が、まさに `fallthrough_strategy` が LLM `fallthrough` を allow/deny に flip したケース数です。

## メトリクス出力

呼び出しごとに `$XDG_STATE_HOME/ccgate/<target>/metrics.jsonl` に JSON 1 行を append (size でローテート)。`ccgate <target> metrics` がファイルを集計し、TTY テーブル or JSON ドキュメントを出力します。

### CLI

```bash
ccgate claude metrics                  # 直近 7 日、TTY テーブル
ccgate claude metrics --days 30        # 集計範囲拡張
ccgate claude metrics --json           # JSON 出力 (機械可読)
ccgate claude metrics --details 5      # 上位 5 件の fallthrough / deny コマンド
ccgate claude metrics --details 0      # ドリルダウン節を非表示
ccgate codex  metrics --days 7         # codex 側も同 shape
```

### 日次テーブル列

| 列          | 意味                                                                                                       |
|-------------|------------------------------------------------------------------------------------------------------------|
| `Date`      | ローカルタイムゾーンでの日境界                                                                              |
| `Total`     | 当日にカウントされた呼び出し数。`ExitPlanMode` / `AskUserQuestion` は除外                                    |
| `Allow`     | `allow` 結果 (LLM 明確判定 + 強制 allow)                                                                    |
| `Deny`      | `deny` 結果 (LLM 明確判定 + 強制 deny)                                                                      |
| `Fall`      | `fallthrough` 結果 (allow/deny に promote されなかったもの)                                                  |
| `F.Allow`   | `Allow` のうち `fallthrough_strategy=allow` で LLM `fallthrough` から promote されたもの                    |
| `F.Deny`    | 同様 `fallthrough_strategy=deny` で promote されたもの                                                      |
| `Err`       | エラー終了した呼び出し数 (parse 失敗 / panic / `Unusable` で扱われない API 失敗)                            |
| `Auto%`     | `(Allow + Deny) / Total`。高いほど上流 prompt に頼らずに ccgate で resolve できている                        |
| `Avg(ms)`   | 平均所要時間 (`DecidePermission` を囲む wall-clock)                                                          |
| `Tokens`    | Anthropic API レポートの input / output トークン日次合計                                                     |

### JSON エントリスキーマ (1 呼び出し = 1 行)

```json
{
  "ts": "2026-04-26T12:34:56.789Z",
  "sid": "session-abc",
  "tool": "Bash",
  "perm_mode": "default",
  "decision": "allow",
  "ft_kind": "",
  "forced": false,
  "reason": "Read-only inspection inside repo; matches allow guidance.",
  "deny_msg": "",
  "model": "claude-haiku-4-5",
  "in_tok": 4321,
  "out_tok": 87,
  "elapsed_ms": 612,
  "error": "",
  "tool_input": {
    "command": "ls -la"
  }
}
```

`ft_kind` は LLM (またはランタイム) が fallthrough を返したときに埋まり、どの fallback path が発火したかを示します (`llm`, `api_unusable`, `no_apikey`, `unknown_provider`, `bypass`, `dontask`, `user_interaction`)。`forced=true` は `fallthrough_strategy` が LLM `fallthrough` を `decision` に promote したことを意味します。

### ドリルダウン節

`ccgate <target> metrics` はデフォルトで 2 つのセクションを追加します:

- **Top fallthrough commands**: LLM が判断に迷った頻度上位の操作。プロジェクトローカルで allow / deny ルールを追加すれば LLM 往復を skip できる候補
- **Top deny commands**: LLM が deny した頻度上位の操作。同じブロックされた操作を自動 job が繰り返してる場合、AI 側のプラン形を変えるべきサインであることが多い

`--details 0` で両セクションを非表示、`--details N` で各上位 N 行に制限。

### 無効化・リダイレクト・ローテート

```jsonnet
{
  // メトリクスファイルを移動
  metrics_path: '~/my-state/ccgate-claude-metrics.jsonl',
  // メトリクスを完全無効化
  // metrics_disabled: true,
  // ローテート閾値デフォルト: 2MB
  // metrics_max_size: 5 * 1024 * 1024,
}
```

ログ側にも同じ field があります (`log_path`, `log_disabled`, `log_max_size`, デフォルト 5MB)。すべての `_max_size` field は `0` を「ローテートしない」として扱います。

## 既知の制約

- **Plan mode (Claude のみ) はプロンプト依存**: `permission_mode == "plan"` では (a) 実装系 write を拒絶する判定と (b) 明示的な allow guidance なしの read-only クエリ許可 を、LLM とシステムプロンプトの指示文に委ねています。どちらの方向にも誤判定の余地あり。[#37](https://github.com/tak848/ccgate/issues/37) で追跡
- **embedded default の特定ルールだけを部分削除する手段なし**: layer は list を **完全置換** (`allow: [...]`) するか **末尾追加** (`append_allow: [...]`) するかのどちらかで、embedded の中の 1 ルールだけ消したい場合は残り全部を `allow:` / `deny:` に書き直すしかない
- **Codex hook は upstream で experimental**: hook schema が予告なく変更される可能性あり
- **Codex `~/.codex/config.toml` 取り込み未実装** (`approval_policy`, `sandbox_mode`, `prefix_rules`): ccgate は hook payload + ccgate config だけで判定するため、Codex 自身の設定が拒絶するはずだった操作のシグナルは LLM に届かない (現状)
