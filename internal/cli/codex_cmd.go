package cli

// CodexCmd groups the OpenAI Codex CLI subcommands. The bare form
// (`ccgate codex`) is the hook entry point ccgate users wire into
// `~/.codex/hooks.json`.
type CodexCmd struct {
	Init    CodexInitCmd    `cmd:"" help:"Output the embedded Codex CLI default configuration."`
	Metrics CodexMetricsCmd `cmd:"" help:"Show Codex CLI usage metrics."`
}

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
