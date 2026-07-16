package gitutil

import (
	"path/filepath"
)

// Context is the cwd-derived git information ccgate exposes to the
// LLM. Targets stitch this onto their own per-tool payload (Claude
// adds referenced_paths, Codex forwards tool_input verbatim).
type Context struct {
	Cwd                 string `json:"cwd"`
	RepoRoot            string `json:"repo_root,omitempty"`
	GitDir              string `json:"git_dir,omitempty"`
	GitCommonDir        string `json:"git_common_dir,omitempty"`
	PrimaryCheckoutRoot string `json:"primary_checkout_root,omitempty"`
	BranchName          string `json:"branch_name,omitempty"`
	IsWorktree          bool   `json:"is_worktree"`
}

// BuildContext gathers git repository context for the given working
// directory. Each git lookup is best-effort: the function never
// errors out — fields stay empty when the corresponding `git`
// command fails or `cwd` is not in a git repo.
func BuildContext(cwd string) Context {
	ctx := Context{Cwd: cwd}
	if cwd == "" {
		return ctx
	}

	if repoRoot, err := Output(cwd, "rev-parse", "--show-toplevel"); err == nil {
		ctx.RepoRoot = repoRoot
	}

	gitDir, err := Output(cwd, "rev-parse", "--git-dir")
	if err == nil {
		ctx.GitDir = gitDir
	}

	gitCommonDir, err := Output(cwd, "rev-parse", "--git-common-dir")
	if err == nil {
		ctx.GitCommonDir = gitCommonDir
		// git emits --git-common-dir relative to cwd (e.g. "../.git")
		// when cwd is a repository subdirectory; resolve it so the
		// PrimaryCheckoutRoot and the worktree comparison below are
		// stable regardless of how git printed the path.
		if abs := resolveAbs(cwd, gitCommonDir); filepath.Base(abs) == ".git" {
			ctx.PrimaryCheckoutRoot = filepath.Dir(abs)
		}
	}

	// Worktree detection: in a linked worktree, git-dir and git-common-dir
	// point at different directories. git may emit either value relative
	// to cwd (e.g. "../.git" from a subdirectory), so resolve both to
	// absolute before comparing — otherwise a plain subdirectory of the
	// main checkout is mistaken for a worktree.
	if gitDir != "" && gitCommonDir != "" && resolveAbs(cwd, gitDir) != resolveAbs(cwd, gitCommonDir) {
		ctx.IsWorktree = true
	}

	if branchName, err := Output(cwd, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		ctx.BranchName = branchName
	}

	return ctx
}

// resolveAbs returns p as a cleaned absolute path, resolving relative
// paths against base. git rev-parse emits paths relative to the working
// directory when cwd is a repository subdirectory, so callers must
// resolve before comparing or embedding such values.
func resolveAbs(base, p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(base, p))
}
