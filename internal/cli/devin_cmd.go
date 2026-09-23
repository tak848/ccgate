package cli

// DevinCmd groups the Devin CLI subcommands. Bare `ccgate devin`
// dispatches to the Hook sub-sub-command via kong's default mechanism
// so users can wire that exact string into their Devin hooks config.
// Hook is left visible in --help so users can see that the bare
// invocation has a concrete entry point.
type DevinCmd struct {
	Hook    DevinHookCmd    `cmd:"" default:"withargs" name:"hook" help:"Run the Devin hook from stdin (default; same as 'ccgate devin')."`
	Init    DevinInitCmd    `cmd:""                                help:"Output the embedded Devin default configuration."`
	Metrics DevinMetricsCmd `cmd:""                                help:"Show Devin usage metrics."`
}

// DevinHookCmd is a marker struct so kong has a "subcommand" to make
// default. The actual hook orchestration is dispatched in cli.go.
type DevinHookCmd struct{}

// DevinInitCmd backs `ccgate devin init`.
type DevinInitCmd struct {
	Project bool   `help:"Output the project-local configuration template instead of the global one." short:"p"`
	Output  string `help:"Write to FILE instead of stdout."                                            short:"o" type:"path"`
	Force   bool   `help:"Overwrite an existing file at --output."                                     short:"f"`
}

// DevinMetricsCmd backs `ccgate devin metrics`.
type DevinMetricsCmd struct {
	Days    int  `default:"7"  help:"Show last N days."`
	JSON    bool `help:"Output as JSON."                                                          name:"json"`
	Details int  `default:"10" help:"Show top-N fallthrough/deny commands per section. Use 0 to hide both sections."`
}
