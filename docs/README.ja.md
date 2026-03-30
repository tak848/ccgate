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
  ├── 設定読み込み (~/.claude/permission-gate.jsonnet)
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

### 1. 設定ファイルを配置

`~/.claude/permission-gate.jsonnet` にルールを記述します。
[example/permission-gate.jsonnet](../example/permission-gate.jsonnet) を参考にしてください。

```jsonnet
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    timeout_ms: 40000,
  },
  allow: [
    'Read-Only Operations: ...',
  ],
  deny: [
    'Git Destructive: force push, deleting remote branches, ...',
  ],
  environment: [
    '**Trusted repo**: The git repository the session started in.',
  ],
}
```

JSON Schema (`permission-gate.schema.json`) を同じディレクトリに配置すると、エディタで補完が効きます。

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

環境変数 `CC_AUTOMODE_ANTHROPIC_API_KEY` または `ANTHROPIC_API_KEY` を設定してください。

## 設定

### 設定ファイルの読み込み順序

1. `~/.claude/permission-gate.jsonnet` — ベース設定
2. `{repo_root}/permission-gate.local.jsonnet` — プロジェクトローカル (Git 未追跡のみ)
3. `{repo_root}/.claude/permission-gate.local.jsonnet` — プロジェクトローカル (Git 未追跡のみ)

後のファイルが前のファイルの設定をマージ (allow/deny/environment は追加、provider は上書き) します。
プロジェクトローカル設定は **Git に追跡されていないファイルのみ** 読み込まれます。

### 設定項目

| フィールド | 型 | デフォルト | 説明 |
|-----------|------|-----------|------|
| `provider.name` | string | `"anthropic"` | プロバイダー名 |
| `provider.model` | string | `"claude-haiku-4-5"` | モデル名 |
| `provider.timeout_ms` | int | `20000` | API タイムアウト (ms) |
| `allow` | string[] | `[]` | 許可ルール |
| `deny` | string[] | `[]` | 拒否ルール (mandatory) |
| `environment` | string[] | `[]` | 環境コンテキスト |

## ログ

`~/.claude/logs/ccgate.log` に出力されます。5MB でローテーション (`.log.1`)。

## 開発

```bash
mise run build    # バイナリビルド
mise run test     # テスト実行
mise run vet      # go vet
```

## ライセンス

MIT
