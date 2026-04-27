package gitutil

import (
	"path/filepath"
	"strings"
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
		if strings.HasSuffix(gitCommonDir, "/.git") || strings.HasSuffix(gitCommonDir, string(filepath.Separator)+".git") {
			ctx.PrimaryCheckoutRoot = filepath.Dir(gitCommonDir)
		}
	}

	// Worktree detection: git-dir and git-common-dir differ in worktrees.
	if gitDir != "" && gitCommonDir != "" && gitDir != gitCommonDir {
		ctx.IsWorktree = true
	}

	if branchName, err := Output(cwd, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		ctx.BranchName = branchName
	}

	return ctx
}
