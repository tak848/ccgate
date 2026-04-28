# ccgate -- Claude Code

[English version (docs/claude.md)](../claude.md)

`ccgate claude` フック専用のドキュメント。

## hook 登録

ccgate は Claude Code の [PermissionRequest hook](https://code.claude.com/docs/en/hooks) イベントに plug します。`~/.claude/settings.json` に追加:

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

`"command": "ccgate"` (subcommand なし) は `"command": "ccgate claude"` と等価です。Claude / Codex の両 hook を同じ dotfiles に書くときは明示形の方が意図が読みやすい。

`"matcher": ""` (空) で全 PermissionRequest を ccgate に流します。tool 種別で絞りたい場合は `"matcher": "Bash|Edit|Write"` のように書きます。

## bare `ccgate` (引数なし + stdin pipe)

引数なしで stdin から読み込む `ccgate` は `ccgate claude` と完全に等価で、サポート対象の呼び出し方の 1 つです。

ターミナルから stdin pipe なしで `ccgate` を起動すると usage banner を出して exit 0。stdin が pipe (= AI ツールが HookInput JSON を流してる) のときだけ hook を実行します。

## ccgate が HookInput から見るフィールド

Claude Code は標準 PermissionRequest payload を流します ([upstream hooks reference](https://code.claude.com/docs/en/hooks))。ccgate が読むのは:

- `tool_name`: ユーザーインタラクション専用 tool (`ExitPlanMode`, `AskUserQuestion`) で early-return -- これらは常に Claude Code prompt に fallthrough、ccgate は判定しない
- `tool_input`: LLM に転送。metrics 層は `command` / `file_path` / `path` / `pattern` のみ記録
- `permission_mode`: `"plan"` のとき system prompt を plan mode rule に切替。`"bypassPermissions"` / `"dontAsk"` は ccgate を fallthrough で短絡
- `cwd`: git context builder (`gitutil.RepoRoot`, branch, worktree) に渡す
- `transcript_path`: recent-transcript loader が末尾 N 件を読み、ユーザー意図 context として LLM に渡す
- `permission_suggestions`: LLM に背景情報として転送
- `settings_permissions`: ccgate が `~/.claude/settings.json` を別途読み、ユーザー定義の static allow / deny / ask パターンを LLM に hint として渡す (whitelist 必須ではない、後述「settings.json パターンが whitelist 要件ではない理由」参照)

## Plan mode

`permission_mode == "plan"` で system prompt の決定ルールが切り替わる:

- `allow`: 副作用なしの操作、または Claude が指定した plan ファイルへの編集。複合シェルコマンド (`|`, `&&`, `||`, `;`) は各サブコマンドが独立にこの基準を満たす必要あり
- `deny`: project / production / 共有状態への副作用全般
- `fallthrough`: 副作用 status が真に曖昧

allow guidance は plan mode で write 操作を allow に promote しません。deny guidance は依然として有効で、read-only 操作も override できます。

完全に prompt-driven なので hard guarantee なし。[#37](https://github.com/tak848/ccgate/issues/37) で追跡。

## `recent_transcript` の使われ方

`recent_transcript` は transcript JSONL の末尾 (直近のユーザーメッセージ + tool 呼び出し) を持ちます。system prompt は LLM にこう指示:

- ユーザーが直近の transcript で当該操作を明示的に依頼してたら、`deny` より `allow` / `fallthrough` を優先せよ
- ユーザーの明示依頼は `deny` を `fallthrough` に escalate できるが、`allow` には escalate できない (deny guidance は依然として勝つ)

これが LLM に「deny ルールに該当するが、ユーザーが明確に依頼してるので、refuse せず Claude Code の prompt に判断を委ねる」と言わせる唯一の signal です。Codex には現状 transcript field が無いので、この lever は Claude のみ。

## `settings.json` パターンが whitelist 要件ではない理由

`settings_permissions` は `~/.claude/settings.json` の `permissions.allow / deny / ask` の中身です。Claude Code は PermissionRequest hook を呼ぶ**前に** これらの static パターンを matching するので、ccgate に届いたリクエストは設計上 allow パターンに自動マッチしなかったケースです。よくある原因:

- `$(...)` 等の合成構文 / pipeline が literal matcher をすり抜ける
- static matcher の無い MCP tool
- ユーザーが allow パターンを最も単純な呼び出しだけに絞り、それ以外を hook に流す方針

→ `settings_permissions.allow` を whitelist 要件として扱うと hook の通常動作が壊れます。ccgate はあくまでユーザー嗜好のヒントとしてのみ使い、`settings_permissions.allow` に存在しないリクエストでも LLM が allow できる設計です。

## Codex CLI との挙動差分

| 観点                                | Claude Code                                        | Codex CLI                                                                                  |
|-------------------------------------|----------------------------------------------------|--------------------------------------------------------------------------------------------|
| Tool surface                        | `Bash`, `Read`, `Write`, `Edit`, `Glob`, MCP, ...  | `Bash`, `apply_patch`, MCP, ...                                                             |
| `permission_mode`                   | `default` / `acceptEdits` / `plan` / `bypassPermissions` / `dontAsk` | 現状 deliver されない                                                |
| `recent_transcript`                 | LLM に転送                                          | deliver されない。LLM は `tool_name` + `tool_input` + `cwd` のみで判断                       |
| `settings_permissions`              | 背景 hint として転送                                | 等価物なし。`~/.codex/config.toml` は取り込まない                                            |
| `permission_suggestions`            | 転送                                                | deliver されない                                                                              |
| State path                          | `$XDG_STATE_HOME/ccgate/claude/`                   | `$XDG_STATE_HOME/ccgate/codex/`                                                              |
| Project-local config                | `{repo_root}/.claude/ccgate.local.jsonnet`         | `{repo_root}/.codex/ccgate.local.jsonnet`                                                    |

Codex 側の詳細は [docs/ja/codex.md](codex.md) を参照。

## 既知の制約

- **Plan mode はプロンプト依存** ([#37](https://github.com/tak848/ccgate/issues/37))
- **embedded default の特定ルールだけを部分削除する手段なし**: layer は list を **完全置換** (`allow: [...]`) するか **末尾追加** (`append_allow: [...]`) するかのどちらかで、embedded の中の 1 ルールだけ消したい場合は残り全部を `allow:` / `deny:` に書き直すしかない
- **`settings.json` の deny パターンに対する deterministic short-circuit なし**: ccgate は現状すべての Claude Code PermissionRequest を LLM に通します。literal な `settings.json` deny match で early exit する deterministic prefilter は将来の最適化候補で、現時点の挙動ではありません
