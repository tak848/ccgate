package codex

import (
	"encoding/json"
	"testing"
)

// TestHookInputUnmarshal pins the upstream Codex PermissionRequest
// payload shape (developers.openai.com/codex/hooks, verified
// 2026-04-24) so we notice if Codex changes the wire format or if a
// refactor breaks the parser. The fixture mirrors the upstream
// example with the minimum set of fields ccgate needs.
func TestHookInputUnmarshal(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		body         string
		wantTool     string
		wantCommand  string
		wantTurnID   string
		wantModel    string
		wantHookName string
	}{
		"upstream example (Bash, with description)": {
			body: `{
				"session_id":      "sess-abc",
				"transcript_path": "/tmp/codex/transcript.jsonl",
				"cwd":             "/home/user/project",
				"hook_event_name": "PermissionRequest",
				"model":           "gpt-5",
				"turn_id":         "turn-42",
				"tool_name":       "Bash",
				"tool_input": {
					"command":     "ls -la",
					"description": "List the current directory"
				}
			}`,
			wantTool:     "Bash",
			wantCommand:  "ls -la",
			wantTurnID:   "turn-42",
			wantModel:    "gpt-5",
			wantHookName: "PermissionRequest",
		},
		"description omitted (Codex sometimes sends null)": {
			body: `{
				"session_id":      "sess-1",
				"transcript_path": "",
				"cwd":             "/repo",
				"hook_event_name": "PermissionRequest",
				"tool_name":       "Bash",
				"tool_input":      {"command": "echo hi"}
			}`,
			wantTool:     "Bash",
			wantCommand:  "echo hi",
			wantHookName: "PermissionRequest",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var in HookInput
			if err := json.Unmarshal([]byte(tc.body), &in); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if in.ToolName != tc.wantTool {
				t.Errorf("tool_name = %q, want %q", in.ToolName, tc.wantTool)
			}
			if in.ToolInput.Command != tc.wantCommand {
				t.Errorf("tool_input.command = %q, want %q", in.ToolInput.Command, tc.wantCommand)
			}
			if in.HookEventName != tc.wantHookName {
				t.Errorf("hook_event_name = %q, want %q", in.HookEventName, tc.wantHookName)
			}
			if tc.wantTurnID != "" && in.TurnID != tc.wantTurnID {
				t.Errorf("turn_id = %q, want %q", in.TurnID, tc.wantTurnID)
			}
			if tc.wantModel != "" && in.Model != tc.wantModel {
				t.Errorf("model = %q, want %q", in.Model, tc.wantModel)
			}
			if len(in.ToolInputRaw) == 0 {
				t.Errorf("tool_input_raw was not preserved; future Codex fields will be invisible to the LLM")
			}
		})
	}
}
