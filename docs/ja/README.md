# ccgate

[![CI](https://github.com/tak848/ccgate/actions/workflows/ci.yml/badge.svg)](https://github.com/tak848/ccgate/actions/workflows/ci.yml)
[![release](https://github.com/tak848/ccgate/actions/workflows/release.yml/badge.svg)](https://github.com/tak848/ccgate/releases)

AI コーディングツール向けの **PermissionRequest** フックです。ツール実行の許可判定を LLM (Claude Haiku) に委任し、jsonnet 設定に書いたルールに基づいて allow / deny / fallthrough を返します。ルールは **LLM への自然言語 guidance** であって、jsonnet による条件分岐ポリシーコードではありません。「何を許可し何を拒否したいか」を散文で書き、実際のリクエストの分類は LLM に任せる、という運用です。

ccgate は組み込みのデフォルトルールを持っているので、設定ファイルなしでも動きます。

![ccgate の動作例: 安全な `echo` は allow、`curl ... | bash` は deny_message 付きで deny](../images/gate.png)

対応ターゲット:

- **[Claude Code](https://docs.anthropic.com/en/docs/claude-code)**
- **[OpenAI Codex CLI](https://developers.openai.com/codex/hooks)**

[English README](../../README.md)

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

### Homebrew

```bash
brew install tak848/tap/ccgate
```

### go install

```bash
go install github.com/tak848/ccgate@latest
```

### GitHub Releases

[Releases](https://github.com/tak848/ccgate/releases) からバイナリをダウンロードし、PATH の通った場所に配置してください。

## クイックスタート — Claude Code

### 1. Claude Code の hooks に登録

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

### 2. API キー

選択した provider の API キーを設定してください。`CCGATE_*_API_KEY` が優先され bare 変数を上書きするので、AI ツール本体の API キーと ccgate 用キーを分離できます。

| `provider.name` | 優先                       | フォールバック        | API キー発行ページ |
|-----------------|----------------------------|-----------------------|--------------------|
| `anthropic`     | `CCGATE_ANTHROPIC_API_KEY` | `ANTHROPIC_API_KEY`   | <https://platform.claude.com/settings/keys> |
| `openai`        | `CCGATE_OPENAI_API_KEY`    | `OPENAI_API_KEY`      | <https://platform.openai.com/api-keys>      |
| `gemini`        | `CCGATE_GEMINI_API_KEY`    | `GEMINI_API_KEY`      | <https://aistudio.google.com/app/api-keys>  |

ここまでで ccgate は組み込みデフォルトで動き始めます。allow / deny を自分で書きたい場合は [ルールチューニング](#ルールチューニング) を、ルールの仕組みを先に押さえたい場合は [コンセプト](#コンセプト) を参照してください。

## クイックスタート — Codex CLI

> [!NOTE]
> Codex hooks 自体が upstream で experimental 扱いで、`[features] codex_hooks = true` flag 配下にあり、schema が今後変わる可能性があります。特定 field に依存する前に [Codex hooks docs](https://developers.openai.com/codex/hooks) を一次情報として確認してください。

### 1. Codex hook として登録

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
codex_hooks = true   # Codex hooks はこの feature flag 配下にあり、互換性のため明示しておく

[[hooks.PermissionRequest]]
matcher = ""

[[hooks.PermissionRequest.hooks]]
type    = "command"
command = "ccgate codex"
statusMessage = "ccgate evaluating request"
```

lookup 順序、project-local overlay、in-tree dev build 用の `go run` レシピは [docs/codex.md](codex.md) を参照。upstream の [Codex hooks ドキュメント](https://developers.openai.com/codex/hooks) が schema の正本です。

### 2. API キー

Claude Code と同じ環境変数を使います — [provider table](#2-api-キー) を参照してください。

ここまでで ccgate は組み込みデフォルトで動き始めます。allow / deny を自分で書きたい場合は [ルールチューニング](#ルールチューニング) を、ルールの仕組みを先に押さえたい場合は [コンセプト](#コンセプト) を参照してください。

## コンセプト

ccgate の `allow` / `deny` / `environment` はいずれも **自然言語の文字列リスト**です。これらが system prompt に埋め込まれて LLM に送られ、LLM が `allow` / `deny` / `fallthrough` のいずれかを返します。jsonnet 側で deterministic にマッチする engine ではなく、すべての PermissionRequest が LLM を経由する設計です。

評価フロー:

```
AI ツールが PermissionRequest を発火
  │
  │  stdin: HookInput JSON
  ▼
ccgate
  ├── jsonnet config を読み込む (embedded defaults + global + project-local)
  ├── context を組み立て (git repo info, referenced paths, recent transcript [Claude のみ])
  ├── 設定済みの LLM (default: Claude Haiku) を structured output で呼ぶ
  └── stdout: allow / deny / fallthrough
```

ccgate が LLM に渡す情報 (代表項目):

- `tool_name`, `tool_input`, `tool_input_raw` (元の JSON payload をそのまま渡す)。
- `cwd`, `repo_root`, `branch_name`, worktree info (`gitutil.Context` から)。working tree の dirty/clean は **渡していない**。
- `referenced_paths` — `tool_input` から best-effort で抽出した path リスト。対象 tool は `Read` / `Write` / `Edit` / `MultiEdit` / `Glob` / `Grep` / `Bash` のみ。`apply_patch` (Codex) や MCP tool では空で、LLM は `tool_input_raw` の hunk / args を直接読む。
- Claude のみ: `permission_mode` (`"plan"` で system prompt が plan mode rule に切替), `permission_suggestions`, `recent_transcript`, `settings_permissions` hint (whitelist ではなく hint 扱い)。

target ごとの完全な入力一覧は [docs/claude.md](claude.md) / [docs/codex.md](codex.md) を参照してください。

## 設定

### 設定ファイルの読み込み順序 (target ごと)

| 順序 | Claude Code | Codex CLI |
|----:|-------------|-----------|
| 1 | 組み込みデフォルト (常にベースとして適用) | 同じ |
| 2 | `~/.claude/ccgate.jsonnet` — グローバル (上に重ねる) | `~/.codex/ccgate.jsonnet` — グローバル |
| 3 | `{main_worktree}/.claude/ccgate.local.jsonnet` — main worktree プロジェクトローカル (Git 未追跡のみ、linked git worktree のときのみ) | `{main_worktree}/.codex/ccgate.local.jsonnet` |
| 4 | `{repo_root}/.claude/ccgate.local.jsonnet` — current worktree プロジェクトローカル (Git 未追跡のみ) | `{repo_root}/.codex/ccgate.local.jsonnet` |

各 layer はすべて同じ merge ルールで合成されます:

- **list**: `allow` / `deny` / `environment` は値を設定した layer が前の layer から引き継いだ list を **置き換える** (`[]` を書けば空 list に置き換え)。`append_*` 系 (`append_allow` / `append_deny` / `append_environment`) は前の layer の累積 list の **末尾に追加** する。
- **スカラー**: `log_*` / `metrics_*` / `fallthrough_strategy` はその layer がフィールドを設定していれば per-field で上書き、設定していなければ前の値を保持。
- **`provider` block**: `provider` を書いた layer では `provider.*` の全フィールド (`name` / `model` / `base_url` / `auth` / `timeout_ms`) をまとめて置き換える。書かなかった layer では前の block をそのまま引き継ぐ。`name` を切り替えると `model` の名前空間や `base_url` の意味も変わる密結合のため、フィールド単位では merge しない。project-local 設定で `provider` を再掲する場合、global layer の `auth` ブロックも忘れずに書き写すこと。書き漏らすと当該プロジェクトだけ helper 設定が静かに消える。

プロジェクトローカル設定は **Git に追跡されていないファイルのみ** 読み込まれます。

`disable_load_main_worktree_local_config: true` を (1) または (2) に書けば (3) をスキップします。この flag は (1) / (2) でのみ有効で、(3) / (4) に書いても無視されます。詳細は [docs/configuration.md](configuration.md#where-ccgate-looks-for-config) を参照。

### 設定項目

| フィールド               | 型                                | デフォルト                                                                       | 説明                                                                                                       |
|--------------------------|-----------------------------------|---------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------|
| `provider.name`          | string                            | `"anthropic"`                                                                   | プロバイダー名。`"anthropic"` / `"openai"` / `"gemini"` のいずれか                                          |
| `provider.model`         | string                            | `"claude-haiku-4-5"`                                                            | モデル名。例: `claude-haiku-4-5` / `claude-sonnet-4-6` (anthropic)、`gpt-5.4-nano-2026-03-17` (openai)、`gemini-3-flash-preview` (gemini)。互換 proxy 経由なら proxy が公開している任意の名前 (例: `anthropic/claude-haiku-4-5`) |
| `provider.base_url`      | string                            | `""`                                                                            | API base URL の上書き。空文字列 (default) で SDK の既定 endpoint を使用。OpenAI 互換 / Anthropic 互換 proxy 経由で叩きたい時に指定 |
| `provider.auth`          | object (`{type, ...}`)            | (省略時は env var)                                                              | 短命 / ローテーションする credential を扱う discriminated union。`type=exec` / `type=file` / `type=profile` の 3 系統。詳細は [api-key-helper.md](api-key-helper.md) |
| `provider.timeout_ms`    | int                               | `20000`                                                                         | API タイムアウト (ms)。`0` = タイムアウトなし                                                              |
| `log_path`               | string                            | `$XDG_STATE_HOME/ccgate/<target>/ccgate.log`                                    | ログファイルパス。`~` でホームディレクトリ展開                                                             |
| `log_disabled`           | bool                              | `false`                                                                         | ログ出力を完全に無効化                                                                                     |
| `log_max_size`           | int                               | `5242880`                                                                       | ローテーション閾値 (bytes, デフォルト 5MB)。`0` = ローテーションなし                                       |
| `metrics_path`           | string                            | `$XDG_STATE_HOME/ccgate/<target>/metrics.jsonl`                                 | メトリクス JSONL のパス                                                                                    |
| `metrics_disabled`       | bool                              | `false`                                                                         | メトリクス収集を完全に無効化                                                                               |
| `metrics_max_size`       | int                               | `2097152`                                                                         | ローテーション閾値 (bytes, デフォルト 2MB)。`0` = ローテーションなし                                       |
| `fallthrough_strategy`   | `"ask"` / `"allow"` / `"deny"`    | `"ask"`                                                                         | LLM が判定に迷った (`fallthrough`) 際の扱い。[Fallthrough 戦略](#fallthrough-戦略) 参照 |
| `disable_load_main_worktree_local_config` | bool | `false`                                                                         | linked git worktree で main worktree 側の `ccgate.local.jsonnet` を読むのをスキップ。詳細は [docs/configuration.md](configuration.md#where-ccgate-looks-for-config) |
| `allow`                  | string[]                          | `[]`                                                                            | 許可ルール。設定すると前の layer から引き継いだ list を **完全置換**                                       |
| `deny`                   | string[]                          | `[]`                                                                            | 拒否ルール (mandatory)。`deny_message:` ヒント対応。`allow` と同じく置換                                   |
| `environment`            | string[]                          | `[]`                                                                            | LLM に渡すコンテキスト (信頼レベル、ポリシー等)。`allow` と同じく置換                                       |
| `append_allow`           | string[]                          | `[]`                                                                            | 引き継いだ list の末尾に **追加**。プロジェクトローカル設定で典型的に使用                                  |
| `append_deny`            | string[]                          | `[]`                                                                            | 引き継いだ deny list の末尾に追加                                                                          |
| `append_environment`     | string[]                          | `[]`                                                                            | 引き継いだ environment list の末尾に追加                                                                   |

`<target>` は Claude / Codex どちらの hook が呼ばれたかで `claude` / `codex` になります。`XDG_STATE_HOME` が未設定の場合は `~/.local/state/ccgate/<target>/...` が fallback として使われます。

## ルールチューニング

ccgate は組み込みデフォルトだけで安全に動きます。この章は、その上に自分の `allow` / `deny` / `environment` を載せる方法です。

### 何を変えられるか

- `allow` / `deny` / `environment` (string list) — 値を設定すると前 layer からの引き継ぎを **置換**。
- `append_allow` / `append_deny` / `append_environment` — 前 layer からの引き継ぎに **追加**。ccgate が今後リリースで品質改善した defaults もそのまま流れ込んでくる。

### どこに書くか

- グローバル: `~/.claude/ccgate.jsonnet` または `~/.codex/ccgate.jsonnet`。
- プロジェクトローカル: `<repo>/.claude/ccgate.local.jsonnet` または `<repo>/.codex/ccgate.local.jsonnet`、Git 未追跡のみ。

layer の合成順は上の [設定ファイルの読み込み順序](#設定ファイルの読み込み順序-target-ごと) を参照。

### 組み込み defaults を確認する

```bash
ccgate claude init           | less                   # Claude embedded defaults を確認
ccgate codex  init           | less                   # Codex も同じ
ccgate claude init -p > .claude/ccgate.local.jsonnet  # プロジェクトローカルのスケルトン
ccgate codex  init -p > .codex/ccgate.local.jsonnet   # Codex も同じ
```

### 置換 vs 追加の判断

`append_*` は前 layer からの引き継ぎを残して追加するので、ccgate が今後リリースで defaults を改善したぶんは自動で取り込まれます。`allow:` / `deny:` で丸ごと置き換える場合は、新 defaults に対する突き合わせを毎リリース自分でやる必要があります (`ccgate <target> init` の出力と diff を取る)。

### ルールの書き方

1 ルール 1 行、対象操作を自然言語で書きます。末尾に `deny_message:` を書くと、その文字列が deny 時に AI に返ります。判定は LLM がやるので、LLM が `tool_input` / `tool_input_raw` / `branch_name` / 各種 path / コマンド文字列から判断できる粒度で書きます。LLM に渡らない情報 (例: working tree の dirty/clean) を guidance に書いても効きません。

書く field によって挙動が変わるので、3 パターンに分けて例を示します。

**追加で広げる** (`append_allow`):

```jsonnet
{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/claude.schema.json',
  append_allow: [
    // Claude: target path は tool_input.file_path / referenced_paths から判定可能
    'Edit / Write / MultiEdit で repo_root/docs/ 配下の Markdown を target にするものは allow (内容レビューは別途行う)。',
  ],
}
```

Codex では `apply_patch` の hunk target を `tool_input_raw` から LLM が読みます:

```jsonnet
{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/codex.schema.json',
  append_allow: [
    'apply_patch の全 hunk が repo_root/docs/ 配下の *.md を target にしている場合は allow (内容レビューは別途行う)。',
  ],
}
```

**追加で狭める** (`append_deny`):

```jsonnet
{
  append_deny: [
    'Production database access: any psql / mysql connection to a *.prod.* host. deny_message: production access is gated behind the runbook.',
    'Setting production environment variables in the running session. deny_message: configure production via the deployment system, not via shell exports.',
  ],
}
```

**完全置換で絞る** (`allow:` / `deny:`):

```jsonnet
{
  // 引き継いだ defaults を全部捨てて自分で list を書く。新 defaults は自動で流れ込まない。
  allow: [
    'Read-only filesystem inspection inside the repository.',
    'Local development commands using project scripts (build, test, lint).',
  ],
  deny: [
    'Downloading and executing remote code (curl | bash, eval $(curl ...), etc.). deny_message: vet the script first; install it via a package manager or a checked-in script.',
  ],
}
```

`$schema` 行はどちらの形でもエディタ補完を有効にします。

ccgate は jsonnet helper として `std.native('env')(name)` (未定義は空文字) と `std.native('must_env')(name)` (未定義は config-load エラー) を register しているので、任意の文字列フィールドから ccgate 独自記法を使わずに env を読めます — ホスト名やアカウント ID をルール文字列に埋め込むときに便利です。

### iteration workflow

1-2 日 ccgate を実利用したら `ccgate <target> metrics --details N` を回します。「Top fallthrough commands」「Top deny commands」のドリルダウンを見ると、追加すれば削減できる操作が分かります。`append_deny` (もしくは `append_allow`) を 1 件足して、また次回 metrics を見る、を繰り返すのが基本サイクルです。

## Provider と credential

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

対応する API キー (`CCGATE_OPENAI_API_KEY` / `CCGATE_GEMINI_API_KEY` — [provider table](#2-api-キー)) を export してください。キーが見つからない場合 ccgate は上流ツールの確認画面に fallthrough するため、provider 切替で hook が壊れることはありません。

> [!WARNING]
> reasoning model は避けること (`gpt-5`, `gpt-5-mini`, `gpt-5-nano`, `gpt-5-chat`, `o1*`, `o3*`, `o4-mini`)。`temperature=0` を拒否するため全リクエストが失敗し、分類タスクには不要な chain-of-thought に数秒かかります。`gpt-4.1-nano` / `gpt-4o-mini` / `gpt-5.4-nano-2026-03-17` を推奨。

### 互換 proxy 経由で利用する

ccgate は各 provider SDK の標準 chat / messages エンドポイントを使うので、**OpenAI 互換 / Anthropic 互換**の任意の endpoint — [LiteLLM proxy](https://docs.litellm.ai/docs/proxy/quick_start), Azure OpenAI, オンプレ gateway, 地域別 endpoint など — に対して動きます。proxy が話すプロトコルに合わせて `provider.base_url` を設定します。

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

### Refresh される credential

credential が静的な環境変数では追従できない頻度で更新される (AWS STS / Vertex ADC / OpenAI 互換 gateway の virtual key / 社内 key broker など) 場合は `provider.auth` を使います。3 つの形式の discriminated union — 用途に合う方を選びます。

```jsonnet
// helper コマンドを実行して credential を取得
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    auth: {
      type: 'exec',
      command: '/usr/local/bin/my-key-broker --provider anthropic',
    },
  },
}

// 外部 rotator が credential をファイルに書き込む
// (path 省略時は $XDG_STATE_HOME/ccgate/<target>/auth_key.json)
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    auth: {
      type: 'file',
      path: '~/.config/my-broker/anthropic.json',
    },
  },
}

// `ant auth login` の profile から credential を読む (Anthropic 専用)
// access token の refresh は SDK 自身が行うので ccgate は credential 経路に
// 一切介入しない
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    auth: {
      type: 'profile',
      profile: 'ccgate',       // `ant auth login --profile ccgate` と一致
    },
  },
}
```

helper / file の中身は次のいずれかを書きます。

- **JSON** `{"key":"sk-...","expires_at":"<RFC3339>"}` — `auth.type=exec` の場合 `$XDG_CACHE_HOME/ccgate/<target>/` にキャッシュされ、期限前に更新されます
- **plain string** — 単一行の非空文字列。キャッシュなし

`auth.type=profile` は別経路です: ccgate は読み込んだ profile を `option.WithConfig` で anthropic-sdk-go に渡し、SDK の refresh-token loop が credential ライフサイクルを保有します。さらに `option.WithoutEnvironmentDefaults` を付けるので、残っている `ANTHROPIC_API_KEY` が、宣言した profile を silent に上書きすることはありません。

解決順序: `provider.auth` (設定済み) > `CCGATE_*_API_KEY` > `*_API_KEY`。`auth` を設定済みのときに失敗しても **env var に silent に fallback はしない**。代わりに `kind=credential_unavailable` で fallthrough します。

helper の完全な仕様 (動かせる例 / `auth.cache_key` によるアカウント別キャッシュ / 初回ブラウザ認証 / 401/403 の挙動マトリクス / 障害復旧チェックリスト) は [api-key-helper.md](api-key-helper.md) を参照してください。

## Fallthrough 戦略

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
- `allow` — 迷ったら自動許可。**危険側**: LLM 自身が判断に迷った操作を無条件に通すことになる。Claude Code / Codex とも `decision.message` は `deny` のときしか AI に届かないため、強制 allow の際 AI には警告が渡らない

対象は **LLM 判定の fallthrough に限定** です。API 応答の打ち切り/拒否、API キー欠損、`bypassPermissions` / `dontAsk` モード (Claude のみ)、`ExitPlanMode` / `AskUserQuestion` (Claude のみ) はいずれも従来通り上流ツールにフォールスルーされます。

強制的に allow / deny へ変換された回数は `ccgate <target> metrics` の `F.Allow` / `F.Deny` 列 (JSON では `forced_allow` / `forced_deny`) で確認できるため、選んだ戦略が妥当に機能しているか後から監査できます。

## ログとメトリクス

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
ccgate codex  metrics --days 7        # codex 側
```

日次テーブルには Allow / Deny / Fall / F.Allow / F.Deny / Err、自動化率、平均レイテンシ、トークン使用量が並びます。「Top fallthrough commands」「Top deny commands」のドリルダウンを見ると、ルール追加で削減できる操作が特定できます。

## 既知の制約

- **Plan mode の正しさはプロンプトのみに依存 (Claude のみ)。** `permission_mode == "plan"` では、(a) 実装系 write を拒絶する判定と (b) allow guidance に載っていない read-only クエリを許可する判定の両方を、LLM とシステムプロンプトの指示文に委ねています。プロンプトで記述する以上、どちらの方向にも誤判定の余地があります。[#37](https://github.com/tak848/ccgate/issues/37) で追跡しています。
- **embedded default の特定ルールだけを部分削除する手段なし。** layer は list を **完全置換** (`allow: [...]`) するか **末尾追加** (`append_allow: [...]`) するかのどちらかです。embedded の中の 1 ルールだけ消したい場合は、その 1 件を除いた残り全部を `allow:` / `deny:` に書き直すしかありません。
- **jsonnet 側に runtime conditional logic はありません。** jsonnet 評価は hook 発火ごとの config load 時に、ccgate が `tool_input` を見る前に 1 回だけ実行されます。したがって `tool_input` / git working tree state / 外部コマンド出力に基づく分岐は jsonnet では書けません。runtime の分類は LLM の仕事で、ルールには散文で意図を書いて LLM に判断させます。config 評価時の env 読み込みは `std.native('env')(name)` / `std.native('must_env')(name)` で可能 (ホスト名などをルール文字列に埋め込む程度の用途)。

## ドキュメント

- [claude.md](claude.md) — Claude Code 固有、HookInput field リファレンス
- [codex.md](codex.md) — Codex CLI 固有、HookInput field リファレンス
- [configuration.md](configuration.md) — 設定 layering、fallthrough_strategy、metrics、既知の制約
- [api-key-helper.md](api-key-helper.md) — `provider.auth` リファレンス (helper の契約、キャッシュ、401/403 挙動、復旧手順)
- [English documentation (docs/)](../claude.md)

## CLI リファレンス

```
ccgate                         stdin から HookInput JSON を読み込む (Claude Code hook)。
                               'ccgate claude' と等価。**今後も維持されるデフォルト挙動** で、廃止予定はない。
                               既存の ~/.claude/settings.json の "command": "ccgate" 設定はそのまま動作し続ける。
ccgate claude                  bare ccgate と完全等価 (新規ユーザー向け推奨表記)
ccgate claude init [-p|-o|-f]  Claude Code 用の埋込デフォルトを出力
ccgate claude metrics [...]    Claude Code のメトリクス集計

ccgate codex                   stdin から HookInput JSON を読み込む (Codex CLI hook)
ccgate codex init [-o|-f]      Codex CLI 用の埋込デフォルトを出力
ccgate codex metrics [...]     Codex CLI のメトリクス集計
```

top-level の `ccgate init` / `ccgate metrics` は実 subcommand ではなく、per-target 形式への 1 行案内を出して exit `2` します。bare `ccgate` (hook 起動) は別経路で、上述の通り動作します。

## 開発

```bash
mise run build    # バイナリビルド
mise run test     # テスト実行
mise run vet      # go vet
mise run schema   # schemas/{claude,codex}.schema.json を再生成
```

## ライセンス

MIT
