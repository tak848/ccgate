# 期限付き・自動更新される API キー

[English version (docs/api-key-helper.md)](../api-key-helper.md)

provider が必要とする認証情報が静的な環境変数では追従できない頻度で更新される — AWS STS セッション、Vertex ADC、OpenAI 互換 gateway の virtual key、社内 key broker など — 場合に、`*_API_KEY` の代わりに `provider.auth` 経由で解決させる仕組みです。

このドキュメントが完全な参照です。README には最小限の設定例しか載せず、helper の契約・キャッシュの挙動・アカウント分離・セキュリティ上の注意・障害時の復旧手順はすべてこのページに集約しています。

## 出力フォーマット

helper は次のいずれかの形を stdout (もしくは `auth.type=file` のファイル中身として) に書きます。

- **JSON**: `{"key":"sk-...","expires_at":"2026-05-04T01:23:45Z"}`。厳密に解析されます。`key` は必須、`expires_at` は任意。未知のトップレベルフィールド (broker のメタデータ等) は受け入れますが捨てます — キャッシュにも SDK にも `{key, expires_at}` だけが渡ります
- **plain string**: 改行を含まない単一の非空文字列。前後の空白を trim した値が SDK に渡されます

`expires_at` は RFC3339。helper の stdout が 64 KiB を超えると `output_too_large` で拒否します。ファイル経路の中身にも同じ 64 KiB 上限が適用されます。

キャッシュの扱いは経路ごとに違います:

- `auth.type=exec` の場合: 未来の `expires_at` を含む JSON は target 別のキャッシュファイル ([キャッシュ](#キャッシュ) 参照) に保存し、`auth.refresh_margin_ms` に従って期限前に更新します。`expires_at` を含まない JSON と plain string は受け付けますが **キャッシュしません** — hook が呼ばれるたびに helper を再実行します
- `auth.type=file` の場合: ccgate は hook が呼ばれるたびにファイルを読み直すだけで、内部キャッシュは持ちません。credential をいつ更新するかは外部 rotator の責務です

`auth.refresh_margin_ms` は **新しく取得した認証情報の最低残 TTL ガード** としても効きます (helper exec 出力 / file 中身の両方に適用)。残 TTL がマージン未満の認証情報は SDK に渡さず `expired` で fallthrough し、次回 API 呼び出しの最中に切れて 401 になる事故を防ぎます。

## 設定

```jsonnet
// auth.type=exec: ccgate がコマンドを実行して stdout を読む
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    auth: {
      type: 'exec',
      command: '/usr/local/bin/my-key-broker --provider anthropic',
      refresh_margin_ms: 60000, // 任意、デフォルト 60000
      timeout_ms: 5000,         // 任意、デフォルト 5000
      cache_key: '${AWS_PROFILE}', // 任意、後述「アカウント分離」参照
    },
  },
}

// auth.type=file: 外部 rotator が認証情報を書き込む
{
  provider: {
    name: 'anthropic',
    auth: {
      type: 'file',
      path: '~/.config/my-broker/anthropic.json',
      refresh_margin_ms: 60000, // 任意、デフォルト 60000
    },
  },
}
```

| field | 型 | デフォルト | 役割 |
|---|---|---|---|
| `auth.type` | `"exec"` / `"file"` | (`auth` を書くなら必須) | 識別子。どの他フィールドが有効になるかを決める |
| `auth.command` | string | `""` | (`type=exec` 専用、必須) `/bin/sh -c` で実行されるシェルコマンド。stdout が認証情報になる |
| `auth.path` | string (絶対パス または `~/...`) | `""` | (`type=file` 専用、必須) ローカル regular file。中身は exec と同じ JSON / plain string |
| `auth.refresh_margin_ms` | int (ms) | `60000` | `now + margin >= expires_at` でキャッシュを stale 扱いに (`type=exec`)。新しく取れた認証情報の最低残 TTL ガード (両 type)。`>= 0` (`0` でガード無効化) |
| `auth.timeout_ms` | int (ms) | `5000` | (`type=exec` 専用) helper 1 回の hot-path 上限。`> 0` |
| `auth.cache_key` | string | `""` | (`type=exec` 専用) cache fingerprint に加える secret-free な salt。`${VAR}` 形式の env 展開対応。後述「[アカウント分離](#キャッシュキーとアカウント分離)」参照 |

`provider` ブロックは設定レイヤー間で **丸ごと置換** される設計のため、project-local 設定で `provider` を再掲する場合は global で書いた `auth` ブロックも忘れずに書き写してください。書き漏らすと当該プロジェクトでだけ helper 設定が静かに消えます。

### `auth.type=file` はローカル FS 専用

`auth.path` は **ローカル regular file 専用の best-effort 契約** です。ローカル POSIX ファイルシステム (XFS / ext4 / APFS / HFS+) はサポート対象。NFS / SMB / FUSE / keychain mount は **明示的に非対応** — Go の `os.File.SetDeadline` は regular file には適用されず、遅延の大きいリモート mount に hard read deadline をかける手段が無いためです。認証情報読み取りに hard timeout が必須なら `auth.type=exec` (`auth.timeout_ms` が効く) に切り替えてください。

ccgate はファイルを `O_NONBLOCK` で開くので、誤って FIFO / device を指してしまった場合は早期に return します。ただし NFS が応答を返してこない regular file を指した場合は、kernel I/O の完了まで hook が固まることがあります。

### ファイルパーミッション

ccgate は `auth.path` が次の場合に `slog.Warn` を出します (hard reject はしません):

- `group` または `other` に read bit が立っている (`mode & 0o044`)
- 現在の UID と異なるユーザー所有

推奨は `chmod 0600 <path>` + 親ディレクトリ `chmod 0700` です。warning は情報提供のみ — 緩いパーミッションが意図通りなら無視して構いません。

## 解決順序とプラットフォーム対応

`provider.auth` (設定済み) > `CCGATE_*_API_KEY` > `*_API_KEY`

`auth` を設定している状態で解決に失敗しても、ccgate は env var 経路へ **暗黙に fallback しません**。silent fallback は helper のバグを隠してしまうためです。代わりに `kind=credential_unavailable` で fallthrough し、reason がどの段階で失敗したかを示します (reason の網羅は [docs/ja/configuration.md](configuration.md) を参照)。

`auth` は Unix のみ (Linux / macOS / *BSD) の対応です。Windows でこれを設定すると `reason=unsupported_platform` で fallthrough します。`auth` を設定していない Windows ユーザーは従来通り `*_API_KEY` の env var 経路で動きますが、設定済みの `auth` が unsupported だったときに ccgate が黙って env var に fallback することはありません。

## キャッシュ

- パス: `$XDG_CACHE_HOME/ccgate/<target>/api_key.<sha256[:16]>.json` (target は `claude` / `codex`)。`XDG_CACHE_HOME` 未設定時は `~/.cache/ccgate/<target>/...` にフォールバック
- パーミッション: ディレクトリ `0700`、ファイル `0600`。既存ディレクトリが緩いモードで作られていた場合も `0700` に締め直します
- キャッシュの中身は正規化した `{key, expires_at}` のみ。helper が出力した余分なフィールド (refresh token / broker session ID など) はディスクには残しません
- atomic rename: 一時ファイルを同じディレクトリ内に作って rename で差し替えるので、ファイルシステム跨ぎの問題はありません
- 同時呼び出しは隣接する lock ファイル (`*.lock`) の `flock` で直列化されます。lock ファイルは消されないので、残っていても異常ではありません

### キャッシュキーとアカウント分離

cache fingerprint は `(target, provider.name, base_url, auth.command, auth.cache_key)` のみから作られ、環境変数は **デフォルトでは含まれません**。helper が `$AWS_PROFILE` / `$GCLOUD_ACCOUNT` / `$OP_ACCOUNT` などに依存している場合、`auth.command: 'aws sts ...'` のように literal に書くと、すべてのアカウントで同じキャッシュファイルを共有してしまいます。

アカウントごとに分離する 3 つの方法:

- **`auth.cache_key` に `${VAR}` env 展開を使う (推奨)**: `auth: { type: 'exec', command: 'aws sts ...', cache_key: '${AWS_PROFILE}' }`。ccgate は hook 起動時に `${VAR}` / `$VAR` を実行時 env から展開し、その値を cache fingerprint に加えます。未定義 env (`${AWS_PROFLIE}` typo / 未 export) を参照した場合、ccgate は空文字に潰さず `cache_key_invalid` で fallthrough します — 黙って空 salt にすると分離の目的そのものが失われるためです。リテラル `$` は `$$` で escape
- **jsonnet の `std.native('env')` を使う**: `auth: { type: 'exec', command: 'aws sts ...', cache_key: std.native('env')('AWS_PROFILE') }`。config load 時に同じ効果が得られます。ccgate は `std.native('must_env')` も登録していて、こちらは未定義時に jsonnet 評価エラーになります — runtime fallthrough ではなく config load 時に確実に失敗させたい場合に
- **アカウントをコマンド文字列に直接埋め込む**: `auth.command: 'aws sts assume-role --profile prod ...'`。コマンド文字列が違えばハッシュも別になり、別プロジェクト・別アカウントは別キャッシュになります。env 機構を使わずに済む単純な方法
- **`auth.type=file` をアカウントごとに分ける**: 各アカウントの rotator が専用パスに書き込めば、パス自体で credential が分離します

## セキュリティ上の注意

- `auth.path`: ccgate は permission warning を出しますが mode の正規化はしません。ファイル本体は `chmod 0600`、親ディレクトリは `chmod 0700` を user 側で設定してください
- `auth.command`: コマンド文字列に literal な秘密情報を **直書きしない** こと。文字列は `/bin/sh -c` に渡されるため、`ps` / `/proc/<pid>/cmdline` / 監査ログ / シェル履歴に残ります。秘密情報はファイルや keychain に置き、helper の中で読む形にしてください
- helper の stderr 本文は `ccgate.log` には **書き出されません**。ccgate は stderr をメモリ上限のために内部 capture しますが、log には byte 数と exit error しか残しません。stderr の内容を見たい場合は ccgate のログを覗くのではなく、helper を `2>&1` 付きで手動実行してください
- provider のエラーレスポンス本文は `ccgate.log` / `metrics.jsonl` に到達する前にマスクされます。`anthropic-sdk-go` と `openai-go` はどちらも `Error.Error()` にレスポンス本文を埋め込む実装なので、ccgate 側でこれを `<provider> API error (status N)` の短い要約に置き換えています。proxy がデバッグ用のレスポンス本文に認証情報を含めた場合でも、ログには漏れません

## helper が満たすべき条件

helper は次の条件を満たす必要があります。

- **非対話的** であること (TTY 入力なし、ブラウザを開かない、stdin はクローズ済みで起動される)
- **デーモン化しない** こと: process group の外に fork するとタイムアウト時の kill が効きません
- stdout には **認証情報のみ** を書く。診断出力は stderr に。ただし stderr にも秘密情報は書かないこと (運用者によっては stderr を取り込むため)
- plain string モードでは、trim 後の stdout が単一行の非空文字列であること。複数行の出力は `invalid_plain_output` で拒否されます
- 同じ `(auth.command, provider.name, base_url, auth.cache_key)` の組み合わせに対しては **決定論的** であること: 同じ設定で動く 2 つの呼び出しは、認証情報が指す対象を一致させてください

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

`chmod 700 ~/bin/ccgate-key-passthrough.sh` してから `auth: { type: 'exec', command: '~/bin/ccgate-key-passthrough.sh' }` を設定。ccgate は呼び出しごとにこれを実行し (plain string なのでキャッシュなし)、env の値を SDK に渡します。

### JSON + expiry: broker からキャッシュ

実運用の broker が期限付き認証情報を発行する場合、`{key, expires_at}` で wrap して ccgate にキャッシュ + 期限直前更新をさせます。token に `"` / `\` / 改行が含まれても壊れないよう `jq` で組み立てます。

```sh
#!/bin/sh
# ~/bin/ccgate-key-broker.sh
set -eu
TOKEN=$(my-key-broker --provider anthropic) # API キーを stdout に出すコマンド
# refresh_margin_ms (デフォルト 60000) に余裕を持たせるため、broker の TTL より少し短めにする
EXP=$(date -u -v+50M +%FT%TZ 2>/dev/null || date -u -d '+50 minutes' +%FT%TZ)
jq -nc --arg key "$TOKEN" --arg expires_at "$EXP" '{key:$key, expires_at:$expires_at}'
```

`auth: { type: 'exec', command: '~/bin/ccgate-key-broker.sh' }` で指す。先に単独で動作確認 (`~/bin/ccgate-key-broker.sh | jq .` で valid な JSON object が返る) を取ってから ccgate に渡します。

### `cache_key` で AWS profile を分離

同じ broker コマンドが AWS profile ごとに別の認証情報を返す場合は、`cache_key` を使ってプロファイルごとに別キャッシュにします。

```jsonnet
{
  provider: {
    name: 'anthropic',
    auth: {
      type: 'exec',
      command: 'aws-sts-broker --provider anthropic',
      cache_key: '${AWS_PROFILE}',
    },
  },
}
```

これで `AWS_PROFILE=prod` と `AWS_PROFILE=dev` を切り替えると、別々のキャッシュファイル (`api_key.<hash-prod>.json` と `api_key.<hash-dev>.json`) になり、上書きが起きません。`AWS_PROFILE` が未設定 / typo していた場合、ccgate は `reason=cache_key_invalid` で fallthrough して設定ミスを気付かせます — 黙って同じキャッシュを共有しません。

### `auth.type=file` の rotator: hot-path 上で helper を呼ばない

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

そして `auth: { type: 'file', path: '~/.config/my-broker/anthropic.json' }` を指す。ccgate は呼び出しごとにファイルを読むだけで、内部キャッシュは持ちません — ローテーションの責務は rotator 側です。

## provider が 401/403 を返したときの挙動

ccgate がたった今使った認証情報を provider が拒否した場合、HTTP status のみで挙動を決めます。

| HTTP status         | `auth.type=exec`                              | `auth.type=file`                          | env var      |
|---------------------|-----------------------------------------------|-------------------------------------------|--------------|
| 401 / 403           | `provider_auth`、**キャッシュ削除 + fallthrough** | `provider_auth`、fallthrough のみ (cache 無し) | **exit 1**   |
| 5xx / network / 429 | exit 1 (従来通り)                              | exit 1                                    | exit 1       |

env var 経路で 401 / 403 を exit 1 にする理由は、ccgate 側に env を rotate する手段がなく、黙って飲むと user 側の設定ミスを隠してしまうためです。

## AWS `credential_process` との差分

出力形式は AWS `credential_process` に意図的に近づけています。そのため、既存の helper は薄いラッパーを 1 枚挟むだけで流用しやすくなっています。一方、**ccgate は helper の出力をディスクにキャッシュする** 設計です。AWS CLI が呼び出しのたびに helper を再実行するのとは違い、hook の実行経路での遅延を抑えるためのトレードオフです。

キャッシュさせたくない broker の場合は、`expires_at` を含めない JSON (`{"key":"..."}`) を返せば毎回再実行されます。

## 障害時の復旧チェックリスト

何かおかしいときは:

1. `ccgate.log` (`$XDG_STATE_HOME/ccgate/<target>/ccgate.log`) を tail して `kind=credential_unavailable` のエントリを探し、`reason` と `source` (`exec` / `file` / `cache` / `lock`) attribute を確認。どの段階で失敗したかが分かります
2. `ccgate <target> metrics` を実行し、**Credential failures** セクションで `(source, reason)` 別の集計を確認
3. キャッシュ起因 (`cache_parse` / `cache_read` / `cache_write` の log warning) が疑わしい場合は `$XDG_CACHE_HOME/ccgate/<target>/api_key.*.json` を削除して再生成させます。隣接する `*.lock` は再利用されるので削除不要です
4. `cache_key_invalid` が出続ける場合は、`auth.cache_key` で参照している env が hook の実行環境にセットされているか確認してください。hook は upstream tool (Claude Code / Codex CLI) の env を継承するため、shell の dotfiles が source されているとは限りません
5. `expired` が出続ける場合は helper の `expires_at` と `date -u` を比較してください。helper 内部の TTL ロジックや時計ズレが原因のことが多いです。`refresh_margin_ms` が helper の TTL より大きいときも同じ症状になります
6. `provider_auth` がキャッシュ削除しても繰り返される場合、helper 自体が provider に拒否される認証情報を生成しています。`/bin/sh -c "$your_command"` を手で実行し、helper が出力する stdout が SDK に渡っているのと同じ内容かを確認してください

reason の完全な分類 (metrics に乗るものと、log のみで出るキャッシュ層 warning の違い) は [docs/ja/configuration.md](configuration.md#credential_unavailable-の-reason-値) を参照してください。
