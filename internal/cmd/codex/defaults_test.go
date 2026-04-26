package codex

import (
	"encoding/json"
	"strings"
	"testing"

	jsonnet "github.com/google/go-jsonnet"
)

// TestDefaultsJsonnetStructure parses the embedded codex defaults and
// asserts the rule taxonomy that ccgate philosophy mandates: every
// target ships allow + deny + environment guidance so the LLM has the
// same shape of context across targets. The actual rule wording is
// editorial and intentionally not asserted.
func TestDefaultsJsonnetStructure(t *testing.T) {
	t.Parallel()

	vm := jsonnet.MakeVM()
	out, err := vm.EvaluateAnonymousSnippet("codex/defaults.jsonnet", defaultsJsonnet)
	if err != nil {
		t.Fatalf("evaluate codex defaults.jsonnet: %v", err)
	}

	var got struct {
		Provider struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		} `json:"provider"`
		Allow       []string `json:"allow"`
		Deny        []string `json:"deny"`
		Environment []string `json:"environment"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal codex defaults: %v", err)
	}

	if got.Provider.Name != "anthropic" {
		t.Errorf("provider.name = %q, want %q", got.Provider.Name, "anthropic")
	}
	if got.Provider.Model == "" {
		t.Errorf("provider.model is empty; defaults must pin a model")
	}
	if len(got.Allow) == 0 {
		t.Errorf("allow is empty; codex defaults must ship allow guidance for Claude Code parity")
	}
	if len(got.Deny) == 0 {
		t.Errorf("deny is empty; codex defaults must ship deny guidance for Claude Code parity")
	}
	if len(got.Environment) == 0 {
		t.Errorf("environment is empty; codex defaults must encode the Bash-only / trust-boundary hints")
	}

	// Codex hooks fire for Bash, apply_patch, MCP tool calls, and
	// other surfaces. The environment block MUST tell the LLM that
	// the tool surface is heterogeneous so it does not regress to a
	// Bash-only assumption (an earlier version of this file did).
	var envText string
	for _, e := range got.Environment {
		envText += "\n" + e
	}
	for _, surface := range []string{"Bash", "apply_patch", "MCP"} {
		if !strings.Contains(envText, surface) {
			t.Errorf("environment guidance must mention %q so the LLM knows the tool surface is heterogeneous, got:\n%s", surface, envText)
		}
	}
}
