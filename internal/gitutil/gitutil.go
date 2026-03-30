package gitutil

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Output runs `git <args...>` in the given directory and returns trimmed stdout.
func Output(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// RepoRoot returns the top-level directory of the git repository containing dir.
func RepoRoot(dir string) (string, error) {
	return Output(dir, "rev-parse", "--show-toplevel")
}

// IsTracked reports whether the file at path is tracked by git in the given repo root.
// Returns (false, nil) if the file does not exist.
// Returns (false, error) on git errors (fail-closed).
func IsTracked(repoRoot, path string) (bool, error) {
	if repoRoot == "" {
		return false, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	if info.IsDir() {
		return false, nil
	}

	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return false, fmt.Errorf("filepath.Rel(%s, %s): %w", repoRoot, path, err)
	}

	cmd := exec.Command("git", "-C", repoRoot, "ls-files", "--error-unmatch", "--", rel)
	if err := cmd.Run(); err == nil {
		return true, nil
	} else {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("git ls-files %s: %w", rel, err)
	}
}
