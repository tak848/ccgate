package hookctx

import (
	"path/filepath"
	"strings"

	"github.com/tak848/ccgate/internal/gitutil"
)

type PermissionContext struct {
	Cwd                 string   `json:"cwd"`
	RepoRoot            string   `json:"repo_root,omitempty"`
	GitDir              string   `json:"git_dir,omitempty"`
	GitCommonDir        string   `json:"git_common_dir,omitempty"`
	PrimaryCheckoutRoot string   `json:"primary_checkout_root,omitempty"`
	BranchName          string   `json:"branch_name,omitempty"`
	IsWorktree          bool     `json:"is_worktree"`
	ReferencedPaths     []string `json:"referenced_paths,omitempty"`
}

// BuildPermissionContext gathers git repository context for the given hook input.
func BuildPermissionContext(input HookInput) PermissionContext {
	ctx := PermissionContext{
		Cwd:             input.Cwd,
		ReferencedPaths: referencedPaths(input),
	}

	if input.Cwd == "" {
		return ctx
	}

	if repoRoot, err := gitutil.Output(input.Cwd, "rev-parse", "--show-toplevel"); err == nil {
		ctx.RepoRoot = repoRoot
	}

	gitDir, err := gitutil.Output(input.Cwd, "rev-parse", "--git-dir")
	if err == nil {
		ctx.GitDir = gitDir
	}

	gitCommonDir, err := gitutil.Output(input.Cwd, "rev-parse", "--git-common-dir")
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

	if branchName, err := gitutil.Output(input.Cwd, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		ctx.BranchName = branchName
	}

	return ctx
}
