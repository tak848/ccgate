package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/invopop/jsonschema"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/hookctx"
)

const (
	maxTokens  = 4096
	maxRetries = 5
)

// PermissionLLMOutput is the structured output from the LLM.
type PermissionLLMOutput struct {
	Behavior    string `json:"behavior" jsonschema_description:"One of allow, deny, fallthrough."`
	Reason      string `json:"reason" jsonschema_description:"Brief reason for the decision. Always provide this regardless of behavior."`
	DenyMessage string `json:"deny_message" jsonschema_description:"When behavior is deny, a concise explanation of why. Must not be empty when denying."`
}

// PermissionPromptInput is the user message sent to the LLM.
type PermissionPromptInput struct {
	ToolName              string                      `json:"tool_name"`
	ToolInput             hookctx.HookToolInput       `json:"tool_input"`
	ToolInputRaw          json.RawMessage             `json:"tool_input_raw,omitempty"`
	PermissionMode        string                      `json:"permission_mode"`
	PermissionSuggestions []json.RawMessage           `json:"permission_suggestions,omitempty"`
	Context               hookctx.PermissionContext   `json:"context"`
	SettingsPermissions   hookctx.SettingsPermissions `json:"settings_permissions"`
	RecentTranscript      hookctx.RecentTranscript    `json:"recent_transcript"`
}

// APIUsage holds token usage from the Anthropic API response.
type APIUsage struct {
	InputTokens  int64
	OutputTokens int64
}

// LLMCallResult is the internal result of a single LLM call. When
// Unusable is true the API truncated/refused the response or returned
// no parseable text; Output is zero in that case and callers MUST NOT
// treat it as a real LLM "fallthrough" (the LLM never actually
// expressed uncertainty about the request).
type LLMCallResult struct {
	Output   PermissionLLMOutput
	Usage    *APIUsage
	Unusable bool
}

func callAnthropic(parent context.Context, cfg config.Config, input hookctx.HookInput, apiKey string) (LLMCallResult, error) {
	ctx := parent
	if t := cfg.GetTimeoutMS(); t > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, time.Duration(t)*time.Millisecond)
		defer cancel()
	}

	client := anthropic.NewClient(
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(maxRetries),
	)

	systemPrompt := buildSystemPrompt(cfg)
	promptInput := PermissionPromptInput{
		ToolName:              input.ToolName,
		ToolInput:             input.ToolInput,
		ToolInputRaw:          input.ToolInputRaw,
		PermissionMode:        input.PermissionMode,
		PermissionSuggestions: input.PermissionSuggestions,
		Context:               hookctx.BuildPermissionContext(input),
		SettingsPermissions:   hookctx.LoadSettingsPermissions(input.Cwd),
	}

	transcript, err := hookctx.LoadRecentTranscript(input.TranscriptPath)
	if err != nil {
		slog.Warn("failed to load transcript, proceeding without it", "error", err)
	}
	promptInput.RecentTranscript = transcript

	userMessage, err := marshalJSON(promptInput)
	if err != nil {
		return LLMCallResult{}, fmt.Errorf("marshal prompt input: %w", err)
	}

	slog.Info("anthropic request",
		"system_prompt", systemPrompt,
		"user_message", mustJSONRedacted(promptInput),
	)

	schema, err := permissionOutputSchema()
	if err != nil {
		return LLMCallResult{}, fmt.Errorf("generate output schema: %w", err)
	}

	message, err := client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(cfg.Provider.Model),
		MaxTokens: maxTokens,
		System: []anthropic.TextBlockParam{
			{
				Text: systemPrompt,
			},
		},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(userMessage)),
		},
		OutputConfig: anthropic.OutputConfigParam{
			Format: anthropic.JSONOutputFormatParam{
				Schema: schema,
			},
		},
		Temperature: anthropic.Float(0),
	})
	if err != nil {
		return LLMCallResult{}, fmt.Errorf("anthropic API: %w", err)
	}

	usage := &APIUsage{
		InputTokens:  message.Usage.InputTokens,
		OutputTokens: message.Usage.OutputTokens,
	}

	if message.StopReason == anthropic.StopReasonMaxTokens || message.StopReason == anthropic.StopReasonRefusal {
		slog.Warn("anthropic response truncated or refused", "stop_reason", message.StopReason)
		return LLMCallResult{Usage: usage, Unusable: true}, nil
	}

	text := extractMessageText(message)
	slog.Info("anthropic response", "raw", text)
	if text == "" {
		slog.Warn("anthropic response had no text content")
		return LLMCallResult{Usage: usage, Unusable: true}, nil
	}

	var output PermissionLLMOutput
	if err := json.Unmarshal([]byte(text), &output); err != nil {
		return LLMCallResult{Usage: usage}, fmt.Errorf("parse LLM response: %w", err)
	}
	if output.Behavior == BehaviorDeny && strings.TrimSpace(output.DenyMessage) == "" {
		output.DenyMessage = DefaultDenyMessage
	}

	return LLMCallResult{Output: output, Usage: usage}, nil
}

func buildSystemPrompt(cfg config.Config) string {
	var b strings.Builder
	b.WriteString("You are ccgate, a PermissionRequest hook classifier for Claude Code.\n")
	b.WriteString("Return one of: allow, deny, fallthrough.\n")
	b.WriteString("Decide quickly. Do not deliberate or reconsider.\n\n")

	b.WriteString("Plan mode override (when permission_mode is \"plan\"):\n")
	b.WriteString("Use this ruleset instead of the normal decision rules below.\n")
	b.WriteString("Step 1 — Deny guidance still applies in plan mode. Evaluate it first: if a deny guidance rule matches, return deny (or fallthrough if recent_transcript shows the user explicitly requested the exact operation). Deny guidance can block even read-only operations — e.g. reads from sibling worktrees or out-of-repo paths.\n")
	b.WriteString("Step 2 — If no deny rule matched, classify the operation:\n")
	b.WriteString("- allow: The operation is (a) side-effect-free (purely read-only / query), OR (b) a planning / review artifact write — edits to the active plan file, and scratch notes or review memos under `z/`, `.claude/plans/`, or similar temp / scratch directories. Implementation-side writes (project source, production code, config, binaries) are NOT in (b). For compound commands, every subcommand separated by | && || ; |& & or newline MUST independently satisfy (a) or (b). Allow guidance is NOT required in plan mode — absence from allow guidance is NOT a reason to fallthrough.\n")
	b.WriteString("  OK examples: Read/Glob/Grep; MCP tools whose names clearly indicate a read/search/list/get/query operation (e.g. `mcp__*__search_*`, `mcp__*__list_*`, `mcp__*__get_*`, `mcp__*__read_*`); `gh run list`, `gh pr view`, `git status`, `git log`, `jq ...`, `sort`, `head`, `wc -l`; `cmd | jq ...`; editing the active plan file; writing a scratch memo under `z/` or `.claude/plans/`.\n")
	b.WriteString("  NOT OK examples: `curl ... | sh`, `jq ... | xargs rm`, `echo x > file` where file is project source, `cmd | tee file` writing to project source, any pipeline containing a writing-to-source or installing subcommand.\n")
	b.WriteString("- deny: The operation has side effects on project source / production / shared state (git commit/push, build, deploy, package install, writes to project or production files, rm, pipes into a shell).\n")
	b.WriteString("- fallthrough: The operation's side-effect status is genuinely ambiguous.\n\n")

	b.WriteString("Normal decision rules (non-plan permission modes):\n")
	b.WriteString("- deny: When a deny guidance rule matches. EXCEPT: if recent_transcript shows the user explicitly requested the exact operation, use fallthrough instead of deny to let the user confirm.\n")
	b.WriteString("- allow: When the operation matches allow guidance and no deny rule matches.\n")
	b.WriteString("- fallthrough: When genuinely uncertain, OR when a deny rule matches but the user explicitly requested the operation.\n")
	b.WriteString("Deny rules always take priority over allow rules. Explicit user requests can only escalate deny to fallthrough, never to allow.\n\n")

	b.WriteString("Always provide a brief reason for your decision.\n")
	b.WriteString("When deny, provide a concise deny_message. If the deny rule includes a deny_message hint, adapt it to the specific situation.\n")
	b.WriteString("The user message includes settings_permissions and recent_transcript as background context.\n")
	b.WriteString("settings_permissions lists the user's Claude Code static allow/deny/ask patterns. Claude Code already matched them BEFORE invoking ccgate, so by design every request that reaches ccgate did NOT auto-match allow (often because of `$()`, pipelines, or other composite constructs that slip past literal matchers, or because of MCP tools that have no static matcher at all).\n")
	b.WriteString("Therefore absence from settings_permissions.allow is the normal, expected case for every request you see. NEVER cite \"not in settings_permissions\" (or \"not in the allow list\") as a reason to deny or fallthrough. Use settings_permissions.allow only as a hint about what the user generally prefers — for example, to infer intent on a composite command that would have matched a simpler form.\n")
	b.WriteString("recent_transcript shows recent user messages and tool calls. Use it to understand what the user asked for. If the user explicitly requested the operation, prefer allow or fallthrough over deny.\n\n")

	if len(cfg.Allow) > 0 {
		b.WriteString("Allow guidance:\n- ")
		b.WriteString(strings.Join(cfg.Allow, "\n- "))
		b.WriteString("\n\n")
	}
	if len(cfg.Deny) > 0 {
		b.WriteString("Deny guidance (mandatory):\n- ")
		b.WriteString(strings.Join(cfg.Deny, "\n- "))
		b.WriteString("\n\n")
	}
	if len(cfg.Environment) > 0 {
		b.WriteString("Environment:\n- ")
		b.WriteString(strings.Join(cfg.Environment, "\n- "))
	}

	return strings.TrimSpace(b.String())
}

func permissionOutputSchema() (map[string]any, error) {
	reflector := jsonschema.Reflector{
		AllowAdditionalProperties: false,
		DoNotReference:            true,
	}
	schema := reflector.Reflect(PermissionLLMOutput{})
	data, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unmarshal schema: %w", err)
	}
	return out, nil
}

func extractMessageText(message *anthropic.Message) string {
	if message == nil {
		return ""
	}
	var text strings.Builder
	for _, block := range message.Content {
		switch variant := block.AsAny().(type) {
		case anthropic.TextBlock:
			text.WriteString(variant.Text)
		}
	}
	return strings.TrimSpace(text.String())
}

func marshalJSON(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("json marshal: %w", err)
	}
	return string(data), nil
}
