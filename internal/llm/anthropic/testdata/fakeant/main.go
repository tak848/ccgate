// Command fakeant is a stand-in for the official `ant` binary used
// by anthropic.bootstrapWithAnt tests. The shape of the CLI is just
// enough to satisfy the subset ccgate calls — `auth login --profile
// <name> --timeout <duration>` — and the runtime behavior is driven
// entirely by env vars so a single binary covers every test case.
//
// Modes (FAKE_ANT_MODE):
//
//   - "" or "success": write a minimal credentials file at the SDK
//     default path under $ANTHROPIC_CONFIG_DIR and exit 0.
//   - "fail": exit 1 without writing anything.
//   - "sleep": sleep FAKE_ANT_SLEEP (parsed as a Go duration; default
//     10s) then write credentials and exit 0. Used to test ant_timeout
//     and the provider-vs-auth timeout split.
//   - "missing_credentials": exit 0 without writing — exercises the
//     credentials_missing_after_login error_class.
//
// The binary is built lazily by the test harness and dropped into a
// per-test t.TempDir, then the test prepends that dir to PATH via
// t.Setenv. Real `ant` writes to active_config too; ccgate does not
// touch active_config and the tests do not assert on it.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	anthropicconfig "github.com/anthropics/anthropic-sdk-go/config"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "auth" || os.Args[2] != "login" {
		fmt.Fprintln(os.Stderr, "fakeant: expected `auth login` subcommand")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	profile := fs.String("profile", "", "")
	timeout := fs.Duration("timeout", 0, "")
	if err := fs.Parse(os.Args[3:]); err != nil {
		os.Exit(2)
	}
	_ = timeout

	switch os.Getenv("FAKE_ANT_MODE") {
	case "fail":
		fmt.Fprintln(os.Stderr, "fakeant: simulated failure")
		os.Exit(1)
	case "missing_credentials":
		os.Exit(0)
	case "sleep":
		d, err := time.ParseDuration(os.Getenv("FAKE_ANT_SLEEP"))
		if err != nil || d == 0 {
			d = 10 * time.Second
		}
		time.Sleep(d)
	case "", "success":
		// fall through to write
	default:
		fmt.Fprintf(os.Stderr, "fakeant: unknown FAKE_ANT_MODE=%q\n", os.Getenv("FAKE_ANT_MODE"))
		os.Exit(2)
	}

	if *profile == "" {
		fmt.Fprintln(os.Stderr, "fakeant: --profile required")
		os.Exit(2)
	}
	dir := os.Getenv("ANTHROPIC_CONFIG_DIR")
	if dir == "" {
		dir = anthropicconfig.DefaultDir()
	}
	path := anthropicconfig.ProfileCredentialsPath(dir, *profile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	exp := time.Now().Add(time.Hour)
	creds := anthropicconfig.Credentials{
		AccessToken:  "fake-access-" + *profile,
		RefreshToken: "fake-refresh-" + *profile,
		ExpiresAt:    &exp,
	}
	if err := anthropicconfig.WriteCredentials(path, creds); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
