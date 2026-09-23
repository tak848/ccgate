# ccgate -- Devin

[English version (docs/devin-cli.md)](../devin-cli.md)

`ccgate devin` フック専用のドキュメント。

## 前提

- Devin CLI hooks (`PreToolUse`, `PermissionRequest`, ...) が使えること。 upstream の payload schema は [Devin hooks docs](https://docs.devin.ai/cli/extensibility/hooks/overview) を参照。
- **Tool-agnostic**: Devin hooks は `exec`, `edit`, `write`, `apply_patch`, MCP tool 呼び出し (`mcp__<server>__<tool>`) など複数の surface で発火します。 ccgate は `tool_name` + `tool_input` JSON 全体で分類。

## hook 登録

Devin CLI が hook を読む場所: (project level) `.devin/hooks.v1.json`, `.devin/config.json` / `.devin/config.local.json` の `"hooks"` key, (user level) `~/.config/devin/config.json` の `"hooks"` key。 `.claude/settings.json` 形式の hook file も自動で読み込まれます。

### `.devin/hooks.v1.json` 形式 (推奨)

```json
{
  "PermissionRequest": [
    {
      "matcher": "",
      "hooks": [
        {
          "type": "command",
          "command": "ccgate devin"
        }
      ]
    }
  ]
}
```

`"matcher": ""` で Devin が emit する全 PermissionRequest (`exec` / `edit` / `apply_patch` / MCP tool 等) を ccgate で評価。subset だけ評価したい場合は tool 名 regex で絞ります (例: `"^exec$"`)。

user level で登録する場合は、同じ object を `~/.config/devin/config.json` の `"hooks"` key 配下に置きます:

```json
{
  "hooks": {
    "PermissionRequest": [
      {
        "matcher": "",
        "hooks": [
          {
            "type": "command",
            "command": "ccgate devin"
          }
        ]
      }
    ]
  }
}
```

> [!NOTE]
> `~/.claude/settings.json` で既に `ccgate` (bare) や `ccgate claude` が PermissionRequest hook として登録されている場合、 Devin はそちらも拾います -- Claude 形式の output も Devin の Claude-compat layer で受理されます。 ただし `.devin/` に `ccgate devin` を追加で登録すると、同じリクエストで両方の hook が発火するので、 project ごとにどちらか一方を選んでください。

### dotfiles を触らずに dev build を試す

リポジトリ内の開発ビルドを試したい場合は、 project-local の hooks file を置き、 `go run` を指す形にします:

```jsonc
// <repo>/.devin/hooks.v1.json
{
  "PermissionRequest": [
    {
      "matcher": "",
      "hooks": [
        {
          "type": "command",
          "command": "go run /absolute/path/to/ccgate devin"
        }
      ]
    }
  ]
}
```

`go run` の build cache が効くので 2 回目以降は速い。

## ccgate が HookInput から見るフィールド

ccgate は `tool_input` の JSON 全体をそのまま LLM に渡します。そのため、ccgate 側に専用フィールドのない MCP arguments や `apply_patch` の hunk metadata も判定対象に含まれます。metrics には parsed view (`command` / `description` / `file_path` / `path` / `pattern`) だけを書きますが、LLM に渡す内容から raw payload を削ることはありません。

ccgate が Devin HookInput から読むフィールド:

- `session_id`
- `prompt_id` (turn ごとの correlation id)
- `tool_use_id`
- `hook_event_name`
- `tool_name` (`exec`, `edit`, `write`, `apply_patch`, `mcp__<server>__<tool>`, ...)
- `tool_input` (typed view)
- `tool_input_raw` (元の JSON payload をそのまま LLM に転送 — `apply_patch` の hunk や MCP 引数を見るときの主経路)
- `referenced_paths` (`tool_input` から best-effort で抽出した path リスト。対応 tool は `read`, `write`, `edit`, `glob`, `grep`, `exec`。`apply_patch` と MCP は `tool_input_raw` を LLM が直接読む)

**wire 上に `cwd` は無し。** Devin の PermissionRequest payload は `cwd` を持ちません。 ccgate は Devin が hook プロセスに設定する `DEVIN_PROJECT_DIR` 環境変数で代替し、それも無ければ hook 自身の working directory に fallback します。

Devin 側の system prompt は LLM に `tool_name` + `tool_input` + `tool_input_raw` + `cwd` で判断するよう指示し、 HookInput に存在しない context を捏造しないようにしています。

## ccgate が emit するもの

判定が付いた場合、 ccgate は Devin の flat decision object を stdout に出力します:

```json
{"decision": "approve"}
{"decision": "block", "reason": "..."}
```

出力が無い場合は fallthrough -- Devin 側の permission prompt が表示されます。

## Devin 固有の state リファレンス

| 観点                      | 値                                                                                                |
|---------------------------|---------------------------------------------------------------------------------------------------|
| Tool surface              | `exec`, `edit`, `write`, `apply_patch`, MCP (`mcp__<server>__<tool>`) など。Devin hooks は tool 種別に関わらず全 PermissionRequest で発火 |
| Global config             | `~/.config/devin/ccgate.jsonnet` (Windows は `%APPDATA%\devin\ccgate.jsonnet`)                     |
| State path                | `$XDG_STATE_HOME/ccgate/devin/` (未設定なら `~/.local/state/ccgate/devin/`)                        |
| Project-local config      | `{repo_root}/.devin/ccgate.local.jsonnet` (Git 未追跡のみ)                                         |

## 埋込デフォルト

`ccgate devin init | less` で binary に同梱された allow / deny / environment guidance の中身を読めます。 拡張・置換の方法は [docs/ja/rule-tuning.md](rule-tuning.md) を参照。
