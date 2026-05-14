# ccgate -- Providers

[English version (docs/providers.md)](../providers.md)

ccgate は各 PermissionRequest を provider LLM (default: Claude Haiku) に投げて分類します。本ページは、対応 provider・切り替え方・API キーの解決順・互換 proxy 経由での利用を扱います。Refresh される credential (`provider.auth`) については [docs/ja/api-key-helper.md](api-key-helper.md) を参照。

## 対応 provider

| `provider.name` | underlying SDK | 既定モデル                          |
|-----------------|----------------|-------------------------------------|
| `anthropic`     | `anthropic-sdk-go` | `claude-haiku-4-5`               |
| `openai`        | `openai-go`        | (`provider.model` で明示指定が必要) |
| `gemini`        | Gemini の OpenAI 互換 endpoint 経由で `openai-go` | (同じく明示指定が必要) |
| `codex-oauth`   | Codex app-server (stdio JSON-RPC) | (明示指定、例: `gpt-5.4`) |

## Provider の切り替え

任意の layer で `provider.name` (必要なら `provider.model` も) を書き換えます:

```jsonnet
{
  provider: {
    name: 'openai',
    model: 'gpt-4o-mini',
  },
}
```

対応する API キー (下の [API キー](#api-キー) 参照) を export してください。キーが見つからない場合 ccgate は上流ツールの確認画面に fallthrough するので、 provider 切替で hook が壊れることはありません。

ChatGPT サブスクリプションで Codex を使う場合は `codex-oauth` を使います:

```jsonnet
{
  provider: {
    name: 'codex-oauth',
    model: 'gpt-5.4',
  },
}
```

`codex-oauth` は分類ごとに `codex app-server` を起動し、Codex 側の ChatGPT login を使います。既定では `$XDG_STATE_HOME/ccgate/codex-oauth/codex-home` (未設定なら `~/.local/state/ccgate/codex-oauth/codex-home`) を専用 `CODEX_HOME` として使い、そこに `cli_auth_credentials_store = "file"` の Codex `config.toml` を作ります。初回だけ次でログインしてください:

```sh
CODEX_HOME="${XDG_STATE_HOME:-$HOME/.local/state}/ccgate/codex-oauth/codex-home" codex login
```

普段使いの `~/.codex` などを意図的に再利用したい場合は `provider.codex_home`、`codex` が `PATH` にない場合は `provider.codex_bin` を設定します。`codex-oauth` の子プロセスからは `CODEX_API_KEY` / `OPENAI_API_KEY` / `CCGATE_OPENAI_API_KEY` を除去するため、API key 課金へ silent に落ちることはありません。

## API キー

`CCGATE_*_API_KEY` が優先され bare 変数を上書きします。 AI ツール本体の API キーと ccgate 用キーを分けられます。

| `provider.name` | 優先                       | フォールバック        | API キー発行ページ |
|-----------------|----------------------------|-----------------------|--------------------|
| `anthropic`     | `CCGATE_ANTHROPIC_API_KEY` | `ANTHROPIC_API_KEY`   | <https://platform.claude.com/settings/keys> |
| `openai`        | `CCGATE_OPENAI_API_KEY`    | `OPENAI_API_KEY`      | <https://platform.openai.com/api-keys>      |
| `gemini`        | `CCGATE_GEMINI_API_KEY`    | `GEMINI_API_KEY`      | <https://aistudio.google.com/app/api-keys>  |
| `codex-oauth`   | (なし)                     | (なし)                | ChatGPT で `codex login`                    |

全体の解決順: `provider.auth` (設定済み) → `CCGATE_*_API_KEY` → `*_API_KEY`。`provider.auth` を設定した状態で失敗しても env var に silent に fallback はせず、 `kind=credential_unavailable` で fallthrough します。 helper の契約と復旧手順は [docs/ja/api-key-helper.md](api-key-helper.md) を参照。

`codex-oauth` は例外で、`provider.auth` や API key env var を使いません。ChatGPT OAuth の保存と token refresh は Codex app-server / CLI の login cache に委譲します。

## モデル選択

ccgate は structured output と `temperature=0` (決定論的な分類) でリクエストを送ります。どちらにも対応するモデルを選んでください。

reasoning 系のモデルは別のパラメータ shape を要求することが多く、 分類タスクには不要な chain-of-thought 遅延が乗ります。 `temperature=0` や structured output を拒否するモデルでは ccgate は loop せず上流ツールの確認画面に fallthrough しますが、 同じモデルで判定が成立することはありません。 特定モデルを採用する前に provider のモデル一覧で `temperature=0` + structured output 対応を確認してください。 provider モデル一覧:

- Anthropic: <https://docs.anthropic.com/en/docs/about-claude/models/overview>
- OpenAI: <https://platform.openai.com/docs/models>
- Gemini: <https://ai.google.dev/gemini-api/docs/models>

## `base_url` と互換 proxy

ccgate は各 provider SDK の標準 chat / messages エンドポイントを使うので、**OpenAI 互換 / Anthropic 互換** の任意の endpoint — [LiteLLM proxy](https://docs.litellm.ai/docs/proxy/quick_start) / Azure OpenAI / オンプレ gateway / 地域別 endpoint など — に `provider.base_url` を向ければ動きます。

`provider.base_url` は underlying SDK の `WithBaseURL` にそのまま渡されるので、書く path は **その SDK の慣習** に従います (ccgate 側で正規化しません):

| `provider.name` | SDK default base URL                                       | `base_url` に書く形                                    |
|-----------------|------------------------------------------------------------|--------------------------------------------------------|
| `openai`        | `https://api.openai.com/v1/`                               | host **+ `/v1`** (SDK が `chat/completions` を追加)    |
| `anthropic`     | `https://api.anthropic.com/`                               | host root のみ (SDK が `/v1/messages` を追加)          |
| `gemini`        | `https://generativelanguage.googleapis.com/v1beta/openai/` | override するなら host **+ `/v1beta/openai`**          |

### OpenAI 互換 endpoint

`/v1/chat/completions` を expose する proxy (LiteLLM proxy / Azure OpenAI の OpenAI 互換モード 等):

```jsonnet
{
  provider: {
    name: 'openai',
    model: 'anthropic/claude-haiku-4-5', // proxy が公開している名前
    base_url: 'https://your-proxy.example/v1',
  },
}
```

proxy の API キーを `CCGATE_OPENAI_API_KEY` で export。OpenAI SDK は base URL に `/chat/completions` を直接 append するので、末尾の `/v1` が必要。

### Anthropic 互換 endpoint

`/v1/messages` を expose する proxy:

```jsonnet
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    base_url: 'https://your-proxy.example',
  },
}
```

proxy の API キーを `CCGATE_ANTHROPIC_API_KEY` で export。 Anthropic SDK が `/v1/messages` を自分で append するので、base URL は host root で止めます。

## 関連

- [docs/ja/api-key-helper.md](api-key-helper.md) — `provider.auth` (refresh される credential、 helper 契約、 401/403 挙動、 障害復旧)
- [docs/ja/configuration.md](configuration.md) — 設定 layering、 全フィールドリファレンス、 fallthrough_strategy、 metrics
