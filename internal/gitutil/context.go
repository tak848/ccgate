package gitutil

import (
	"os"
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

	// GitDir and GitCommonDir mirror git's output verbatim, which may be
	// relative to cwd (e.g. "../.git" from a subdirectory). They are kept
	// raw for payload fidelity; any comparison or derivation below resolves
	// them to absolute first. RepoRoot/PrimaryCheckoutRoot are absolute.
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
	// point at different directories. git may emit either value relative to
	// cwd (e.g. "../.git" from a subdirectory), and the absolute value may
	// differ cosmetically from a resolved relative one — symlinked temp
	// dirs (macOS /var vs /private/var) or mixed separators (Windows C:/
	// vs C:\). Compare by filesystem identity so none of that causes a
	// plain subdirectory of the main checkout to be mistaken for a worktree.
	if gitDir != "" && gitCommonDir != "" && !samePath(cwd, gitDir, gitCommonDir) {
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

// samePath reports whether two git paths (each possibly relative to cwd)
// refer to the same filesystem location. Paths are resolved against cwd
// and compared by os.SameFile so symlinks and path-separator differences
// do not cause false mismatches. If either path cannot be stat'd, it
// falls back to a lexical comparison of the resolved absolute paths.
func samePath(cwd, a, b string) bool {
	ra, rb := resolveAbs(cwd, a), resolveAbs(cwd, b)
	if ra == rb {
		return true
	}
	sa, errA := os.Stat(ra)
	sb, errB := os.Stat(rb)
	if errA == nil && errB == nil {
		return os.SameFile(sa, sb)
	}
	return false
}
