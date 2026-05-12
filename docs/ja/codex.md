# ccgate -- OpenAI Codex CLI

[English version (docs/codex.md)](../codex.md)

`ccgate codex` フック専用のドキュメント。

## ステータス

- **upstream schema を一次情報とする**: Codex hooks は `[features] codex_hooks = true` flag 配下にあります。OpenAI の [Codex hooks docs](https://developers.openai.com/codex/hooks) を authoritative として参照し、特定 field に依存する前に再確認してください
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
codex_hooks = true   # Codex hooks はこの feature flag 配下にあるため、互換性のため明示しておく

[[hooks.PermissionRequest]]
matcher = ""

[[hooks.PermissionRequest.hooks]]
type    = "command"
command = "ccgate codex"
statusMessage = "ccgate evaluating request"
```

### dotfiles を触らずに dev build を試す

project-local の `<repo>/.codex/{hooks.json,config.toml}` は、project が trusted の場合だけ読み込まれます。リポジトリ内の開発ビルドを試したい場合は、Git 未追跡の project-local hooks file を置き、`go run` を指す形にします:

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

ccgate は `tool_input` の JSON 全体をそのまま LLM に渡します。そのため、ccgate 側に専用フィールドのない MCP arguments や `apply_patch` の hunk metadata も判定対象に含まれます。metrics には parsed view (`command` / `description` / `file_path` / `path` / `pattern`) だけを書きますが、LLM に渡す内容から raw payload を削ることはありません。

upstream Codex docs に記載があり ccgate が利用するフィールド:

- `session_id`
- `transcript_path` (path のみ; ccgate は transcript JSONL を parse しない)
- `cwd`
- `hook_event_name`
- `model` (AI 側のモデル、例: `gpt-5`)
- `turn_id`
- `tool_name` (`Bash`, `apply_patch`, `mcp__<server>__<tool>`, ...)
- `tool_input` (typed view)
- `tool_input_raw` (元の JSON payload をそのまま LLM に転送 — `apply_patch` の hunk や MCP 引数を見るときの主経路)
- `referenced_paths` (`tool_input` から best-effort で抽出した path リスト。Codex では `Bash` のみ対応。`apply_patch` と MCP は `tool_input_raw` を LLM が直接読む)

Codex 側の system prompt は LLM に `tool_name` + `tool_input` + `tool_input_raw` + `cwd` で判断するよう指示し、 HookInput に存在しない context を捏造しないようにしています。

## Codex 固有の state リファレンス

| 観点                      | 値                                                                                                |
|---------------------------|---------------------------------------------------------------------------------------------------|
| Tool surface              | `Bash`, `apply_patch`, MCP (`mcp__<server>__<tool>`)。Codex hooks は tool 種別に関わらず全 PermissionRequest で発火 |
| State path                | `$XDG_STATE_HOME/ccgate/codex/` (未設定なら `~/.local/state/ccgate/codex/`)                       |
| Project-local config      | `{repo_root}/.codex/ccgate.local.jsonnet` (Git 未追跡のみ、project trust が必要)                 |

## 埋込デフォルト

`ccgate codex init | less` で binary に同梱された allow / deny / environment guidance の中身を読めます。 拡張・置換の方法は [docs/ja/rule-tuning.md](rule-tuning.md) を参照。
