package codex

import (
	"encoding/json"
	"strings"
	"testing"

	jsonnet "github.com/google/go-jsonnet"
)

// TestDefaultsJsonnetStructure parses both embedded Codex defaults
// (the global one shipped via `ccgate codex init` and the project
// template shipped via `ccgate codex init -p`) and asserts the rule
// taxonomy ccgate philosophy mandates: every target ships allow +
// deny + environment guidance so the LLM has the same shape of
// context across targets. The actual rule wording is editorial and
// intentionally not asserted.
func TestDefaultsJsonnetStructure(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		snippet string
		// the global template ships allow + deny + environment;
		// the project-local template only adds restrictions on top
		// of the global base, so any of allow / deny / environment
		// being empty there is fine.
		mustHaveProvider    bool
		mustHaveAllow       bool
		mustHaveDeny        bool
		mustHaveEnvironment bool
	}{
		"defaults": {
			snippet:             defaultsJsonnet,
			mustHaveProvider:    true,
			mustHaveAllow:       true,
			mustHaveDeny:        true,
			mustHaveEnvironment: true,
		},
		"defaults_project": {
			snippet: defaultsProjectJsonnet,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			vm := jsonnet.MakeVM()
			out, err := vm.EvaluateAnonymousSnippet("codex/"+name+".jsonnet", tc.snippet)
			if err != nil {
				t.Fatalf("evaluate %s: %v", name, err)
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
				t.Fatalf("unmarshal %s: %v", name, err)
			}

			if tc.mustHaveProvider {
				if got.Provider.Name != "anthropic" {
					t.Errorf("provider.name = %q, want %q", got.Provider.Name, "anthropic")
				}
				if got.Provider.Model == "" {
					t.Errorf("provider.model is empty; defaults must pin a model")
				}
			}
			if tc.mustHaveAllow && len(got.Allow) == 0 {
				t.Errorf("allow is empty; %s must ship allow guidance", name)
			}
			if tc.mustHaveDeny && len(got.Deny) == 0 {
				t.Errorf("deny is empty; %s must ship deny guidance", name)
			}
			if tc.mustHaveEnvironment && len(got.Environment) == 0 {
				t.Errorf("environment is empty; %s must ship environment context", name)
			}

			// Codex hooks fire for Bash, apply_patch, MCP tool calls,
			// and other surfaces. The global environment block MUST
			// tell the LLM that the tool surface is heterogeneous so
			// it does not regress to a Bash-only assumption (an
			// earlier version of this file did).
			if name == "defaults" {
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
		})
	}
}
