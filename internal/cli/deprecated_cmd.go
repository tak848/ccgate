package cli

import (
	"fmt"
	"io"
)

// DeprecatedInitCmd / DeprecatedMetricsCmd register the historical
// top-level subcommands so they no longer parse as unknown commands.
// Both intentionally accept no flags — the kong help text and the
// runtime error message both point users at the new per-target form.
type (
	DeprecatedInitCmd    struct{}
	DeprecatedMetricsCmd struct{}
)

const (
	releaseURL = "https://github.com/tak848/ccgate/releases/tag/v0.6.0"

	deprecatedInitMessage = "'ccgate init' has been removed in v0.6.0.\n" +
		"Use one of:\n" +
		"  ccgate claude init   (Claude Code)\n" +
		"  ccgate codex init    (OpenAI Codex CLI)\n" +
		"See: " + releaseURL + "\n"

	deprecatedMetricsMessage = "'ccgate metrics' has been removed in v0.6.0.\n" +
		"Use one of:\n" +
		"  ccgate claude metrics   (Claude Code)\n" +
		"  ccgate codex metrics    (OpenAI Codex CLI)\n" +
		"See: " + releaseURL + "\n"
)

func runDeprecatedInit(stderr io.Writer) int {
	fmt.Fprint(stderr, deprecatedInitMessage)
	return 2
}

func runDeprecatedMetrics(stderr io.Writer) int {
	fmt.Fprint(stderr, deprecatedMetricsMessage)
	return 2
}
