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
    model: 'gpt-4o-mini',
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

ccgate sends each request with structured output and `temperature=0` (deterministic classification). Pick a model that supports both.

Reasoning-tier models often require a different parameter shape and add chain-of-thought latency that ccgate's classification does not need. If a model rejects `temperature=0` or otherwise refuses structured output, ccgate falls through to the upstream tool's prompt instead of looping — but the same model will never produce a verdict. Confirm `temperature=0` + structured output support against the provider's model docs before relying on a specific model. Provider model lists:

- Anthropic: <https://docs.anthropic.com/en/docs/about-claude/models/overview>
- OpenAI: <https://platform.openai.com/docs/models>
- Gemini: <https://ai.google.dev/gemini-api/docs/models>

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
