# ccgate

Claude Code の PermissionRequest フックとして動作する Go バイナリ。

## インストール

```bash
# mise (推奨)
mise use -g go:github.com/tak848/ccgate

# or go install
go install github.com/tak848/ccgate@latest
```

## 開発

```bash
mise run build    # バイナリビルド (開発用)
mise run test     # テスト実行
mise run vet      # go vet
```

## コーディング規約

- Go 1.25
- エラーは `fmt.Errorf("...: %w", err)` でラップ
- サイレントなエラー無視は禁止
- テストは table-driven で書く
- マジックナンバーは名前付き定数にする
