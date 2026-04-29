# ccgate -- OpenAI Codex CLI

[English version (docs/codex.md)](../codex.md)

`ccgate codex` フック専用のドキュメント。

## ステータス

- **Experimental**: Codex hooks は upstream で experimental 扱い。スキーマや挙動が予告なく変更される可能性あり。OpenAI の [Codex hooks docs](https://developers.openai.com/codex/hooks) を一次情報として参照し、特定 field に依存する前に再確認してください
- **Tool-agnostic**: Codex hooks は Bash、`apply_patch`、MCP tool 呼び出しなど複数の surface で発火します。ccgate は `tool_name` + `tool_input` JSON 全体で分類

## hook 登録

Codex CLI の lookup 順序 (OpenAI [Codex hooks docs](https://developers.openai.com/codex/hooks) より):

1. `~/.codex/hooks.json`
2. `~/.codex/config.toml`
3. `<repo>/.codex/hooks.json` (project の `.codex/` layer が trusted の場合のみ)
4. `<repo>/.codex/config.toml` (同 trust 要件)

Layer は加算 -- global と project-local の両方に hook が登録されてれば両方発火します。

### `hooks.json` 形式 (推奨)

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

`"matcher": ""` で Codex が emit する全 PermissionRequest (Bash / `apply_patch` / MCP tool 等) を ccgate で評価。subset だけ評価したい場合は tool 名 pattern で絞ります。

### `config.toml` 形式

Codex 設定全体を 1 ファイルにまとめたい場合:

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

### dotfiles を触らずに dev build を試す

Project-local `<repo>/.codex/{hooks.json,config.toml}` は project が trusted のときのみ load されます。in-tree dev build を試すには、project-local hooks file (untracked) を置いて `go run` を指す:

```jsonc
// <repo>/.codex/hooks.json (untracked)
{
  "hooks": {
    "PermissionRequest": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "go run /absolute/path/to/ccgate codex",
            "statusMessage": "ccgate (dev) evaluating request"
          }
        ]
      }
    ]
  }
}
```

`go run` の build cache が効くので 2 回目以降は速い。dotfiles 管理の `~/.codex/config.toml` には触らない。

## ccgate が HookInput から見るフィールド

ccgate は `tool_input` JSON 全体を verbatim で LLM に転送するので、ccgate が typed field を持たない MCP arguments や `apply_patch` hunk metadata も classifier に届きます。metrics 層は parsed view (`command` / `description` / `file_path` / `path` / `pattern`) のみ JSONL に書きますが、raw payload を LLM message から削ぐことはありません。

upstream Codex docs に記載があり ccgate が利用するフィールド:

- `session_id`
- `transcript_path` (path のみ; ccgate は transcript JSONL を parse しない)
- `cwd`
- `hook_event_name`
- `model` (AI 側のモデル、例: `gpt-5`)
- `turn_id`
- `tool_name` (`Bash`, `apply_patch`, `mcp__<server>__<tool>`, ...)
- `tool_input` (typed view + raw forward)

Codex は Claude の `permission_mode` / `permission_suggestions` / `recent_transcript` / `settings_permissions` を deliver しません。system prompt は LLM に「ここに recent_transcript は無い -- `tool_name` + `tool_input` + `cwd` のみで判断せよ」と明示的に伝え、存在しない context を捏造しないようにしています。

## デフォルトスナップショット

ccgate は埋込 Codex defaults (`internal/cmd/codex/defaults.jsonnet`) を持ち、Claude 側と同じ allow + deny + environment 構造です。Codex 固有の主要エントリ:

- `allow`: 読み取り専用 Bash 検査、workspace 内 write (`apply_patch` の cwd / repo_root 配下のハンク、AI が今やってるプロジェクトファイル編集)、project script による build/test、repo 内パッケージインストール、feature branch git 操作、ユーザーが trust してる MCP server で side effect が user-authorized scope に収まる MCP tool
- `deny`: remote content の pipe-to-shell、one-shot remote package execution (`npx` / `pnpx` / `bunx` で unfamiliar package)、`sudo`、workspace 外への `rm -rf` / `mv` / `apply_patch` hunks、protected branch への破壊的 git、無制限 network out (`nc` / `ssh` / `scp` / `ftp` の非 allowlist 先)、destructive side effect を advertise する MCP tool で per-rule allow なし
- `environment`: heterogeneous tool surface、trusted-repo 境界、path scope ルール、**ccgate は upstream prompt の代替** -- 真に曖昧なときだけ fallthrough、それ以外は allow / deny を返す、`recent_transcript` は不在

workspace 内 `apply_patch` は意図的に `allow` に**含めています**: Claude Code の Edit/Write が ccgate を通る際と同じ bar で、ユーザーが ccgate を入れた目的はまさに repo 内編集の prompt を skip することだからです。workspace 外への apply_patch hunk は既存の deny rule でブロックされます。より保守的にしたい場合は、project-local rule で apply_patch の allow scope を狭めてください (specific subtree のみ等)。

## Claude Code との挙動差分

完全な表は [docs/ja/claude.md](claude.md) を参照。

