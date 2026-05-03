# ccgate

[![CI](https://github.com/tak848/ccgate/actions/workflows/ci.yml/badge.svg)](https://github.com/tak848/ccgate/actions/workflows/ci.yml)
[![release](https://github.com/tak848/ccgate/actions/workflows/release.yml/badge.svg)](https://github.com/tak848/ccgate/releases)

AI コーディングツール向けの **PermissionRequest** フックです。ツール実行の許可判定を LLM (Claude Haiku) に委任し、設定ファイルに記述したルールに基づいて allow / deny / fallthrough を返します。

![ccgate の動作例: 安全な `echo` は allow、`curl ... | bash` は deny_message 付きで deny](../images/gate.png)

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

選択した provider の API キーを設定してください。`CCGATE_*_API_KEY` が優先され bare 変数を上書きするので、AI ツール本体の API キーと ccgate 用キーを分離できます。

| `provider.name` | 優先                       | フォールバック        | API キー発行ページ |
|-----------------|----------------------------|-----------------------|--------------------|
| `anthropic`     | `CCGATE_ANTHROPIC_API_KEY` | `ANTHROPIC_API_KEY`   | <https://platform.claude.com/settings/keys> |
| `openai`        | `CCGATE_OPENAI_API_KEY`    | `OPENAI_API_KEY`      | <https://platform.openai.com/api-keys>      |
| `gemini`        | `CCGATE_GEMINI_API_KEY`    | `GEMINI_API_KEY`      | <https://aistudio.google.com/app/api-keys>  |

OpenAI 互換 / Anthropic 互換 proxy (LiteLLM proxy, Azure OpenAI, オンプレ gateway 等) を経由したい場合は、`provider.base_url` を設定して対応する native provider を使います — 詳細は [互換 proxy 経由で叩く](#互換-proxy-経由で叩く) を参照。

## セットアップ — Codex CLI (experimental)

> Codex hooks は upstream で experimental 扱いです。スキーマや挙動が今後変わる可能性があります。

### 1. 設定ファイルを配置 (オプション)

```bash
ccgate codex init > ~/.codex/ccgate.jsonnet
```

defaults は Claude Code と同じ思想 (allow + deny + environment)。Codex hooks は Bash、`apply_patch`、MCP tool 呼び出しなど複数の tool surface で発火し、ccgate のルールは全 surface を対象にしています。system prompt は LLM に「`tool_name` + `tool_input` の JSON 全体を見て分類せよ」と指示します。

### 2. Codex hook として登録

Codex は `~/.codex/hooks.json` と `~/.codex/config.toml` から hook を読み込みます (project が trusted なら `<repo>/.codex/{hooks.json,config.toml}` も overlay)。好きな方で登録してください。

`~/.codex/hooks.json`:

```json
{
  "hooks": {
    "PermissionRequest": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "ccgate codex",
            "statusMessage": "ccgate evaluating request"
          }
        ]
      }
    ]
  }
}
```

`~/.codex/config.toml`:

```toml
[features]
codex_hooks = true   # 必須: Codex hooks は experimental で、この feature flag で gate されている

[[hooks.PermissionRequest]]
matcher = ""

[[hooks.PermissionRequest.hooks]]
type    = "command"
command = "ccgate codex"
statusMessage = "ccgate evaluating request"
```

lookup 順序、project-local overlay、in-tree dev build 用の `go run` レシピは [docs/codex.md](../codex.md) を参照。upstream の [Codex hooks ドキュメント](https://developers.openai.com/codex/hooks) が schema の正本です。

### 3. API キー

Claude Code と同じ環境変数を使います — [provider table](#3-api-キー) を参照してください。

## 設定

### 設定ファイルの読み込み順序 (target ごと)

| 順序 | Claude Code | Codex CLI |
|----:|-------------|-----------|
| 1 | 組み込みデフォルト (常にベースとして適用) | 同じ |
| 2 | `~/.claude/ccgate.jsonnet` — グローバル (上に重ねる) | `~/.codex/ccgate.jsonnet` — グローバル (同じ) |
| 3 | `{repo_root}/.claude/ccgate.local.jsonnet` — プロジェクトローカル (Git 未追跡のみ、上に重ねる) | `{repo_root}/.codex/ccgate.local.jsonnet` — プロジェクトローカル (同じ) |

3 つの layer はすべて同じ merge ルールで合成されます:

- **list**: `allow` / `deny` / `environment` は値を設定した layer が前の layer から引き継いだ list を **置き換える** (`[]` を書けば空 list に置き換え)。`append_*` 系 (`append_allow` / `append_deny` / `append_environment`) は前の layer の累積 list の **末尾に追加** する。
- **スカラー**: `log_*` / `metrics_*` / `fallthrough_strategy` はその layer がフィールドを設定していれば per-field で上書き、設定していなければ前の値を保持。
- **`provider` block**: `provider` を書いた layer は block 全体 (`name` + `model` + `base_url` + `api_key_command` + `api_key_file` + `api_key_refresh_margin` + `api_key_command_timeout` + `timeout_ms`、つまり全 `provider.*` field) を **丸ごと置換**。書かなかった layer は前の block をそのまま継承。`name` を切り替えると `model` の名前空間も `base_url` も意味が変わる密結合なので、per-field merge にすると下位 layer の値が残って壊れるため block 単位で扱う。注意: project-local 設定で `provider` を再掲する場合、global layer に書いた `api_key_command` / `api_key_file` も忘れずに書き写すこと。書き漏らすと当該プロジェクトでだけ helper 設定が静かに消える。

`~/.<target>/ccgate.jsonnet` で model だけ変えたい場合でも `provider: {name: 'anthropic', model: 'claude-sonnet-4-6'}` のように block 全体を書き直す必要があります (embedded の `allow` / `deny` はそのまま残ります)。`allow: [...]` を書けば embedded の allow を完全に差し替え (これは v0.6 以前のグローバル設定がすでに行っていた挙動なので、そのまま冪等)。プロジェクトローカル設定は典型的に `append_deny: [...]` / `append_environment: [...]` で追加制限を載せます。
プロジェクトローカル設定は **Git に追跡されていないファイルのみ** 読み込まれます。


### 設定項目

| フィールド               | 型                                | デフォルト                                                                       | 説明                                                                                                       |
|--------------------------|-----------------------------------|---------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------|
| `provider.name`          | string                            | `"anthropic"`                                                                   | プロバイダー名。`"anthropic"` / `"openai"` / `"gemini"` のいずれか                                          |
| `provider.model`         | string                            | `"claude-haiku-4-5"`                                                            | モデル名。例: `claude-haiku-4-5` / `claude-sonnet-4-6` (anthropic)、`gpt-5.4-nano-2026-03-17` (openai)、`gemini-3-flash-preview` (gemini)。互換 proxy 経由なら proxy が公開している任意の名前 (例: `anthropic/claude-haiku-4-5`) |
| `provider.base_url`      | string                            | `""`                                                                            | API base URL の上書き。空文字列 (default) で SDK の既定 endpoint を使用。OpenAI 互換 / Anthropic 互換 proxy (LiteLLM proxy, Azure OpenAI, オンプレ gateway, 地域別 endpoint 等) 経由で叩きたい時に指定 |
| `provider.api_key_command` | string                          | `""`                                                                            | Unix 限定。stdout に API キーを出すシェルコマンド (`/bin/sh -c`)。JSON `{key, expires_at}` なら disk cache + 早期 refresh、plain stdout は cache なし。[短命 / ローテーションする API キー](#短命--ローテーションする-api-キー) 参照 |
| `provider.api_key_file`  | string                            | `""`                                                                            | Unix 限定。abs path or `~/...` のファイルを毎 fire 読む。`api_key_command` と同じ shape を期待。`api_key_command` が空のときのみ参照 |
| `provider.api_key_refresh_margin` | duration                 | `"30s"`                                                                         | cache 有効性判定の早期 refresh 余裕。`now + margin >= expires_at` で stale 扱い。`>= 0` (`0s` で早期 refresh 無効) |
| `provider.api_key_command_timeout` | duration                | `"5s"`                                                                          | helper 1 回起動の hot-path 上限 (lock retry + exec)。`> 0` (`0s` は reject)                                |
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

### OpenAI / Gemini に切り替える

任意の layer で `provider.name`（必要に応じて `provider.model` も）を書き換えるだけです:

```jsonnet
{
  provider: {
    name: 'openai',
    model: 'gpt-5.4-nano-2026-03-17',
  },
}
```

対応する API キー (`CCGATE_OPENAI_API_KEY` / `CCGATE_GEMINI_API_KEY` — [provider table](#3-api-キー)) を export してください。キーが見つからない場合 ccgate は上流ツールの確認画面に fallthrough するため、provider 切替で hook が壊れることはありません。

> **reasoning model は避ける** (`gpt-5`, `gpt-5-mini`, `gpt-5-nano`, `gpt-5-chat`, `o1*`, `o3*`, `o4-mini`): `temperature=0` を拒否するため全リクエストが失敗し、分類タスクには不要な chain-of-thought に数秒かかります。`gpt-4.1-nano` / `gpt-4o-mini` / `gpt-5.4-nano-2026-03-17` を推奨。

### 互換 proxy 経由で叩く

ccgate は Anthropic / OpenAI クライアントが使うのと同じ chat completions API を喋るので、**OpenAI 互換 / Anthropic 互換**の任意の endpoint — [LiteLLM proxy](https://docs.litellm.ai/docs/proxy/quick_start), Azure OpenAI, オンプレ gateway, 地域別 endpoint など — に対して動きます。proxy が話すプロトコルに合わせて `provider.base_url` を設定します。

`provider.base_url` は underlying SDK の `WithBaseURL` にそのまま渡されるので、書く path は **その SDK の慣習**に従います (ccgate 側で正規化しません):

| `provider.name` | underlying SDK | default base URL                  | `base_url` に書く形              |
|-----------------|----------------|-----------------------------------|----------------------------------|
| `openai`        | `openai-go`    | `https://api.openai.com/v1/`      | host **+ `/v1`** (SDK が `chat/completions` を追加) |
| `anthropic`     | `anthropic-sdk-go` | `https://api.anthropic.com/` | host root のみ (SDK が `/v1/messages` を追加) |
| `gemini`        | Gemini の OpenAI 互換 endpoint 経由で `openai-go` | `https://generativelanguage.googleapis.com/v1beta/openai/` | override する場合は host **+ `/v1beta/openai`** |

**OpenAI 互換 endpoint** (LiteLLM proxy の `/v1/chat/completions` 等):

```jsonnet
{
  provider: {
    name: 'openai',
    model: 'anthropic/claude-haiku-4-5', // proxy が公開している名前
    base_url: 'https://your-proxy.example/v1',
  },
}
```

proxy の API キーを `CCGATE_OPENAI_API_KEY` で export。OpenAI SDK は base URL に `/chat/completions` を直接 append するので、末尾の `/v1` が必要。

**Anthropic 互換 endpoint** (LiteLLM proxy の `/v1/messages` 等):

```jsonnet
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    base_url: 'https://your-proxy.example',
  },
}
```

proxy の API キーを `CCGATE_ANTHROPIC_API_KEY` で export。Anthropic SDK が `/v1/messages` を自分で append するので、base URL は host root で止めます。

### 短命 / ローテーションする API キー

静的 env var では追いつかない credential — AWS STS セッション / Vertex ADC / OpenAI 互換 gateway の virtual key / 社内 key broker など — が相手のときは、ccgate にプロセスかファイルを指してもらいます。

**Unix のみ** (Linux / macOS / *BSD)。Windows では `kind=credential_unavailable` / `reason=unsupported_platform` で fallthrough し、env var 経路はそのまま動き続けます。

#### 出力フォーマット

helper は stdout (もしくはファイル) に次のいずれかの形を書きます:

- JSON: `{"key":"sk-...","expires_at":"2026-05-02T01:23:45Z"}`。strict parse。`expires_at` が未来の場合は `$XDG_CACHE_HOME/ccgate/<target>/api_key.<hash>.json` (mode `0600`) に memoize し、`api_key_refresh_margin` で早めに refresh
- plain text: 単一行の非空文字列。そのまま渡される (**cache しない**)。低頻度な `gcloud auth print-access-token` 等には十分だが、tool 起動が頻繁な hot path では毎回 helper を exec する hot-path コストになる

`expires_at` は RFC3339。optional な `version` 未指定は `1` 扱い (将来 schema 変更時の互換用予約)。stdout 64KiB 超えは `output_too_large` で reject。

#### 設定

```jsonnet
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    api_key_command: '/usr/local/bin/my-key-broker --provider anthropic',
    api_key_refresh_margin: '60s', // optional, default 30s
    api_key_command_timeout: '5s', // optional, default 5s
  },
}
```

外部 rotator (cron / launchd / systemd timer) がファイル更新する運用ならファイルを指す形でも OK。この場合 ccgate 自身は何も exec しない:

```jsonnet
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    api_key_file: '~/.config/my-broker/anthropic.json',
  },
}
```

解決順序: `api_key_command` > `api_key_file` > `CCGATE_*_API_KEY` > `*_API_KEY`。helper / file が設定済みの状態で失敗したら ccgate は env var に **fallback しない** (silent fallback は helper のバグを隠す)。代わりに `kind=credential_unavailable` で fallthrough し、reason がどの段階で失敗したかを示します (`ccgate <target> metrics` 参照)。

#### credential を安全に持ちまわるコツ

- `api_key_file` の permission は user 側で設定: `chmod 0600 <ファイル>`、親ディレクトリは `chmod 0700`。ccgate は読み取るだけで mode の正規化はしない
- `api_key_command` に literal secret を直書きしない。command 文字列は `/bin/sh -c` に渡されるので、`ps` / `/proc/<pid>/cmdline` / audit log / shell history に丸見え。secret はファイルや keychain に置き、helper の内部で読む形にすること
- ccgate のログファイル (`$XDG_STATE_HOME/ccgate/<target>/ccgate.log`) は `0644` で書かれる。helper 失敗時、ccgate は exit error と stderr のバイト数のみログに残し、**stderr 本文は書き出さない** ので helper 内部の `set -x` 事故で token が漏れたりしない。stderr の内容を見たいときは ccgate のログを覗くのではなく、helper を `2>&1` 付きで手動実行すること

helper / file 由来の credential に対して provider 側が 401/403 を返した場合、cache を invalidate して次回 hook fire で fresh helper exec を強制します。同じ fire は (exit 1 ではなく) fallthrough として返るので、upstream tool の prompt がユーザーに表示されます。

#### helper の暗黙契約

helper はこれらを満たすこと:

- 非対話 (TTY 入力なし、ブラウザを開かない、stdin は close 状態で起動)
- daemonize しない (process group を抜ける fork は timeout-kill の対象外になる)
- stdout には credential **だけ** を書く (debug は stderr へ。ただし stderr に secret は書かない — ccgate は stderr をメモリ上限のために内部 capture するが本文は `ccgate.log` には書き出さない、log にはバイト数と exit error だけ残る)
- 同じ `(api_key_command, provider.name, base_url)` は同じ credential を返す決定論性
- plain string 形式は trim 後に単一行 + 非空であること。複数行は `invalid_plain_output` で reject

ccgate は helper の env に `CCGATE_API_KEY_RESOLUTION=1` を追加します。helper が ccgate を再帰呼び出しする構造で再帰検知に使えます。それ以外の env var (`*_API_KEY` 含む) は継承するので、既存 credential を wrap する helper はそのまま動きます。

#### AWS `credential_process` との差分

API shape は AWS の `credential_process` に意図的に近づけてあるので、既存 helper を 1 行 wrapper で流用しやすいです。ただし AWS CLI が毎回 helper を再 exec するのに対し、**ccgate は disk に memoize** します。hook は 1 セッションで何十回も fire するので hot-path 遅延を優先したトレードオフ。memoize されたくない broker の場合は `expires_at` を含めない JSON (`{"key":"..."}`) を返せば毎回再 exec されます。

#### 障害時の運用復旧 checklist

何かおかしいときは:

1. `ccgate.log` を tail して `kind=credential_unavailable` のエントリを探す。`reason` と `source` (`command` / `file` / `cache` / `lock`) attribute を見る
2. `ccgate <target> metrics` の **Credential failures** セクションで `(source, reason)` 別の集計を確認
3. cache 起因が疑わしければ `$XDG_CACHE_HOME/ccgate/<target>/api_key.*.json` を削除して再生成。隣接する `*.lock` は再利用されるので削除しないでよい
4. `expired` が出続けるなら helper の `expires_at` と `date -u` を比較。helper 内 TTL ロジックや時計ズレが原因
5. 単独再現は `/bin/sh -c "$your_command"` を実行して helper と同じ stdout が出るか確認

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
