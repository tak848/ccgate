# ccgate -- ルールチューニング

[English version (docs/rule-tuning.md)](../rule-tuning.md)

provider と hook の登録が済んだら、ここから先は「`allow` / `deny` / `append_*` をどう書くか」が中心です。1 ページで完結するよう、 defaults の確認から iteration までを順に説明します。

## 1. 組み込み defaults を確認する

```bash
ccgate claude init | less                                # Claude embedded defaults を読む
ccgate codex  init | less                                # Codex も同じ
ccgate claude init -p > .claude/ccgate.local.jsonnet     # プロジェクトローカルのスケルトン (空雛形)
ccgate codex  init -p > .codex/ccgate.local.jsonnet      # Codex も同じ
```

`-p` で吐ける雛形には provider 設定や `fallthrough_strategy` のコメントアウト例が入っているので、空ファイルから書き始めるより楽です。

## 2. どこに書くか

- グローバル: `~/.claude/ccgate.jsonnet` / `~/.codex/ccgate.jsonnet`
- プロジェクトローカル: `<repo>/.claude/ccgate.local.jsonnet` / `<repo>/.codex/ccgate.local.jsonnet` (Git 未追跡のみ)

layer の合成順は [設定リファレンス](configuration.md#ccgate-が-config-を探す場所) 参照。 ざっくり: embedded defaults → グローバル → main worktree project-local → current worktree project-local の順で上書きされていきます。

## 3. 置換 vs 追加 (= コピペするかしないか)

| 書き方 | 実行中の binary の embedded defaults との関係 |
|--------|--------------------------------------------|
| `append_allow` / `append_deny` / `append_environment` | embedded defaults を残し、 自分のエントリを末尾に追加 |
| `allow:` / `deny:` / `environment:` | embedded defaults を捨てて、 自分の list だけが有効になる |

完全置換側を使っている場合は、 `ccgate <target> init` を現時点の embedded list の参照源として使い、 手で反映してください。

「default を 1 件だけ消したい」を append で実現する経路はありません (append は **足す** だけ)。1 件除外したい場合は `allow:` / `deny:` で残り全部を書き直すしかありません。

## 4. ルールの書き方

1 ルール 1 行の自然言語で対象操作を書きます。末尾に `deny_message:` を付けると、deny 時にその文字列が AI へのヒントとして渡ります。

判定は LLM が行うので、LLM が `tool_input` / `tool_input_raw` / `branch_name` / 各種 path / コマンド文字列から判断できる粒度で書くのが要点です。LLM に渡らない情報 (例: working tree の dirty/clean) を guidance に書いても効きません。LLM に渡る代表項目は README の [§ Concepts](../../README.md#concepts) 参照。

書く field によって挙動が変わるので、3 パターンに分けて例を示します。

### 追加で広げる (`append_allow`)

Claude:

```jsonnet
{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/claude.schema.json',
  append_allow: [
    // target path は tool_input.file_path / referenced_paths から LLM が判定可能
    'Edit / Write / MultiEdit で repo_root/docs/ 配下の Markdown を target にするものは allow (内容レビューは別途)。',
  ],
}
```

Codex (`apply_patch` の hunk target を `tool_input_raw` から LLM が読む):

```jsonnet
{
  ['$schema']: 'https://raw.githubusercontent.com/tak848/ccgate/main/schemas/codex.schema.json',
  append_allow: [
    'apply_patch の全 hunk が repo_root/docs/ 配下の *.md を target にしている場合は allow (内容レビューは別途)。',
  ],
}
```

### 追加で狭める (`append_deny`)

```jsonnet
{
  append_deny: [
    'Production database access: any psql / mysql connection to a *.prod.* host. deny_message: production access is gated behind the runbook.',
    'Setting production environment variables in the running session. deny_message: configure production via the deployment system, not via shell exports.',
  ],
}
```

### 完全置換で絞る (`allow:` / `deny:`)

```jsonnet
{
  // embedded defaults を採用せず自分で list を書く形 (embedded defaults は有効にならない)。
  allow: [
    'Read-only filesystem inspection inside the repository.',
    'Local development commands using project scripts (build, test, lint).',
  ],
  deny: [
    'Downloading and executing remote code (curl | bash, eval $(curl ...), etc.). deny_message: vet the script first; install it via a package manager or a checked-in script.',
  ],
}
```

`$schema` 行はどの形でもエディタ補完を有効にします。

### 動的な値を埋め込む

ルール文字列にホスト名やアカウント ID など環境依存の値を入れたいときは jsonnet helper を使います:

- `std.native('env')(name)` — 未定義は空文字
- `std.native('must_env')(name)` — 未定義なら config-load エラー

評価は **hook 発火ごとの config load 時** に 1 回だけ行われます (= ccgate が `tool_input` を見る前)。`tool_input` や git state に基づいて runtime に分岐する仕組みではないので注意してください。 runtime の分類は LLM の仕事です。

## 5. iteration workflow

1-2 日 ccgate を実利用したら `ccgate <target> metrics --details N` を回します。「Top fallthrough commands」「Top deny commands」のドリルダウンで、ルール追加で削減できる操作が分かります。`append_deny` (もしくは `append_allow`) を 1 件足して metrics を再確認する、を繰り返すのが基本サイクルです。

metrics の列の意味、JSON 出力の schema、credential failure の集計は [設定リファレンスのメトリクス出力](configuration.md#メトリクス出力) を参照。

## 関連

- [設定リファレンス (configuration.md)](configuration.md) — Layer / merge / 設定全フィールド / metrics / Fallthrough strategy
- [Refresh される credential (api-key-helper.md)](api-key-helper.md) — `provider.auth` の helper 契約、 401/403 挙動、復旧手順
- [Claude Code 固有 (claude.md)](claude.md) — Claude Code の HookInput
- [Codex CLI 固有 (codex.md)](codex.md) — Codex CLI の HookInput
