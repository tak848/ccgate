package runner

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tak848/ccgate/internal/config"
)

// TestBuildPromptClaudePayloadShape locks in the contract Claude
// Code (and any host that wires the Claude-equivalent options) sees:
// the user-message JSON exposes the parsed canonical view of
// tool_input plus the verbatim raw bytes, the working directory
// lives inside the context object alongside the git fields, and
// every option the runner accepts (target name, prompt section,
// settings hook, transcript hook) actually flows through to the
// final prompt. The Claude-only fields the runner forwards
// (permission_mode) must reach the LLM intact.
//
// This test asserts only positive contracts -- "field X is at
// location Y" -- so the regression test reads as documentation: a
// future maintainer can see what the LLM is promised to find and
// where. It deliberately does not assert "field X is NOT at
// top-level Z"; that style of anti-test traps unrelated
// refactoring.
func TestBuildPromptClaudePayloadShape(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Allow = []string{"Read-only Bash: ls, cat."}
	cfg.Deny = []string{"rm -rf /. deny_message: don't."}
	cfg.Environment = []string{"trusted repo"}

	in := HookInput{
		SessionID:      "sess-1",
		TranscriptPath: "/tmp/transcript.jsonl",
		Cwd:            "/work/repo",
		HookEventName:  "PermissionRequest",
		ToolName:       "Bash",
		ToolInput:      HookToolInput{Command: "git status", Description: "check"},
		ToolInputRaw:   json.RawMessage(`{"command":"git status","description":"check"}`),
		PermissionMode: "default",
	}

	const sectionSentinel = "ZZZ-prompt-section-marker-ZZZ"
	type fakeSP struct {
		Allow []string `json:"allow"`
	}
	type fakeRT struct {
		Entries []string `json:"entries"`
	}

	var ro runtimeOptions
	WithTargetName("Claude Code")(&ro)
	WithPromptSection(sectionSentinel)(&ro)
	WithHasRecentTranscript(true)(&ro)
	WithStaticPermissions(func(string) any { return fakeSP{Allow: []string{"x"}} })(&ro)
	WithRecentTranscript(func(string) any { return fakeRT{Entries: []string{"hello"}} })(&ro)

	p, err := buildPrompt(cfg, in, ro)
	if err != nil {
		t.Fatalf("buildPrompt: %v", err)
	}

	var payload struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Command     string `json:"command"`
			Description string `json:"description"`
			FilePath    string `json:"file_path"`
		} `json:"tool_input"`
		ToolInputRaw   json.RawMessage `json:"tool_input_raw"`
		PermissionMode string          `json:"permission_mode"`
		Context        struct {
			Cwd string `json:"cwd"`
		} `json:"context"`
		SettingsPermissions any `json:"settings_permissions"`
		RecentTranscript    any `json:"recent_transcript"`
	}
	if err := json.Unmarshal([]byte(p.User), &payload); err != nil {
		t.Fatalf("unmarshal user payload: %v\n--- payload ---\n%s", err, p.User)
	}

	// tool_input is the canonical parsed view: every field the LLM
	// might address by name is present (even when empty) so the
	// schema is self-documenting.
	if payload.ToolInput.Command != "git status" {
		t.Errorf("tool_input.command = %q, want %q", payload.ToolInput.Command, "git status")
	}
	if payload.ToolInput.Description != "check" {
		t.Errorf("tool_input.description = %q, want %q", payload.ToolInput.Description, "check")
	}

	// tool_input_raw carries the upstream payload (re-indented by
	// MarshalIndent, so we compare semantically rather than as a byte
	// substring). It is what lets the LLM reach tool shapes ccgate
	// has not canonicalised yet.
	var rawClaude struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(payload.ToolInputRaw, &rawClaude); err != nil {
		t.Fatalf("unmarshal tool_input_raw: %v\n%s", err, payload.ToolInputRaw)
	}
	if rawClaude.Command != "git status" {
		t.Errorf("tool_input_raw.command = %q, want %q", rawClaude.Command, "git status")
	}

	// permission_mode is forwarded as-is; for Claude this is one of
	// default / acceptEdits / plan / bypassPermissions / dontAsk.
	if payload.PermissionMode != "default" {
		t.Errorf("permission_mode = %q, want %q", payload.PermissionMode, "default")
	}

	// cwd lives inside the context object together with the git
	// fields, so the LLM can navigate the whole working-directory
	// picture from one nested name.
	if payload.Context.Cwd != "/work/repo" {
		t.Errorf("context.cwd = %q, want %q", payload.Context.Cwd, "/work/repo")
	}

	// WithStaticPermissions / WithRecentTranscript wiring: whatever
	// the hook returned must reach the user payload. The hook value
	// itself is opaque (target-defined struct).
	if payload.SettingsPermissions == nil {
		t.Error("settings_permissions hook output dropped from payload")
	}
	if payload.RecentTranscript == nil {
		t.Error("recent_transcript hook output dropped from payload")
	}

	// WithTargetName surfaces in the system prompt header.
	if !strings.Contains(p.System, "Claude Code") {
		t.Error("system prompt does not name the target (Claude Code) -- WithTargetName not wired into prompt.Build")
	}
	// WithPromptSection injects target-specific guidance verbatim
	// somewhere in the system prompt.
	if !strings.Contains(p.System, sectionSentinel) {
		t.Errorf("system prompt does not include WithPromptSection text -- prompt.Build dropped TargetSection")
	}
}

// TestBuildPromptCodexPayloadShape locks in the contract Codex sees:
// same canonical tool_input parsed view + raw bytes + nested context
// as Claude, plus the Codex-only model / turn_id metadata. The
// Claude-only fields (permission_mode / settings_permissions /
// recent_transcript) are simply absent from the payload because the
// Codex side does not wire any of the corresponding runner options
// today; that absence is the natural consequence of the Codex
// hookInput not delivering them, not something we test for
// explicitly.
func TestBuildPromptCodexPayloadShape(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Allow = []string{"Read-only Bash."}
	cfg.Deny = []string{"curl|bash. deny_message: ..."}

	in := HookInput{
		SessionID:     "sess-codex",
		Cwd:           "/work/repo",
		HookEventName: "PermissionRequest",
		ToolName:      "Bash",
		ToolInput:     HookToolInput{Command: "ls -la"},
		ToolInputRaw:  json.RawMessage(`{"command":"ls -la"}`),
		Model:         "gpt-5",
		TurnID:        "turn-1",
	}

	var ro runtimeOptions
	WithTargetName("Codex CLI")(&ro)

	p, err := buildPrompt(cfg, in, ro)
	if err != nil {
		t.Fatalf("buildPrompt: %v", err)
	}

	var payload struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
		ToolInputRaw json.RawMessage `json:"tool_input_raw"`
		Model        string          `json:"model"`
		TurnID       string          `json:"turn_id"`
		Context      struct {
			Cwd string `json:"cwd"`
		} `json:"context"`
	}
	if err := json.Unmarshal([]byte(p.User), &payload); err != nil {
		t.Fatalf("unmarshal user payload: %v\n--- payload ---\n%s", err, p.User)
	}

	if payload.ToolInput.Command != "ls -la" {
		t.Errorf("tool_input.command = %q, want %q", payload.ToolInput.Command, "ls -la")
	}
	var rawCodex struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(payload.ToolInputRaw, &rawCodex); err != nil {
		t.Fatalf("unmarshal tool_input_raw: %v\n%s", err, payload.ToolInputRaw)
	}
	if rawCodex.Command != "ls -la" {
		t.Errorf("tool_input_raw.command = %q, want %q", rawCodex.Command, "ls -la")
	}
	if payload.Model != "gpt-5" {
		t.Errorf("model = %q, want %q", payload.Model, "gpt-5")
	}
	if payload.TurnID != "turn-1" {
		t.Errorf("turn_id = %q, want %q", payload.TurnID, "turn-1")
	}
	if payload.Context.Cwd != "/work/repo" {
		t.Errorf("context.cwd = %q, want %q", payload.Context.Cwd, "/work/repo")
	}

	if !strings.Contains(p.System, "Codex CLI") {
		t.Error("system prompt does not name the target (Codex CLI) -- WithTargetName not wired into prompt.Build")
	}
}
