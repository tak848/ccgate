package codex

import (
	"encoding/json"
	"fmt"

	"github.com/tak848/ccgate/internal/config"
	"github.com/tak848/ccgate/internal/gitutil"
	"github.com/tak848/ccgate/internal/llm"
	"github.com/tak848/ccgate/internal/prompt"
)

// promptInput is the structured user message ccgate sends to the LLM.
// tool_input is forwarded as raw JSON so MCP arguments and other
// tool-specific shapes survive intact.
type promptInput struct {
	ToolName    string          `json:"tool_name"`
	ToolInput   json.RawMessage `json:"tool_input,omitempty"`
	Description string          `json:"description,omitempty"`
	Cwd         string          `json:"cwd"`
	Context     gitutil.Context `json:"context"`
	Model       string          `json:"model,omitempty"`
	TurnID      string          `json:"turn_id,omitempty"`
}

// buildPrompt assembles the system + user messages for the Codex hook.
func buildPrompt(cfg config.Config, in HookInput) (llm.Prompt, error) {
	pi := promptInput{
		ToolName:    in.ToolName,
		ToolInput:   in.ToolInputRaw,
		Description: in.ToolInput.Description,
		Cwd:         in.Cwd,
		Context:     gitutil.BuildContext(in.Cwd),
		Model:       in.Model,
		TurnID:      in.TurnID,
	}
	user, err := json.MarshalIndent(pi, "", "  ")
	if err != nil {
		return llm.Prompt{}, fmt.Errorf("marshal prompt input: %w", err)
	}

	p := prompt.Build(prompt.Args{
		PlanMode:    false,
		Allow:       cfg.Allow,
		Deny:        cfg.Deny,
		Environment: cfg.Environment,
		UserPayload: string(user),
	})
	p.Model = cfg.Provider.Model
	p.TimeoutMS = cfg.GetTimeoutMS()
	return p, nil
}
