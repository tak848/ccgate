//go:build unix

package keystore

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
)

// applyHelperProcessAttrs places the shell child in its own process
// group so cancelling the context can take down the whole pipeline,
// not just `bash` / `pwsh`. Helpers that explicitly daemonize via
// `setsid` escape the group; that contract is documented and
// enforced by observation rather than by force-kill heuristics.
func applyHelperProcessAttrs(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killHelperProcessTree fires when the context is cancelled. Sending
// SIGKILL with a negative pid targets the process group ID set by
// applyHelperProcessAttrs, taking down the shell and any children it
// spawned (a `go run` build process, a browser opened by an SSO
// helper, etc.). SIGKILL rather than SIGTERM because helpers are
// expected to be non-interactive and quick to terminate.
func killHelperProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// warnLoosePermissions emits a slog.Warn when an on-disk
// credential file has either a non-current owner or any
// group/other read bit set. It does NOT hard-reject — the user is
// authoritative on filesystem layout — but it surfaces a security
// nudge so a forgotten `chmod 0600` is obvious in the logs. The
// `source` label distinguishes the user-managed `auth.path` from
// ccgate's own cache file.
func warnLoosePermissions(path string, info os.FileInfo, source Source) {
	if info == nil {
		return
	}
	mode := info.Mode().Perm()
	uid := -1
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		uid = int(st.Uid)
	}
	groupOrOtherReadable := mode&0o044 != 0
	wrongOwner := uid >= 0 && uid != os.Geteuid()
	if !groupOrOtherReadable && !wrongOwner {
		return
	}
	slog.Warn("keystore: credential file has loose permissions, recommend 0600",
		"source", string(source),
		"path", path,
		"mode", fmt.Sprintf("%#o", mode),
		"uid", uid,
	)
}
