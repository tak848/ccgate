package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRepoRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	initGit(t, dir)

	root, err := RepoRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	// TempDir may return a symlinked path on macOS
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(root)
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRepoRootNotGitRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := RepoRoot(dir)
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
}

func TestIsTrackedTrue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	initGit(t, dir)

	path := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "tracked.txt")

	tracked, err := IsTracked(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	if !tracked {
		t.Fatal("expected file to be tracked")
	}
}

func TestIsTrackedFalse(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	initGit(t, dir)

	path := filepath.Join(dir, "untracked.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	tracked, err := IsTracked(dir, path)
	if err != nil {
		t.Fatal(err)
	}
	if tracked {
		t.Fatal("expected file to be untracked")
	}
}

func TestIsTrackedNonExistent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	initGit(t, dir)

	tracked, err := IsTracked(dir, filepath.Join(dir, "nonexistent.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if tracked {
		t.Fatal("expected false for nonexistent file")
	}
}

func TestIsTrackedNotGitRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := IsTracked(dir, path)
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
}

func TestIsTrackedEmptyRoot(t *testing.T) {
	t.Parallel()

	tracked, err := IsTracked("", "/any/path")
	if err != nil {
		t.Fatal(err)
	}
	if tracked {
		t.Fatal("expected false for empty root")
	}
}

func initGit(t *testing.T, dir string) {
	t.Helper()
	gitRun(t, dir, "init")
	gitRun(t, dir, "config", "user.email", "test@test.com")
	gitRun(t, dir, "config", "user.name", "test")
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}
