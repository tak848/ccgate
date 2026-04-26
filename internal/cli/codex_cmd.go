package cli

// CodexCmd groups the OpenAI Codex CLI subcommands. Bare `ccgate codex`
// dispatches to the Hook sub-sub-command via kong's default mechanism
// so users can wire that exact string into their Codex hook config.
// Hook is left visible in --help so users can see that the bare
// invocation has a concrete entry point.
type CodexCmd struct {
	Hook    CodexHookCmd    `cmd:"" default:"withargs" name:"hook" help:"Run the Codex CLI hook from stdin (default; same as 'ccgate codex')."`
	Init    CodexInitCmd    `cmd:""                                help:"Output the embedded Codex CLI default configuration."`
	Metrics CodexMetricsCmd `cmd:""                                help:"Show Codex CLI usage metrics."`
}

// CodexHookCmd is a marker struct so kong has a "subcommand" to make
// default. The actual hook orchestration is dispatched in cli.go.
type CodexHookCmd struct{}

// CodexInitCmd backs `ccgate codex init`. Codex defaults are a single
// jsonnet file (no project-vs-global split) so we only carry --output
// / --force here.
type CodexInitCmd struct {
	Output string `help:"Write to FILE instead of stdout."   short:"o" type:"path"`
	Force  bool   `help:"Overwrite an existing file at --output." short:"f"`
}

// CodexMetricsCmd backs `ccgate codex metrics`.
type CodexMetricsCmd struct {
	Days    int  `default:"7"  help:"Show last N days."`
	JSON    bool `help:"Output as JSON."                                                          name:"json"`
	Details int  `default:"10" help:"Show top-N fallthrough/deny commands per section. Use 0 to hide both sections."`
}
