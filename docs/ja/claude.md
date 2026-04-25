# ccgate — Claude Code

[English version (docs/claude.md)](../claude.md)

`ccgate claude` フック専用のドキュメント。

> このページが埋まるまで、Claude Code のセットアップ・設定・既知の制約は **[ルート README (docs/ja/README.md)](README.md)** を一次情報としてください。本ページは詳細解説のための placeholder です。

このページで扱う予定 (follow-up issue で進捗管理):

- `~/.claude/settings.json` での hook 登録 (現状 README に記載)
- Plan mode (`permission_mode == "plan"`) の挙動と既知の制約 ([#37](https://github.com/tak848/ccgate/issues/37))
- `permission_suggestions` のセマンティクスと ccgate での利用方法
- `recent_transcript` パースとユーザー意図によるエスカレーション
- Codex CLI との挙動差分 ([docs/ja/codex.md](codex.md) 参照)

## bare `ccgate` (引数なし + stdin pipe)

引数なしで stdin から読み込む `ccgate` は `ccgate claude` と完全に等価です。これは Claude Code hook の正規呼び出し方法であり、v0.6 multi-target リファクタ後も**永続的に維持**されます。既存の `~/.claude/settings.json` の `"command": "ccgate"` 設定はそのまま動作し続けます。
