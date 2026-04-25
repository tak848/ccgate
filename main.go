// Command ccgate is a PermissionRequest hook for AI coding tools.
// All CLI logic lives in internal/cli; this file exists only to
// thread os.Args / stdio into cli.Run and propagate the exit code.
package main

import (
	"os"
	"runtime/debug"

	"github.com/tak848/ccgate/internal/cli"
)

// version is overwritten via -ldflags at release time. It is also
// resolved from the Go module build info as a fallback so unstamped
// `go install` / `go build` users still see a useful version string
// (e.g. v0.6.0 from a tagged checkout).
var version = "dev"

func init() {
	if version != "dev" {
		return
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}
}

func main() {
	os.Exit(cli.Run(version, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
