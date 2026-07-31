# ccgate -- Providers

[日本語版 (docs/ja/providers.md)](ja/providers.md)

ccgate calls a provider LLM (default: Claude Haiku) to classify each PermissionRequest. This page covers which providers ccgate supports, how to switch between them, where the API keys come from, and how to route through a compatible proxy. For refreshable / rotating credentials (`provider.auth`), see [docs/api-key-helper.md](api-key-helper.md).

## Supported providers

| `provider.name` | Underlying SDK | Default model        |
|-----------------|----------------|----------------------|
| `anthropic`     | `anthropic-sdk-go` | `claude-haiku-4-5` |
| `openai`        | `openai-go`        | (set explicitly via `provider.model`) |
| `gemini`        | `openai-go` against Gemini's OpenAI-compat endpoint | (set explicitly) |

## Switching providers

Set `provider.name` (and `provider.model` when needed) in any layer:

```jsonnet
{
  provider: {
    name: 'openai',
    model: 'gpt-5.6-luna',
  },
}
```

Export the matching API key (see [API keys](#api-keys) below). If the key is missing, ccgate falls through to the upstream tool's permission prompt, so a wrong provider name cannot break the hook.

## API keys

`CCGATE_*_API_KEY` is the preferred name and overrides the bare variant, so ccgate's key can stay separate from the AI tool's own key.

| `provider.name` | Preferred                  | Fallback             | Get a key |
|-----------------|----------------------------|----------------------|------------|
| `anthropic`     | `CCGATE_ANTHROPIC_API_KEY` | `ANTHROPIC_API_KEY`  | <https://platform.claude.com/settings/keys> |
| `openai`        | `CCGATE_OPENAI_API_KEY`    | `OPENAI_API_KEY`     | <https://platform.openai.com/api-keys>      |
| `gemini`        | `CCGATE_GEMINI_API_KEY`    | `GEMINI_API_KEY`     | <https://aistudio.google.com/app/api-keys>  |

Resolution order overall: `provider.auth` (when set) → `CCGATE_*_API_KEY` → `*_API_KEY`. When `provider.auth` is set and fails, ccgate does **not** silently fall back to env vars — the hook falls through with `kind=credential_unavailable`. See [docs/api-key-helper.md](api-key-helper.md) for the helper contract and recovery.

## Model selection

ccgate sends each request with structured output, so pick a model that supports it. Sampling parameters are left alone — ccgate sets no `temperature` — so each model uses its own default.

Provider model lists:

- Anthropic: <https://docs.anthropic.com/en/docs/about-claude/models/overview>
- OpenAI: <https://platform.openai.com/docs/models>
- Gemini: <https://ai.google.dev/gemini-api/docs/models>

## `reasoning_effort`

Classifying one tool call needs no reasoning, so ccgate asks for none by default. Leaving it to the model is not neutral: current models reason by default, which costs latency on every permission check and spends output tokens that the verdict itself needs.

`provider.reasoning_effort` is one setting across providers, and each one asks for as little reasoning as its API can express:

| value | `openai` | `gemini` | `anthropic` |
|---|---|---|---|
| unset (= `none`) | `reasoning_effort: "none"` | `reasoning_effort: "minimal"` | `thinking: {type: "disabled"}` |
| `""` | nothing sent | nothing sent | nothing sent |
| anything else | `reasoning_effort` verbatim | `reasoning_effort` verbatim | `thinking: {type: "adaptive"}` + `output_config.effort` |

Two providers cannot say "none" literally. Anthropic has no such effort level — its `output_config.effort` is `low`/`medium`/`high`/`xhigh`/`max` — and the parameter that stops Claude from thinking is `thinking`, so `none` disables that instead. Gemini [cannot turn reasoning off](https://ai.google.dev/gemini-api/docs/openai) on its current models at all, so `none` asks for `minimal`, the floor its compatibility layer offers.

**The value is not validated.** `provider.name` only picks which protocol to speak, and `base_url` can point that protocol at anything, so the endpoint on the other end is the only authority on which levels mean something — a proxy fronting a dozen models may accept levels no first-party API defines. `""` and `none` are the only two values ccgate interprets; everything else is forwarded as written, including a typo.

So which values work is narrow, specific to the model's generation, and yours to check. A model with no reasoning parameter at all rejects every value; a reasoning model may accept `low` but not `none`; Anthropic has no `minimal`. When one is rejected the hook exits with the provider's own 400, which usually names the values it does accept, and the log records the configured `provider.reasoning_effort` alongside it — read it against the table above to see what that became on the wire.

Set the value the model accepts:

```jsonnet
{
  provider: {
    name: 'openai',
    model: 'a-reasoning-model',
    reasoning_effort: 'low',
  },
}
```

Or opt out entirely for a model that takes no reasoning parameter:

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
> Reasoning tokens count against the same output budget as the verdict. At higher efforts a model can spend the whole budget thinking and return a truncated response, which ccgate reports as unusable and turns into a fallthrough.

## `base_url` and compatible proxies

ccgate uses each provider SDK's standard chat / messages endpoint. That means any **OpenAI-compatible** or **Anthropic-compatible** endpoint — [LiteLLM proxy](https://docs.litellm.ai/docs/proxy/quick_start), Azure OpenAI, on-prem gateways, regional endpoints — works once you point `provider.base_url` at it.

`provider.base_url` is passed verbatim to the underlying SDK's `WithBaseURL`. The path follows each SDK's convention; ccgate does not normalise it:

| `provider.name` | SDK default base URL                                       | What to put in `base_url`                              |
|-----------------|------------------------------------------------------------|--------------------------------------------------------|
| `openai`        | `https://api.openai.com/v1/`                               | host **+ `/v1`** (SDK appends `chat/completions`)      |
| `anthropic`     | `https://api.anthropic.com/`                               | host root only (SDK appends `/v1/messages`)            |
| `gemini`        | `https://generativelanguage.googleapis.com/v1beta/openai/` | host **+ `/v1beta/openai`** when overriding            |

### OpenAI-compatible endpoint

For a proxy that exposes `/v1/chat/completions` (LiteLLM proxy, Azure OpenAI in OpenAI-compat mode, ...):

```jsonnet
{
  provider: {
    name: 'openai',
    model: 'anthropic/claude-haiku-4-5', // whatever the proxy exposes
    base_url: 'https://your-proxy.example/v1',
  },
}
```

Export the proxy's API key as `CCGATE_OPENAI_API_KEY`. The trailing `/v1` is required because the OpenAI SDK appends `/chat/completions` directly to the base URL.

### Anthropic-compatible endpoint

For a proxy that exposes `/v1/messages`:

```jsonnet
{
  provider: {
    name: 'anthropic',
    model: 'claude-haiku-4-5',
    base_url: 'https://your-proxy.example',
  },
}
```

Export the proxy's API key as `CCGATE_ANTHROPIC_API_KEY`. The Anthropic SDK appends `/v1/messages` itself, so the base URL stops at the host root.

## See also

- [docs/api-key-helper.md](api-key-helper.md) — `provider.auth` (refreshable credentials, helper contract, 401/403 behaviour, recovery checklist)
- [docs/configuration.md](configuration.md) — config layering, full field reference, fallthrough_strategy, metrics
