//go:build windows

package keystore

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// applyHelperProcessAttrs starts the shell child in a new process
// group so killHelperProcessTree can find and terminate the entire
// pipeline. Equivalent role to Setpgid on Unix.
func applyHelperProcessAttrs(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
}

// killHelperProcessTree walks the descendants of the helper PID via
// Toolhelp32Snapshot and TerminateProcesses each one. We can't use
// Job Objects without restructuring (Job assignment must happen
// while the child is suspended), so we accept a small race where a
// grandchild may spawn between snapshot and kill.
func killHelperProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	root := uint32(cmd.Process.Pid) //nolint:gosec // pid is a positive int from os/exec
	return killTreeWindows(root)
}

func killTreeWindows(root uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return fmt.Errorf("toolhelp snapshot: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snap) }()

	parents := map[uint32][]uint32{}
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snap, &entry); err != nil {
		return fmt.Errorf("process32first: %w", err)
	}
	for {
		parents[entry.ParentProcessID] = append(parents[entry.ParentProcessID], entry.ProcessID)
		if err := windows.Process32Next(snap, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return fmt.Errorf("process32next: %w", err)
		}
	}

	// BFS from the helper, then terminate in reverse so children
	// die before their parents (avoids re-parenting to PID 1).
	queue := []uint32{root}
	var pids []uint32
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		pids = append(pids, pid)
		queue = append(queue, parents[pid]...)
	}
	for i := len(pids) - 1; i >= 0; i-- {
		pid := pids[i]
		h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
		if err != nil {
			continue // already gone or insufficient rights
		}
		_ = windows.TerminateProcess(h, 1)
		_ = windows.CloseHandle(h)
	}
	return nil
}

// warnLoosePermissions checks the file's DACL for allow-ACEs that
// grant read access to the Everyone or BuiltinUsers SID. It is a
// best-effort security nudge — equivalent in spirit to the POSIX
// `chmod 0600` check on Unix — and never hard-rejects. ACL
// inspection failures are silent (we don't want to warn falsely if
// the kernel just returned an unexpected error code). The check is
// shallow: deny ACEs, inheritance, effective permissions, and SIDs
// other than Everyone / BuiltinUsers are not evaluated.
func warnLoosePermissions(path string, _ os.FileInfo, source Source) {
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return
	}

	world, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		return
	}
	users, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		return
	}

	if !daclGrantsReadTo(dacl, world, users) {
		return
	}
	slog.Warn("keystore: credential file has loose ACL granting read to Everyone/Users, recommend restricting",
		"source", string(source),
		"path", path,
	)
}

// daclGrantsReadTo walks the ACEs in dacl and returns true if any
// ACCESS_ALLOWED_ACE grants generic / file read to one of the given
// well-known SIDs. Deny-ACEs and other ACE types are ignored.
func daclGrantsReadTo(dacl *windows.ACL, sids ...*windows.SID) bool {
	const readMask = windows.GENERIC_READ | windows.GENERIC_ALL |
		windows.FILE_GENERIC_READ | windows.FILE_READ_DATA
	count := uint32(dacl.AceCount)
	for i := range count {
		var hdrPtr *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, (**windows.ACCESS_ALLOWED_ACE)(unsafe.Pointer(&hdrPtr))); err != nil {
			continue
		}
		if hdrPtr == nil {
			continue
		}
		if hdrPtr.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		if hdrPtr.Mask&readMask == 0 {
			continue
		}
		aceSID := (*windows.SID)(unsafe.Pointer(&hdrPtr.SidStart))
		for _, candidate := range sids {
			if windows.EqualSid(aceSID, candidate) {
				return true
			}
		}
	}
	return false
}
