package codexoauth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tak848/ccgate/internal/llm"
)

func TestThreadStartParamsUseCodexAppServerSandboxMode(t *testing.T) {
	t.Parallel()

	p := llm.Prompt{
		Model: "gpt-5.4",
		User:  `{"context":{"cwd":"/tmp/workspace"}}`,
	}
	params := threadStartParams(p)

	if got := params["model"]; got != "gpt-5.4" {
		t.Fatalf("model = %q, want gpt-5.4", got)
	}
	if got := params["cwd"]; got != "/tmp/workspace" {
		t.Fatalf("cwd = %q, want /tmp/workspace", got)
	}
	if got := params["approvalPolicy"]; got != "never" {
		t.Fatalf("approvalPolicy = %q, want never", got)
	}
	if got := params["sandbox"]; got != threadSandboxModeReadOnly {
		t.Fatalf("sandbox = %q, want %q", got, threadSandboxModeReadOnly)
	}
	if params["sandbox"] == "readOnly" {
		t.Fatal("thread/start sandbox must use Codex app-server mode spelling read-only, not readOnly")
	}
	if got := params["serviceName"]; got != serviceName {
		t.Fatalf("serviceName = %q, want %q", got, serviceName)
	}
}

func TestTurnStartParamsUseReadOnlySandboxPolicy(t *testing.T) {
	t.Parallel()

	p := llm.Prompt{
		Model:  "gpt-5.4",
		System: "system rules",
		User:   `{"context":{"cwd":"/tmp/workspace"},"tool_name":"Bash"}`,
	}
	params := turnStartParams("thread-123", p)

	if got := params["threadId"]; got != "thread-123" {
		t.Fatalf("threadId = %q, want thread-123", got)
	}
	if got := params["cwd"]; got != "/tmp/workspace" {
		t.Fatalf("cwd = %q, want /tmp/workspace", got)
	}
	if got := params["model"]; got != "gpt-5.4" {
		t.Fatalf("model = %q, want gpt-5.4", got)
	}
	if got := params["approvalPolicy"]; got != "never" {
		t.Fatalf("approvalPolicy = %q, want never", got)
	}

	sandbox, ok := params["sandboxPolicy"].(map[string]any)
	if !ok {
		t.Fatalf("sandboxPolicy has type %T, want map[string]any", params["sandboxPolicy"])
	}
	if got := sandbox["type"]; got != turnSandboxPolicyReadOnly {
		t.Fatalf("sandboxPolicy.type = %q, want %q", got, turnSandboxPolicyReadOnly)
	}
	if got := sandbox["networkAccess"]; got != false {
		t.Fatalf("sandboxPolicy.networkAccess = %v, want false", got)
	}
	if _, ok := sandbox["access"]; ok {
		t.Fatal("turn/start sandboxPolicy must not include the old unsupported access shape")
	}

	input, ok := params["input"].([]map[string]string)
	if !ok || len(input) != 1 {
		t.Fatalf("input = %#v, want one text input", params["input"])
	}
	if input[0]["type"] != "text" {
		t.Fatalf("input[0].type = %q, want text", input[0]["type"])
	}
	if !strings.Contains(input[0]["text"], "system rules") || !strings.Contains(input[0]["text"], `"tool_name":"Bash"`) {
		t.Fatalf("classification prompt missing policy or payload: %q", input[0]["text"])
	}
	if _, ok := params["outputSchema"].(map[string]any); !ok {
		t.Fatalf("outputSchema has type %T, want map[string]any", params["outputSchema"])
	}
}

func TestValidateChatGPTAccount(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		raw     string
		wantErr error
	}{
		"chatgpt account": {
			raw: `{"account":{"type":"chatgpt","email":"user@example.test"}}`,
		},
		"chatgpt auth tokens account": {
			raw: `{"account":{"type":"chatgptAuthTokens"}}`,
		},
		"missing account": {
			raw:     `{"account":null,"requiresOpenaiAuth":true}`,
			wantErr: ErrAuthUnavailable,
		},
		"api key account": {
			raw:     `{"account":{"type":"apiKey"}}`,
			wantErr: ErrWrongAuthMode,
		},
		"invalid json": {
			raw:     `{`,
			wantErr: errors.New("parse account/read result"),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := validateChatGPTAccount([]byte(tc.raw))
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("validateChatGPTAccount returned error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if errors.Is(tc.wantErr, ErrAuthUnavailable) || errors.Is(tc.wantErr, ErrWrongAuthMode) {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want sentinel %v", err, tc.wantErr)
				}
				return
			}
			if !strings.Contains(err.Error(), tc.wantErr.Error()) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr.Error())
			}
		})
	}
}

func TestEnsureManagedCodexHomeCreatesFileCredentialConfig(t *testing.T) {
	t.Parallel()

	home := filepath.Join(t.TempDir(), "codex-home")
	if err := ensureManagedCodexHome(home); err != nil {
		t.Fatalf("ensureManagedCodexHome returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	if got, want := string(data), "cli_auth_credentials_store = \"file\"\n"; got != want {
		t.Fatalf("config.toml = %q, want %q", got, want)
	}

	custom := []byte("model = \"gpt-test\"\n")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), custom, 0o600); err != nil {
		t.Fatalf("write custom config.toml: %v", err)
	}
	if err := ensureManagedCodexHome(home); err != nil {
		t.Fatalf("ensureManagedCodexHome second call returned error: %v", err)
	}
	data, err = os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("read config.toml after second call: %v", err)
	}
	if string(data) != string(custom) {
		t.Fatalf("ensureManagedCodexHome overwrote existing config: %q", data)
	}
}

func TestParseOutput(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"plain json":    `{"behavior":"allow","reason":"safe read","deny_message":""}`,
		"wrapped json":  "```json\n{\"behavior\":\"deny\",\"reason\":\"unsafe\",\"deny_message\":\"blocked\"}\n```",
		"braces inside": `prefix {"behavior":"fallthrough","reason":"ambiguous {input}","deny_message":""} suffix`,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := parseOutput(input)
			if err != nil {
				t.Fatalf("parseOutput returned error: %v", err)
			}
			switch out.Behavior {
			case llm.BehaviorAllow, llm.BehaviorDeny, llm.BehaviorFallthrough:
			default:
				t.Fatalf("unexpected behavior %q", out.Behavior)
			}
		})
	}
}

func TestCodexAppServerEnvRemovesAPIKeys(t *testing.T) {
	t.Parallel()

	home := filepath.Join(string(filepath.Separator), "tmp", "codex-home")
	env := codexAppServerEnv([]string{
		"PATH=/bin",
		"OPENAI_API_KEY=sk-api",
		"CCGATE_OPENAI_API_KEY=sk-ccgate",
		"CODEX_API_KEY=sk-codex",
		"CODEX_HOME=/old",
		"OTHER=value",
	}, home)

	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, forbidden := range []string{"\nOPENAI_API_KEY=", "\nCCGATE_OPENAI_API_KEY=", "\nCODEX_API_KEY=", "\nCODEX_HOME=/old\n"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("env leaked %s in %q", forbidden, joined)
		}
	}
	if !strings.Contains(joined, "\nOTHER=value\n") {
		t.Fatalf("env dropped unrelated variable: %q", joined)
	}
	if !strings.Contains(joined, "\nCODEX_HOME="+home+"\n") {
		t.Fatalf("env did not set managed CODEX_HOME: %q", joined)
	}
}
