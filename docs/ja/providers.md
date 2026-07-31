# ccgate -- Providers

[English version (docs/providers.md)](../providers.md)

ccgate は各 PermissionRequest を provider LLM (default: Claude Haiku) に投げて分類します。本ページは、対応 provider・切り替え方・API キーの解決順・互換 proxy 経由での利用を扱います。Refresh される credential (`provider.auth`) については [docs/ja/api-key-helper.md](api-key-helper.md) を参照。

## 対応 provider

| `provider.name` | underlying SDK | 既定モデル                          |
|-----------------|----------------|-------------------------------------|
| `anthropic`     | `anthropic-sdk-go` | `claude-haiku-4-5`               |
| `openai`        | `openai-go`        | (`provider.model` で明示指定が必要) |
| `gemini`        | Gemini の OpenAI 互換 endpoint 経由で `openai-go` | (同じく明示指定が必要) |

## Provider の切り替え

任意の layer で `provider.name` (必要なら `provider.model` も) を書き換えます:

```jsonnet
{
  provider: {
    name: 'openai',
    model: 'gpt-5.6-luna',
  },
}
```

対応する API キー (下の [API キー](#api-キー) 参照) を export してください。キーが見つからない場合 ccgate は上流ツールの確認画面に fallthrough するので、 provider 切替で hook が壊れることはありません。

## API キー

`CCGATE_*_API_KEY` が優先され bare 変数を上書きします。 AI ツール本体の API キーと ccgate 用キーを分けられます。

| `provider.name` | 優先                       | フォールバック        | API キー発行ページ |
|-----------------|----------------------------|-----------------------|--------------------|
| `anthropic`     | `CCGATE_ANTHROPIC_API_KEY` | `ANTHROPIC_API_KEY`   | <https://platform.claude.com/settings/keys> |
| `openai`        | `CCGATE_OPENAI_API_KEY`    | `OPENAI_API_KEY`      | <https://platform.openai.com/api-keys>      |
| `gemini`        | `CCGATE_GEMINI_API_KEY`    | `GEMINI_API_KEY`      | <https://aistudio.google.com/app/api-keys>  |

全体の解決順: `provider.auth` (設定済み) → `CCGATE_*_API_KEY` → `*_API_KEY`。`provider.auth` を設定した状態で失敗しても env var に silent に fallback はせず、 `kind=credential_unavailable` で fallthrough します。 helper の契約と復旧手順は [docs/ja/api-key-helper.md](api-key-helper.md) を参照。

## モデル選択

ccgate は structured output でリクエストを送るので、それに対応するモデルを選んでください。sampling パラメータには触れません (`temperature` を設定しません) ので、各モデルの既定値がそのまま使われます。

provider モデル一覧:

- Anthropic: <https://docs.anthropic.com/en/docs/about-claude/models/overview>
- OpenAI: <https://platform.openai.com/docs/models>
- Gemini: <https://ai.google.dev/gemini-api/docs/models>

## `reasoning_effort`

tool 呼び出し 1 件の分類に reasoning は要らないので、ccgate は既定で reasoning させません。モデル任せは中立ではなく、現行モデルは既定で reasoning するため、判定のたびに latency が乗り、判定結果そのものに要る output token を消費します。

`provider.reasoning_effort` は provider をまたいだ 1 つの設定で、それぞれの API が表現できる範囲で最も少ない reasoning を要求します。

| 値 | `openai` | `gemini` | `anthropic` |
|---|---|---|---|
| 未指定 (= `none`) | `reasoning_effort: "none"` | `reasoning_effort: "minimal"` | `thinking: {type: "disabled"}` |
| `""` | 何も送らない | 何も送らない | 何も送らない |
| それ以外 | `reasoning_effort` にそのまま | `reasoning_effort` にそのまま | `thinking: {type: "adaptive"}` + `output_config.effort` |

2 つの provider は "none" をそのまま言えません。Anthropic にはその effort level が無く (`output_config.effort` は `low`/`medium`/`high`/`xhigh`/`max`)、Claude の思考を止めるパラメータは `thinking` なので `none` はそちらを無効化します。Gemini は現行モデルで [reasoning を切れない](https://ai.google.dev/gemini-api/docs/openai) ので、`none` は互換レイヤの下限である `minimal` を要求します。

**値の検証はしません。** `provider.name` はどちらのプロトコルを喋るかを選ぶだけで、`base_url` でその接続先は何にでも向けられます。どの level が意味を持つかを知っているのは接続先だけで、複数モデルを束ねる proxy がどの first-party API にも無い level を受理することもあります。ccgate が解釈するのは `""` と `none` の 2 つだけで、それ以外はタイポも含めて書いたとおりに送ります。

したがって、どの値が通るかはモデルの世代ごとに狭く、確認する責任は設定する側にあります。reasoning パラメータを持たないモデルはどの値も拒否しますし、reasoning モデルでも `low` は受けるが `none` は受けない、Anthropic には `minimal` が無い、といった具合です。拒否された場合は provider の 400 をそのまま添えて hook が異常終了し、その本文はたいてい受理可能な値を列挙しています。log には設定値 `provider.reasoning_effort` が並記されるので、上の表と突き合わせて実際の送信内容を確認してください。

そのモデルが受理する値を設定してください。

```jsonnet
{
  provider: {
    name: 'openai',
    model: 'a-reasoning-model',
    reasoning_effort: 'low',
  },
}
```

reasoning パラメータを持たないモデルなら、送信自体をやめます。

```jsonnet
{
  provider: {
    name: 'openai',
    model: 'a-non-reasoning-model',
    reasoning_effort: '',
  },
}
```

> [!NOTE]
> reasoning token は判定結果と同じ output の枠を食います。effort を上げるとモデルが枠を思考で使い切って応答が truncate され、ccgate はそれを unusable として fallthrough します。

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
