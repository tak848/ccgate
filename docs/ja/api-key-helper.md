# 期限付き・自動更新される API キー

[English version (docs/api-key-helper.md)](../api-key-helper.md)

provider が必要とする認証情報が静的な環境変数では追従できない頻度で更新される — AWS STS セッション、Vertex ADC、OpenAI 互換 gateway の virtual key、社内 key broker など — 場合に、`*_API_KEY` の代わりに helper プロセスやファイル経由で解決させる仕組みです。

このドキュメントが完全な参照です。README には最小限の設定例しか載せず、helper の契約・キャッシュの挙動・アカウント分離・セキュリティ上の注意・障害時の復旧手順はすべてこのページに集約しています。

## 出力フォーマット

helper は次のいずれかの形を stdout (もしくは `api_key_file` の中身として) に書きます。

- **JSON**: `{"key":"sk-...","expires_at":"2026-05-04T01:23:45Z"}`。厳密に解析されます。`key` は必須、`expires_at` は任意。未知のトップレベルフィールド (broker のメタデータ等) は受け入れますが捨てます — キャッシュにも SDK にも `{key, expires_at}` だけが渡ります。
- **plain string**: 改行を含まない単一の非空文字列。そのまま渡されます。

`expires_at` は RFC3339。helper の stdout が 64 KiB を超えると `output_too_large` で拒否します。`api_key_file` の内容にも同じ 64 KiB 上限が適用されます。

キャッシュの扱いは経路ごとに違います:

- `api_key_command` の場合: 未来の `expires_at` を含む JSON は target 別のキャッシュファイル ([キャッシュ](#キャッシュ) 参照) に保存し、`api_key_refresh_margin` に従って期限前に更新します。`expires_at` を含まない JSON と plain string は受け付けますが **キャッシュしません** — hook が呼ばれるたびに helper を再実行します。
- `api_key_file` の場合: ccgate は hook が呼ばれるたびにファイルを読み直すだけで、内部キャッシュは持ちません。credential をいつ更新するかは外部 rotator の責務です。

## 設定

```jsonnet
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    api_key_command: '/usr/local/bin/my-key-broker --provider anthropic',
    api_key_refresh_margin: '60s', // 任意、デフォルト 30s
    api_key_command_timeout: '5s', // 任意、デフォルト 5s
  },
}
```

| field | 型 | デフォルト | 役割 |
|---|---|---|---|
| `provider.api_key_command` | string | `""` | `/bin/sh -c` で実行されるシェルコマンド。stdout が認証情報になる |
| `provider.api_key_file` | string (絶対パス または `~/...`) | `""` | 認証情報が書かれたファイルを呼び出しごとに読む。キャッシュなし |
| `provider.api_key_refresh_margin` | duration | `"30s"` | `now + margin >= expires_at` でキャッシュを stale 扱いにする。`>= 0` (`0s` で早期更新を無効化) |
| `provider.api_key_command_timeout` | duration | `"5s"` | helper 1 回の hot-path 上限。`> 0` |

`provider` ブロックは設定レイヤー間で **丸ごと置換** される設計のため、project-local 設定で `provider` を再掲する場合は global で書いた `api_key_command` / `api_key_file` も忘れずに書き写してください。書き漏らすと当該プロジェクトでだけ helper 設定が静かに消えます。

## 解決順序とプラットフォーム対応

`api_key_command` > `api_key_file` > `CCGATE_*_API_KEY` > `*_API_KEY`

`api_key_command` または `api_key_file` を設定している状態で解決に失敗しても、ccgate は env var 経路へ **暗黙に fallback しません**。silent fallback は helper のバグを隠してしまうためです。代わりに `kind=credential_unavailable` で fallthrough し、reason がどの段階で失敗したかを示します (reason の網羅は [docs/ja/configuration.md](configuration.md) を参照)。

`api_key_command` / `api_key_file` は Unix のみ (Linux / macOS / *BSD) の対応です。Windows でこれらを設定すると `reason=unsupported_platform` で fallthrough します。どちらも設定していない Windows ユーザーは従来通り `*_API_KEY` の env var 経路で動きますが、設定済みの helper / file が unsupported だったときに ccgate が黙って env var に fallback することはありません。

## キャッシュ

- パス: `$XDG_CACHE_HOME/ccgate/<target>/api_key.<sha256[:16]>.json` (target は `claude` / `codex`)。`XDG_CACHE_HOME` 未設定時は `~/.cache/ccgate/<target>/...` にフォールバック
- パーミッション: ディレクトリ `0700`、ファイル `0600`。既存ディレクトリが緩いモードで作られていた場合も `0700` に締め直します
- キャッシュの中身は正規化した `{key, expires_at}` のみ。helper が出力した余分なフィールド (refresh token / broker session ID など) はディスクには残しません
- atomic rename: 一時ファイルを同じディレクトリ内に作って rename で差し替えるので、ファイルシステム跨ぎの問題はありません
- 同時呼び出しは隣接する lock ファイル (`*.lock`) の `flock` で直列化されます。lock ファイルは消されないので、残っていても異常ではありません

### キャッシュキーとアカウント分離

キャッシュキーは `(target, provider.name, base_url, api_key_command)` のみから作られ、環境変数は **含まれません**。helper が `AWS_PROFILE` / `GCLOUD_ACCOUNT` / `OP_ACCOUNT` などに依存している場合、`api_key_command: 'aws sts ...'` のように literal に書くと、すべてのアカウントで同じキャッシュファイルを共有してしまいます。

アカウントごとに分離するには次のどちらか。

- アカウントをコマンド文字列に直接埋め込む: `api_key_command: 'aws sts assume-role --profile prod ...'`。コマンド文字列が違えばハッシュも別になり、別プロジェクト・別アカウントは別キャッシュになります
- `api_key_file` をアカウントごとに分け、各アカウントの rotator が専用パスに書き込む形にする

コマンド文字列で表現しきれない事情がある場合の `api_key_cache_key` (ユーザー指定 salt) は、[#61](https://github.com/tak848/ccgate/issues/61) の follow-up として追跡しています。

## セキュリティ上の注意

- `api_key_file`: ccgate は読むだけで mode の正規化はしません。ファイル本体は `chmod 0600`、親ディレクトリは `chmod 0700` を user 側で設定してください
- `api_key_command`: コマンド文字列に literal な秘密情報を **直書きしない** こと。文字列は `/bin/sh -c` に渡されるため、`ps` / `/proc/<pid>/cmdline` / 監査ログ / シェル履歴に残ります。秘密情報はファイルや keychain に置き、helper の中で読む形にしてください
- helper の stderr 本文は `ccgate.log` には **書き出されません**。ccgate は stderr をメモリ上限のために内部 capture しますが、log には byte 数と exit error しか残しません。stderr の内容を見たい場合は ccgate のログを覗くのではなく、helper を `2>&1` 付きで手動実行してください
- provider のエラーレスポンス本文は `ccgate.log` / `metrics.jsonl` に到達する前にマスクされます。`anthropic-sdk-go` と `openai-go` はどちらも `Error.Error()` にレスポンス本文を埋め込む実装なので、ccgate 側でこれを `<provider> API error (status N)` の短い要約に置き換えています。proxy がデバッグ用のレスポンス本文に認証情報を含めた場合でも、ログには漏れません

## helper が満たすべき条件

helper は次の条件を満たす必要があります。

- **非対話的** であること (TTY 入力なし、ブラウザを開かない、stdin はクローズ済みで起動される)
- **デーモン化しない** こと: process group の外に fork するとタイムアウト時の kill が効きません
- stdout には **認証情報のみ** を書く。診断出力は stderr に。ただし stderr にも秘密情報は書かないこと (運用者によっては stderr を取り込むため)
- plain string モードでは、trim 後の stdout が単一行の非空文字列であること。複数行の出力は `invalid_plain_output` で拒否されます
- 同じ `(api_key_command, provider.name, base_url)` の組み合わせに対しては **決定論的** であること: 同じ設定で動く 2 つの呼び出しは、認証情報が指す対象を一致させてください

ccgate は helper の環境変数に `CCGATE_API_KEY_RESOLUTION=1` を追加して起動します。helper が ccgate を再帰的に呼び出す構成のときに、再帰検知に使えます。それ以外の環境変数 (`*_API_KEY` を含む) は継承されるので、既存の認証情報を読み取って wrap するパターンの helper はそのまま動きます。

## helper の例

### plain string: 既存の env var を中継するだけ

最も単純な helper は、運用者がすでに環境変数として持っている認証情報をそのまま出力するだけのものです。実際の broker を組む前に解決経路の動作確認をするのに便利です。

```sh
#!/bin/sh
# ~/bin/ccgate-key-passthrough.sh
set -eu
printf '%s' "${ANTHROPIC_API_KEY:?ANTHROPIC_API_KEY is not set}"
```

`chmod 700 ~/bin/ccgate-key-passthrough.sh` してから `api_key_command: '~/bin/ccgate-key-passthrough.sh'` を設定。ccgate は呼び出しごとにこれを実行し (キャッシュなし)、env の値を SDK に渡します。

### JSON + expiry: broker からキャッシュ

実運用の broker が期限付き認証情報を発行する場合、`{key, expires_at}` で wrap して ccgate にキャッシュ + 期限直前更新をさせます。token に `"` / `\` / 改行が含まれても壊れないよう `jq` で組み立てます。

```sh
#!/bin/sh
# ~/bin/ccgate-key-broker.sh
set -eu
TOKEN=$(my-key-broker --provider anthropic) # API キーを stdout に出すコマンド
# refresh_margin (デフォルト 30s) に余裕を持たせるため、broker の TTL より少し短めにする
EXP=$(date -u -v+50M +%FT%TZ 2>/dev/null || date -u -d '+50 minutes' +%FT%TZ)
jq -nc --arg key "$TOKEN" --arg expires_at "$EXP" '{key:$key, expires_at:$expires_at}'
```

`api_key_command: '~/bin/ccgate-key-broker.sh'` で指す。先に単独で動作確認 (`~/bin/ccgate-key-broker.sh | jq .` で valid な JSON object が返る) を取ってから ccgate に渡します。

### `api_key_file` の rotator: hot-path 上で helper を呼ばない

hook の hot path で helper exec を完全に避けたい場合、外部の rotator が同じ JSON 形を atomic rename でファイルに書き出します。

```sh
#!/bin/sh
# cron / launchd / systemd-timer から実行
set -eu
TOKEN=$(my-key-broker --provider anthropic)
EXP=$(date -u -v+1H +%FT%TZ 2>/dev/null || date -u -d '+1 hour' +%FT%TZ)
TMP=$(mktemp ~/.config/my-broker/anthropic.json.XXXXXX)
jq -nc --arg key "$TOKEN" --arg expires_at "$EXP" '{key:$key, expires_at:$expires_at}' > "$TMP"
chmod 0600 "$TMP"
mv "$TMP" ~/.config/my-broker/anthropic.json
```

そして `api_key_file: '~/.config/my-broker/anthropic.json'` を指す。ccgate は呼び出しごとにファイルを読むだけで、内部キャッシュは持ちません — ローテーションの責務は rotator 側です。

## provider が 401/403 を返したときの挙動

ccgate がたった今使った認証情報を provider が拒否した場合の挙動は経路ごとに違います。

- `api_key_command` 経路: keystore のキャッシュファイルを unlink し、その呼び出しは fallthrough (exit 1 にはしない)。次の呼び出しで helper が再実行され、新鮮な認証情報が取り直されます
- `api_key_file` 経路: 内部キャッシュがないので invalidate するものがありません。その呼び出しは fallthrough しますが、新しい認証情報をファイルに書き直すのは rotator の責務です
- env var 経路は **意図的にこの分岐に乗せていません**。ccgate からは env を rotate できず、401/403 を黙って飲むと user 側の設定ミスを隠してしまうためです — env var の場合は通常の API エラー経路 (exit 1) のまま

## AWS `credential_process` との差分

出力形式は AWS `credential_process` に意図的に近づけています。そのため、既存の helper は薄いラッパーを 1 枚挟むだけで流用しやすくなっています。一方、**ccgate は helper の出力をディスクにキャッシュする** 設計です。AWS CLI が呼び出しのたびに helper を再実行するのとは違い、hook の実行経路での遅延を抑えるためのトレードオフです。

キャッシュさせたくない broker の場合は、`expires_at` を含めない JSON (`{"key":"..."}`) を返せば毎回再実行されます。

## 障害時の復旧チェックリスト

何かおかしいときは:

1. `ccgate.log` (`$XDG_STATE_HOME/ccgate/<target>/ccgate.log`) を tail して `kind=credential_unavailable` のエントリを探し、`reason` と `source` (`command` / `file` / `cache` / `lock`) attribute を確認。どの段階で失敗したかが分かります
2. `ccgate <target> metrics` を実行し、**Credential failures** セクションで `(source, reason)` 別の集計を確認
3. キャッシュ起因が疑わしい場合は `$XDG_CACHE_HOME/ccgate/<target>/api_key.*.json` を削除して再生成させます。隣接する `*.lock` は再利用されるので削除不要です
4. `expired` が出続ける場合は helper の `expires_at` と `date -u` を比較してください。helper 内部の TTL ロジックや時計ズレが原因のことが多いです
5. 単独再現は `/bin/sh -c "$your_command"` を実行して helper と同じ stdout が出るかを確認

reason の完全な分類 (metrics に乗るものと、log のみで出るキャッシュ層 warning の違い) は [docs/ja/configuration.md](configuration.md#credential_unavailable-の-reason-値) を参照してください。
