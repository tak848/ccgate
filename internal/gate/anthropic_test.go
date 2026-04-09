package gate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tak848/ccgate/internal/config"
)

func TestBuildSystemPromptRemovedHardcodedContent(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Allow = []string{"Test allow"}
	cfg.Deny = []string{"Test deny"}
	prompt := buildSystemPrompt(cfg)

	for _, banned := range []string{
		"Japanese",
		"Built-in deny rules",
		"routine development operation",
	} {
		if strings.Contains(prompt, banned) {
			t.Errorf("system prompt should not contain %q", banned)
		}
	}
}

func TestBuildSystemPromptInjectsConfigRules(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Allow = []string{"Custom allow rule"}
	cfg.Deny = []string{"Custom deny rule"}
	cfg.Environment = []string{"Custom env"}
	prompt := buildSystemPrompt(cfg)

	for _, expected := range []string{
		"Custom allow rule",
		"Custom deny rule",
		"Custom env",
		"Plan mode override",
	} {
		if !strings.Contains(prompt, expected) {
			t.Errorf("system prompt should contain %q", expected)
		}
	}
}

func TestPermissionOutputSchemaNoJapanese(t *testing.T) {
	t.Parallel()

	schema, err := permissionOutputSchema()
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(data), "Japanese") {
		t.Fatal("output schema should not reference 'Japanese'")
	}
}
