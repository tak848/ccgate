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

func TestMainWorktreeRoot(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		isGit        bool
		makeWorktree bool
	}{
		"main repo":       {isGit: true, makeWorktree: false},
		"linked worktree": {isGit: true, makeWorktree: true},
		"non-git dir":     {isGit: false, makeWorktree: false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			main := t.TempDir()
			if tc.isGit {
				initGit(t, main)
			}

			target := main
			var wantMain string
			if tc.makeWorktree {
				gitRun(t, main, "commit", "--allow-empty", "-m", "init")
				wt := filepath.Join(filepath.Dir(main), filepath.Base(main)+"-wt")
				t.Cleanup(func() { _ = os.RemoveAll(wt) })
				gitRun(t, main, "worktree", "add", "--detach", wt)
				target = wt
				wantMain, _ = filepath.EvalSymlinks(main)
			}

			got := MainWorktreeRoot(target)

			if wantMain == "" {
				if got != "" {
					t.Fatalf("got %q, want empty", got)
				}
				return
			}

			gotResolved, err := filepath.EvalSymlinks(got)
			if err != nil {
				t.Fatalf("EvalSymlinks(%q): %v", got, err)
			}
			if gotResolved != wantMain {
				t.Fatalf("got %q, want %q", gotResolved, wantMain)
			}
		})
	}
}

func TestMainWorktreeRootBareRepo(t *testing.T) {
	t.Parallel()

	// A bare repo plus a linked worktree must surface as no-op
	// (empty string), matching the project's "bare repo / submodule /
	// custom $GIT_DIR -> empty" invariant. Two naming shapes:
	//   - `repo.git`     : the conventional bare-repo name
	//   - `holder/.git`  : a hostile shape whose suffix happens to
	//                      match the non-bare `.git` heuristic
	cases := map[string]struct {
		bareName string // path of the bare repo relative to parent
	}{
		"conventional <name>.git": {bareName: "repo.git"},
		"hostile <dir>/.git":      {bareName: "holder/.git"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			parent := t.TempDir()
			bare := filepath.Join(parent, tc.bareName)
			if dir := filepath.Dir(bare); dir != parent {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			gitRun(t, parent, "init", "--bare", tc.bareName)

			seed := filepath.Join(parent, "seed")
			gitRun(t, parent, "clone", bare, "seed")
			gitRun(t, seed, "config", "user.email", "test@test.com")
			gitRun(t, seed, "config", "user.name", "test")
			gitRun(t, seed, "commit", "--allow-empty", "-m", "init")
			gitRun(t, seed, "push", "origin", "HEAD:refs/heads/main")

			wt := filepath.Join(parent, "wt")
			gitRun(t, bare, "worktree", "add", wt, "main")

			if got := MainWorktreeRoot(wt); got != "" {
				t.Fatalf("bare repo linked worktree (%s): got %q, want empty", tc.bareName, got)
			}
		})
	}
}

func TestBuildContextWorktreeDetection(t *testing.T) {
	t.Run("subdirectory of main checkout is not a worktree", func(t *testing.T) {
		t.Parallel()

		main := t.TempDir()
		initGit(t, main)

		// A plain subdirectory of the main checkout, not a linked worktree.
		// git rev-parse emits --git-common-dir relative to cwd here
		// (e.g. "../.git"), which used to be string-compared against an
		// absolute --git-dir and misclassify the directory as a worktree.
		sub := filepath.Join(main, "sub")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}

		ctx := BuildContext(sub)
		if ctx.IsWorktree {
			t.Fatalf("plain subdirectory misdetected as worktree: git_dir=%q git_common_dir=%q",
				ctx.GitDir, ctx.GitCommonDir)
		}
	})

	t.Run("linked worktree detected", func(t *testing.T) {
		t.Parallel()

		main := t.TempDir()
		initGit(t, main)
		gitRun(t, main, "commit", "--allow-empty", "-m", "init")
		wt := filepath.Join(filepath.Dir(main), filepath.Base(main)+"-wt")
		t.Cleanup(func() { _ = os.RemoveAll(wt) })
		gitRun(t, main, "worktree", "add", "--detach", wt)

		ctx := BuildContext(wt)
		if !ctx.IsWorktree {
			t.Fatalf("linked worktree not detected: git_dir=%q git_common_dir=%q",
				ctx.GitDir, ctx.GitCommonDir)
		}

		wantMain, _ := filepath.EvalSymlinks(main)
		gotMain, err := filepath.EvalSymlinks(ctx.PrimaryCheckoutRoot)
		if err != nil {
			t.Fatalf("EvalSymlinks(%q): %v", ctx.PrimaryCheckoutRoot, err)
		}
		if gotMain != wantMain {
			t.Fatalf("PrimaryCheckoutRoot=%q, want %q", gotMain, wantMain)
		}
	})
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
