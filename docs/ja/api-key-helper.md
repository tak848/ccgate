# Refresh される credential

[English version (docs/api-key-helper.md)](../api-key-helper.md)

provider に渡す credential が静的な環境変数では追いつかない頻度で入れ替わる場合 (AWS STS セッション、Vertex ADC、OpenAI 互換 gateway の virtual key、社内 key broker など) は、`*_API_KEY` の代わりに `provider.auth` で取得します。

`provider.auth` には 3 つのモードがあります。

- **`type: 'exec'`**: ccgate がシェルコマンドを実行し、その stdout を credential として使う。
- **`type: 'file'`**: 外部のローテーターが書いたファイルを ccgate が読む。
- **`type: 'profile'`**: Anthropic 専用。`ant auth login` の profile を anthropic-sdk-go に渡し、refresh は SDK 自身が担当する。[Profile ベース認証](#profile-ベース認証-anthropic-のみ) を参照。

(`*_API_KEY` env var は `provider.auth` が省略されている場合の別経路。`auth` のモードではありません。)

`exec` の helper を動かすシェルは `auth.shell` で選択 (default `bash`)。詳細は [helper の契約](#helper-の契約) を参照。

## 設定

```jsonnet
// helper コマンド
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    auth: {
      type: 'exec',
      command: '/usr/local/bin/my-key-broker --provider anthropic',
      refresh_margin_ms: 60000,  // 任意、default 60000
      timeout_ms: 30000,         // 任意、default 30000
    },
  },
}

// 外部ローテーターがファイルに書き込む (path 省略時は
// $XDG_STATE_HOME/ccgate/<target>/auth_key.json)
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    auth: {
      type: 'file',
      path: '~/.config/my-broker/anthropic.json',
      refresh_margin_ms: 60000,  // 任意、default 60000
    },
  },
}
```

| 項目 | 型 | 既定値 | 説明 |
|---|---|---|---|
| `auth.type` | `"exec"` / `"file"` / `"profile"` | (`auth` を書くなら必須) | 取得モード。`profile` は Anthropic 専用、[Profile ベース認証](#profile-ベース認証-anthropic-のみ) を参照 |
| `auth.command` | string | `""` | (`exec` 専用、必須) シェルコマンド。stdout が認証情報 |
| `auth.shell` | `"bash"` / `"powershell"` | `"bash"` | (`exec` 専用) `auth.command` を実行するシェル。`powershell` は `pwsh` を優先解決、無ければ `powershell` に fallback |
| `auth.path` | string | `$XDG_STATE_HOME/ccgate/<target>/auth_key.json` | (`file` 専用) 認証情報ファイルのパス。省略でデフォルトを使用 |
| `auth.profile` | string | `""` | (`profile` 専用) Anthropic profile 名 (`ant auth login --profile <name>` が `<config_dir>/credentials/<name>.json` に書く値)。空 / 省略時は SDK が `$ANTHROPIC_PROFILE` → `<config_dir>/active_config` → `"default"` を解決 |
| `auth.refresh_margin_ms` | int (ms) | `60000` | `expires_at` の何 ms 前で期限切れ扱いにするか。`0` で無効。(`exec` / `file` 専用 — `profile` は SDK が refresh を担当するので無視) |
| `auth.timeout_ms` | int (ms) | `30000` | Resolve 1 回の上限。`> 0`。(`exec` / `file` 専用) |
| `auth.cache_key` | string | `""` | (`exec` 専用) cache fingerprint に加える salt。[アカウント分離](#アカウント分離) 参照 |

`auth.command` / `auth.path` の相対パスは hook 起動時のカレントディレクトリから解決します (設定ファイルのあるディレクトリではありません)。

認証情報の解決順は `provider.auth` (設定済みのとき) > `CCGATE_*_API_KEY` > `*_API_KEY` です。`auth` を設定している状態で解決に失敗しても env var には fallback しません。`kind=credential_unavailable` で fallthrough します。

## helper の出力

helper は次のどちらかの形を stdout (もしくは `auth.type=file` ならファイル中身として) に書きます。

- **JSON**: `{"key": "sk-...", "expires_at": "2026-05-04T01:23:45Z"}`。`key` は必須、`expires_at` は RFC3339 で任意。トップレベルの未知フィールドは受け付けますが捨て、SDK にもキャッシュにも `{key, expires_at}` だけが渡ります。
- **plain string**: 改行を含まない単一行の非空文字列。前後の空白を trim して渡します (複数行は不可)。

64 KiB を超える出力は拒否します。ファイルの中身にも同じ上限が適用されます。

## helper の契約

helper は次を満たす必要があります。

- stdout には **認証情報のみ** を書く。診断出力は stderr に書き、stderr にも秘密情報は出さないこと。
- 同じ `(shell, command, provider.name, base_url, cache_key)` の組に対して **決定論的** に振る舞う。同じ設定で 2 回呼んだら同じ意味の認証情報を返すこと。
- **デーモン化しない**。process group の外に fork するとタイムアウト時の kill が効きません。
- `auth.timeout_ms` 以内に終了する。
- `auth.command` 文字列に **literal な秘密情報を直接書かない**。文字列は設定したシェル (`bash -c <command>`、または `auth.shell: 'powershell'` のときは `pwsh -Command <command>` / `powershell -Command <command>`) に渡されるため、 process listing やシェル履歴に残ります。秘密情報はファイルや keychain に置き、 helper の中で読み出してください。

ccgate は helper の env に `CCGATE_API_KEY_RESOLUTION=1` を入れるので、helper が ccgate を再帰起動していないかを自分で検知できます。それ以外の環境変数 (`*_API_KEY` 含む) はそのまま継承します。stdin は閉じています (helper から親ターミナルの入力は読めません)。

### 初回ブラウザ認証

`gcloud auth print-access-token`、 `aws sso login`、 社内 SSO 経由の key broker など、 初回起動時にブラウザが開いて OAuth / SAML 認証 → 完了後 stdout に認証情報を出すタイプの helper も使えます。 2 回目以降はローカルにキャッシュされた refresh token が使われ、 silent に完了します。

既定の `auth.timeout_ms` (`30000`) は非対話的な helper の大半をカバーします。ブラウザでユーザーが同意画面を操作するタイプは `120000` 程度まで上げてください。一定時間アイドル後の最初の Permission Request がブラウザ操作の完了まで待つ形になりますが、`reason=timeout` で fallthrough しなくなります。

## Profile ベース認証 (Anthropic のみ)

Anthropic provider 専用です。公式 `ant` CLI (`ant auth login` でブラウザ OAuth、`ant profile activate <name>` で active profile を切り替え) が `<config_dir>/credentials/<name>.json` (mode 0600) に credentials を書き出します。`<config_dir>` の既定は `~/.config/anthropic`。Anthropic の profile 解決順 (`$ANTHROPIC_PROFILE` → `<config_dir>/active_config` → `"default"`) は Go SDK / Claude Code / Claude Agent SDK で **共有** されます ([wif-reference doc](https://platform.claude.com/docs/en/api/authentication/wif-reference))。access token の refresh は SDK 自身が行うため、ccgate は credential 経路には立ち入りません — ただし SDK の static-credential 用 env autoload (`ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN`) は無効化するので、残っている env var が、宣言した profile を silent に上書きすることはありません。Workload Identity Federation は `authentication.type=oidc_federation` を含む profile config 経由のみ対応。

### Quick start

```sh
# `ant` CLI を任意の方法で入れる (例: `mise use -g aqua:anthropics/anthropic-cli`、
# `aqua g -i anthropics/anthropic-cli`、 upstream の release ページから直接 download など)。
ant auth login --profile ccgate         # ブラウザが開き ~/.config/anthropic/credentials/ccgate.json を書き出す
# ccgate.jsonnet に追記:
#   provider: { ..., auth: { type: 'profile', profile: 'ccgate' } }
# tail -f $XDG_STATE_HOME/ccgate/<target>/ccgate.log
#   → 期待: rg 'credential source selected.*source=profile.*profile_name_set=true'
```

> [!IMPORTANT]
> **`ant auth login` は profile 名に関わらず `<config_dir>/active_config` を書き換える** 仕様です。ant は `--profile <name>` 指定時はその `<name>` に、`--profile` 省略時は `default` に、`<config_dir>/active_config` を毎回書き換えます。Anthropic の profile 解決順は Claude Code / Claude Agent SDK と共有されるため、この書き換えで Claude Code の credential 経路が subscription から従量課金 API に移ることがあります。対応:
>
> - `default` 以外の名前 (例: `ccgate`) で profile を作成し、ccgate.jsonnet で明示宣言する。 `ant auth login` を `--profile` 指定なしで実行すると `default` profile が作られ、 既定の参照先として使われるため、 これを避ける。
> - `ant auth login` の後は `<config_dir>/active_config` の参照先を戻す。 2 つの選択肢:
>   - `rm <config_dir>/active_config` で参照先を完全に消す (SDK は `default` 解決に戻る)
>   - 普段使う profile があれば `ant profile activate <previous-profile-name>`
> - 同じ profile を別 org / workspace に関連付け直す場合: ant は既に関連付け済みの profile への再 login を拒否するので、 `<config_dir>/configs/<name>.json` を削除して `ant auth login --profile <name>` を再実行する。

## 例

### env var に既にある認証情報をラップする

最も単純な helper は env var の値を出すだけです。本格的な broker を組む前に、解決経路の動作確認に便利です。

```sh
#!/bin/sh
# ~/bin/ccgate-key-passthrough.sh
set -eu
printf '%s' "${ANTHROPIC_API_KEY:?ANTHROPIC_API_KEY is not set}"
```

```jsonnet
auth: { type: 'exec', command: '~/bin/ccgate-key-passthrough.sh' }
```

plain string の出力はキャッシュされず、hook 起動のたびに helper が再実行されます。

### broker 経由でキャッシュさせる

実際の broker が期限付き認証情報を発行する場合は、`{key, expires_at}` 形式に整えて返します。`jq` で組み立てれば、token に `"`、`\`、改行が混じっても安全です。

```sh
#!/bin/sh
# ~/bin/ccgate-key-broker.sh
# 出力: {"key": "...", "expires_at": "<RFC3339>"} の 1 行 JSON。
# `my-broker-expiry-rfc3339` は「now + helper lifetime」を RFC3339 文字列で返す任意のコマンドに置き換えてください。
set -eu
TOKEN=$(my-key-broker --provider anthropic)
EXP=$(my-broker-expiry-rfc3339)
jq -nc --arg key "$TOKEN" --arg expires_at "$EXP" '{key:$key, expires_at:$expires_at}'
```

```jsonnet
auth: { type: 'exec', command: '~/bin/ccgate-key-broker.sh' }
```

ccgate に渡す前に `~/bin/ccgate-key-broker.sh | jq .` 等で単体動作を確認してください。

### 外部ローテーター (hook 経路で helper を回さない)

hook の hot path で helper を回したくない場合は、外部ローテーターから同じ JSON 形を **atomic replace** (`tmp に書き込み` → `chmod 0600 tmp` → `rename tmp <auth.path>`) で `auth.path` に置きます。ローテートはローテーター側で担当し、 ccgate は hook 起動のたびに file を読むだけです。

```jsonnet
auth: { type: 'file', path: '~/.config/my-broker/anthropic.json' }
```

## キャッシュ

`auth.type=exec` で `expires_at` が未来の JSON が返ってきた場合、内容を `$XDG_CACHE_HOME/ccgate/<target>/api_key.<sha256[:16]>.json` (ディレクトリ `0700`、ファイル `0600`) に保存します。`now + auth.refresh_margin_ms >= expires_at` になった時点でキャッシュは stale 扱いになり、次回の hook 起動で helper を再実行します。並列で起動した hook は隣接ロックファイル (`*.lock`) の `flock` で直列化され、helper は 1 回だけ走ります。

`expires_at` のない JSON、plain string 出力、`auth.type=file` は ccgate 側ではキャッシュしません。

## アカウント分離

cache fingerprint には `auth.cache_key` も含まれるので、同じ `auth.command` でも `cache_key` が違えばキャッシュファイルが分かれます。1 つの helper コマンドが AWS profile / GCP account ごとに別の認証情報を返す場合に使います。

```jsonnet
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    auth: {
      type: 'exec',
      command: 'aws-sts-broker --provider anthropic',
      cache_key: std.native('must_env')('AWS_PROFILE'),
    },
  },
}
```

ccgate は config 評価時に env を読む jsonnet ヘルパーを 2 つ register しています。

- `std.native('env')(name)`: 値が未設定なら空文字を返します。
- `std.native('must_env')(name)`: 値が未設定なら jsonnet 評価エラーで落ちます。

または、コマンド文字列にアカウントを直接埋め込む (`aws sts ... --profile prod`) と、コマンド文字列違いで自動的に別キャッシュになります。`auth.type=file` でアカウントごとに別パスを使うのも同じ効果です。

cache fingerprint には **カレントディレクトリもホスト名も含まれません**。具体的には、**同じ repo の別 checkout、別 repo であっても `(provider.name, base_url, shell, command, cache_key)` が一致する設定はすべて 1 つのキャッシュファイルを共有**します。`$XDG_CACHE_HOME` が同期ディレクトリを指していれば別マシン間でも同じです。普段はこれが望ましい (checkout ごとに毎回再取得するのは無駄) ので default で共有しています。分離したいときに `cache_key` で分けてください。

- checkout ごと: `cache_key: std.native('env')('PWD')`
- ホストごと: `cache_key: std.native('env')('HOSTNAME')`
- env 経由でアカウント別: `cache_key: std.native('must_env')('AWS_PROFILE')`

`cache_key` を空のままにするのは「同じ helper コマンドを使うすべての checkout / repo / host で credential を共有する」を **明示的に選択した** 状態です。

## ファイル経路の注意点

`auth.path` の読み取りも exec 経路と同じく `auth.timeout_ms` (default 30000) で上限が決まります — 応答しない file source では `reason=timeout` で fallthrough します (hook はブロックしません)。 ネットワーク経由や仮想 file source に置くと毎 fire `timeout_ms` 分待つコストが発生するので、 user 専用のローカル path を強く推奨します。

`auth.path` か cache file が現在の user 以外でも読める / 所有者が違う場合、ccgate は `slog.Warn` を出します (拒否はしません)。これは security nudge であり policy enforcement ではなく、ファイルシステムから取得できる "world-readable" 指標だけを確認し、effective access の計算は行いません。推奨は user 専用に読めるファイルを user 専用に読めるディレクトリ配下に置く構成です。

## provider が 401/403 を返した場合の挙動

provider が認証情報を拒否した場合、HTTP status のみで挙動が決まります。

| HTTP status         | `auth.type=exec`                                  | `auth.type=file`                          | `auth.type=profile`                                                 | env var      |
|---------------------|---------------------------------------------------|-------------------------------------------|---------------------------------------------------------------------|--------------|
| 401 / 403           | `provider_auth`、**キャッシュ削除して fallthrough** | `provider_auth`、fallthrough のみ (cache 無し) | `provider_auth`、fallthrough (SDK の refresh-token loop が credential を保有、ccgate cache 無し) | **exit 1**   |
| 5xx / 429 / network | exit 1 (従来通り)                                  | exit 1                                    | exit 1                                                              | exit 1       |

env 経路で 401 / 403 を exit 1 にしているのは、ccgate 側で env を rotate する手段がないためです。黙って飲み込むとユーザー側の設定ミスを隠してしまいます。

キャッシュさせたくない場合は `expires_at` を含めない JSON (または plain string) を返せば、helper は毎 fire 再実行されます。

## 障害時の復旧チェックリスト

1. `tail $XDG_STATE_HOME/ccgate/<target>/ccgate.log` で `kind=credential_unavailable` のエントリを探します。`reason` と `source` (`exec` / `file` / `cache` / `lock`) でどの段階の失敗かが分かります。
2. `ccgate <target> metrics` を実行し、**Credential failures** セクションで `(source, reason)` 別の集計を確認します。
3. キャッシュ起因 (`cache_parse` / `cache_read` / `cache_write` の log warning) が疑わしい場合は `$XDG_CACHE_HOME/ccgate/<target>/api_key.*.json` を削除して再生成させます。隣接する `*.lock` は再利用するので残しておいてください。
4. `expired` が出続ける場合は helper の `expires_at` と `date -u` を比較してください。helper 側の TTL ロジックや時計ズレが原因のことが多いです。
5. 新しい環境で `command_exit` が出る場合は、まず `auth.shell` で指定したシェルが `$PATH` にあるかを確認してください。`auth.shell: 'bash'` なら `bash` が、`auth.shell: 'powershell'` なら `pwsh` (優先) または `powershell` のどちらか一方が `$PATH` で解決できる必要があります。両方とも見つからない場合、`os/exec` の lookup エラーとして `command_exit` で現れます。
6. キャッシュを削除しても `provider_auth` が繰り返される場合は、helper 自体が provider に拒否される認証情報を生成しています。ccgate と同じシェルで手動実行してください — `bash -c "$your_command"`、`auth.shell: 'powershell'` の場合は `pwsh -Command "$your_command"` / `powershell -Command "$your_command"`。SDK に渡された stdout を直接確認します。
7. `profile_load` (`auth.type=profile`) の場合、slog の `error_class` で原因を絞れます:
   - `profile_config_missing`: `ant auth login --profile <name>` で profile を作成。
   - `profile_config_parse` / `profile_config_invalid`: `<config_dir>/configs/<name>.json` を mode `0644` に戻すか、削除して `ant auth login --profile <name>` で作り直す。
   - `credentials_missing`: `ant auth login --profile <name>` で credentials を発行。
   - `credentials_stat_failed`: credentials file または親 dir の `os.Stat` が "missing" 以外で失敗 (権限が典型)。`<config_dir>/credentials/` を mode 0700 に戻す。
8. `ant auth login` の後は `ant auth status` で `<config_dir>/active_config` を確認してください。Claude Code が想定外の profile を使い始めたら `rm <config_dir>/active_config` で参照先を消すか、`ant profile activate <previous-profile>` で戻します。

reason の網羅は [configuration.md](configuration.md#credential_unavailable-の-reason-値) にあります。
