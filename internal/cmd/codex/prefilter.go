package codex

// prefilterResult tells Run whether to short-circuit the LLM call.
// Codex hooks are upstream-minimal today: there is no permission_mode
// field, no tool_use_id, no settings.json equivalent. So the prefilter
// is a no-op for v0.5 — every PermissionRequest goes to the LLM.
//
// Follow-up issues (see plan L#1, L#3, L#4):
//   - permission_mode=plan detection once Codex exposes it
//   - ~/.codex/config.toml prefix_rules: forbidden -> deny, prompt -> fallthrough
type prefilterResult struct {
	Skip            bool
	FallthroughKind string
	Reason          string
}

func runPrefilter(_ HookInput) prefilterResult {
	return prefilterResult{}
}
