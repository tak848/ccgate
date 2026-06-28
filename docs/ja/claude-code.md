# ccgate -- Claude Code

[English version (docs/claude-code.md)](../claude-code.md)

`ccgate claude` フック専用のドキュメント。

## hook 登録

ccgate は Claude Code の [PermissionRequest hook](https://code.claude.com/docs/en/hooks) イベントに接続します。`~/.claude/settings.json` に追加:

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

- `tool_name`: ユーザー操作専用 tool (`ExitPlanMode`, `AskUserQuestion`) は早期に処理を終え、常に Claude Code の確認 prompt に委ねます。ccgate はこれらを判定しません
- `tool_input`: typed object として LLM に転送。metrics 層は `command` / `file_path` / `path` / `pattern` のみ記録
- `tool_input_raw`: 元の `tool_input` JSON をそのまま LLM に渡します。typed view から漏れる field (ネストされた MCP 引数など) もここから読めます
- `referenced_paths`: `tool_input` から best-effort で抽出した path のリスト。対象 tool は `Read` / `Write` / `Edit` / `MultiEdit` / `Glob` / `Grep` / `Bash` のみ。それ以外の tool (MCP / user-interaction tool) では空。LLM は `tool_input_raw` から raw payload を直接読めます
- `permission_mode`: `"plan"` のとき system prompt を plan mode rule に切替。`"bypassPermissions"` / `"dontAsk"` は ccgate を fallthrough で短絡
- `cwd`: git context builder (`gitutil.RepoRoot`, branch, worktree) に渡す。working tree の dirty/clean は渡しません
- `transcript_path`: デフォルトでは recent-transcript loader が末尾 N 件を読み、ユーザー意図 context として LLM に渡す。`include_recent_transcript_in_prompt: false` で省略可能
- `permission_suggestions`: LLM に背景情報として転送
- `settings_permissions`: ccgate が `~/.claude/settings.json` を別途読み、ユーザー定義の static allow / deny パターンを LLM に hint として渡す (whitelist 必須ではない、後述「settings.json パターンが whitelist 要件ではない理由」参照)

## `--add-dir` / `additionalDirectories` と ccgate の信頼境界

Claude Code の `--add-dir` フラグや `permissions.additionalDirectories` は、Claude Code がアクセスできるディレクトリを広げる設定です。ただし、それだけで ccgate がそのディレクトリを trusted と扱うわけではありません。PermissionRequest payload には現在 add-dir されているディレクトリ一覧が含まれないため、ccgate から見えるのは `cwd`、git context、現在の `tool_input` に出てきた path だけです。

追加ディレクトリを ccgate の判断材料にしたい場合は、`append_environment` で明示してください。ここに書かれていない repo 外 path は、既定ルール上は越境アクセスに見え、deny または確認への fallthrough になる可能性があります。

read-only の参照ディレクトリとして扱う例:

```jsonnet
{
  append_environment: [
    'Additional Claude directory /path/to/shared-lib is read-only reference material. Reads are expected; writes, build/install commands, and deletion there are outside the trusted repo.',
  ],
}
```

同じ trusted workspace として扱う例:

```jsonnet
{
  append_environment: [
    'Additional Claude directory /path/to/shared-lib is part of the same trusted workspace as this repo. Treat reads and local development commands there as expected, unless a deny rule otherwise matches.',
  ],
}
```

## Plan mode

`permission_mode == "plan"` で system prompt の決定ルールが切り替わる:

- `allow`: 副作用なしの操作、または Claude が指定した plan ファイルへの編集。複合シェルコマンド (`|`, `&&`, `||`, `;`) は各サブコマンドが独立にこの基準を満たす必要あり
- `deny`: project / production / 共有状態への副作用全般
- `fallthrough`: 副作用 status が真に曖昧

allow guidance は plan mode で write 操作を allow に promote しません。deny guidance は依然として有効で、read-only 操作も override できます。

完全に prompt-driven なので hard guarantee なし。

## `recent_transcript` の使われ方

`recent_transcript` は transcript JSONL の末尾 (直近のユーザーメッセージ + tool 呼び出し) を持ちます。デフォルトでは含めますが、`include_recent_transcript_in_prompt: false` で省略できます。含めた場合、system prompt は LLM にこう指示:

- ユーザーが直近の transcript で当該操作を明示的に依頼していた場合、`deny` より `allow` / `fallthrough` を優先せよ
- ユーザーの明示依頼は `deny` を `fallthrough` に引き上げられるが、`allow` までは引き上げられない (deny guidance は依然として勝つ)

これが LLM に「deny ルールに該当するが、ユーザーが明確に依頼してるので、refuse せず Claude Code の prompt に判断を委ねる」と言わせる唯一の signal です。`include_recent_transcript_in_prompt` が false の場合、ccgate はこの field を省略し、この escalation を使わない prompt rule に切り替えます。

## `settings.json` パターンが whitelist 要件ではない理由

`settings_permissions` は `~/.claude/settings.json` の `permissions.allow / deny` の中身です。Claude Code は PermissionRequest hook を呼ぶ**前に** これらの static パターンを matching するので、ccgate に届いたリクエストは設計上 allow パターンに自動マッチしなかったケースです。よくある原因:

- `$(...)` 等の合成構文 / pipeline が literal matcher をすり抜ける
- static matcher の無い MCP tool
- ユーザーが allow パターンを最も単純な呼び出しだけに絞り、それ以外を hook に流す方針

→ `settings_permissions.allow` を whitelist 要件として扱うと hook の通常動作が壊れます。ccgate はあくまでユーザー嗜好のヒントとしてのみ使い、`settings_permissions.allow` に存在しないリクエストでも LLM が allow できる設計です。

## Claude 固有の HookInput / state リファレンス

| 観点                              | 値                                                                                                                                       |
|-----------------------------------|------------------------------------------------------------------------------------------------------------------------------------------|
| Tool surface                      | `Bash`, `Read`, `Write`, `Edit`, `MultiEdit`, `Glob`, `Grep`, MCP, ユーザー操作 tool (`ExitPlanMode`, `AskUserQuestion`)                  |
| `permission_mode` の値            | `default` / `acceptEdits` / `plan` / `bypassPermissions` / `dontAsk`。`plan` は system prompt を切替、`bypassPermissions` / `dontAsk` は fallthrough |
| `recent_transcript`               | デフォルトでは `transcript_path` から読み込み、ユーザー意図 context として LLM に渡す。`include_recent_transcript_in_prompt: false` で省略 (上の「`recent_transcript` の使われ方」参照) |
| `settings_permissions`            | hint として LLM に渡す (上の「`settings.json` パターンが whitelist 要件ではない理由」参照)                                                |
| `permission_suggestions`          | そのまま LLM に転送                                                                                                                       |
| State path                        | `$XDG_STATE_HOME/ccgate/claude/` (未設定なら `~/.local/state/ccgate/claude/`)                                                              |
| Project-local config              | `{repo_root}/.claude/ccgate.local.jsonnet` (Git 未追跡のみ)                                                                                |

## 制約

- **Plan mode は prompt-only**: `permission_mode == "plan"` では (a) 実装系 write を拒絶する判定と (b) 明示的な allow guidance なしの read-only クエリ許可の両方を、LLM とシステムプロンプトの指示文に委ねている。どちらの方向にも誤判定の余地あり
- **embedded default の特定ルールだけを部分削除する手段なし**: layer は list を **完全置換** (`allow: [...]`) するか **末尾追加** (`append_allow: [...]`) するかのどちらかで、embedded の中の 1 ルールだけ消したい場合は残り全部を `allow:` / `deny:` に書き直すしかない
- **`settings.json` の deny パターンに対する deterministic short-circuit なし**: ccgate はすべての Claude Code PermissionRequest を LLM に通す。literal な `settings.json` deny match で ccgate を early exit する経路はない
