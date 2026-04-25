package cli

// ClaudeCmd groups the Claude Code subcommands. Bare `ccgate claude`
// dispatches to the hidden Hook sub-sub-command (kong's default
// mechanism), making it the explicit equivalent of bare `ccgate`.
type ClaudeCmd struct {
	Hook    ClaudeHookCmd    `cmd:"" default:"withargs" hidden:""                                                 name:"hook"`
	Init    ClaudeInitCmd    `cmd:""                                                                              help:"Output the embedded Claude Code default configuration."`
	Metrics ClaudeMetricsCmd `cmd:""                                                                              help:"Show Claude Code usage metrics (combined with legacy path)."`
}

// ClaudeHookCmd is a marker struct so kong has a "subcommand" to make
// default. The actual hook orchestration is dispatched in cli.go.
type ClaudeHookCmd struct{}

// ClaudeInitCmd backs `ccgate claude init`.
type ClaudeInitCmd struct {
	Project bool   `help:"Output the project-local configuration template instead of the global one." short:"p"`
	Output  string `help:"Write to FILE instead of stdout."                                            short:"o" type:"path"`
	Force   bool   `help:"Overwrite an existing file at --output."                                     short:"f"`
}

// ClaudeMetricsCmd backs `ccgate claude metrics`.
type ClaudeMetricsCmd struct {
	Days    int  `default:"7"  help:"Show last N days."`
	JSON    bool `help:"Output as JSON."                                                          name:"json"`
	Details int  `default:"10" help:"Show top-N fallthrough/deny commands per section. Use 0 to hide both sections."`
}
