package runner

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// referencedPaths extracts filesystem paths from tool_input on a
// best-effort basis. The case list is shared across targets: PascalCase
// names are Claude Code's, snake_case ones Devin's (exec, edit, ...).
// Tool shapes ccgate does not model here (apply_patch hunks, MCP
// arguments, notebook_path) reach the LLM via tool_input_raw instead.
func referencedPaths(input HookInput) []string {
	switch input.ToolName {
	case "Read", "Write", "Edit", "MultiEdit", "read", "write", "edit":
		return uniqueNonEmpty(expandPaths(input.Cwd, input.ToolInput.FilePath))
	case "Glob", "glob":
		return uniqueNonEmpty(expandPaths(input.Cwd, input.ToolInput.Path, input.ToolInput.Pattern))
	case "Grep", "grep":
		return uniqueNonEmpty(expandPaths(input.Cwd, input.ToolInput.Path))
	case "Bash", "exec":
		return uniqueNonEmpty(extractBashPaths(input.Cwd, input.ToolInput.Command))
	default:
		return nil
	}
}

// homePrefixes are the spellings of the home directory a path commonly
// starts with.
var homePrefixes = []string{"~", "$HOME", "${HOME}"}

// cutHomePrefix reports whether value starts with a home-directory
// spelling, alone or followed by `/`, and returns the rest of the path.
func cutHomePrefix(value string) (string, bool) {
	for _, prefix := range homePrefixes {
		rest, found := strings.CutPrefix(value, prefix)
		if found && (rest == "" || strings.HasPrefix(rest, "/")) {
			return strings.TrimPrefix(rest, "/"), true
		}
	}
	return "", false
}

// expandHome replaces a leading home-directory spelling with the home
// directory. The value is returned unchanged when the home directory
// cannot be resolved.
func expandHome(value string) string {
	rest, ok := cutHomePrefix(value)
	if !ok {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return value
	}
	return filepath.Join(home, rest)
}

// resolvePath makes p absolute against cwd when it is relative, and
// cleans it.
func resolvePath(cwd, p string) string {
	if filepath.IsAbs(p) || cwd == "" {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(cwd, p))
}

// expandPaths resolves path-valued tool_input fields (not shell words,
// which extractBashPaths expands the way the shell would).
func expandPaths(cwd string, values ...string) []string {
	var out []string
	for _, value := range values {
		if value == "" {
			continue
		}
		out = append(out, resolvePath(cwd, expandHome(value)))
	}
	return out
}

// extractBashPaths parses command as Bash and collects the paths its
// commands, assignments, loops and redirections name, including those
// inside `$(...)`, `<(...)` and subshells. Here-doc bodies and comments
// are not commands, so paths in them are not collected.
func extractBashPaths(cwd string, command string) []string {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		// Not valid Bash: a zsh-only construct, or a command cut short.
		return fallbackBashPaths(cwd, command)
	}
	c := &bashPathCollector{cwd: cwd, cfg: expandConfig()}
	syntax.Walk(file, c.visit)
	return c.paths
}

// fallbackBashPaths takes the whitespace-separated words of a command
// that does not parse and keeps those that look like paths, ignoring
// quoting.
func fallbackBashPaths(cwd, command string) []string {
	var out []string
	for _, word := range strings.Fields(command) {
		if looksLikePathToken(word) {
			out = append(out, resolvePath(cwd, expandHome(word)))
		}
	}
	return out
}

// errUnknownValue fails the expansion of a word whose value only
// exists once the command runs.
var errUnknownValue = errors.New("value not known before the command runs")

// expandConfig makes expand.Literal resolve quotes, backslashes, `~` and
// $HOME the way the shell would, and fail on any other variable and on
// command or process substitution. A word that fails is skipped rather
// than turned into a wrong path ("$TMPDIR/x" is not "/x").
func expandConfig() *expand.Config {
	var env []string
	if home, err := os.UserHomeDir(); err == nil {
		env = append(env, "HOME="+home)
	}
	return &expand.Config{
		Env:     expand.ListEnviron(env...),
		NoUnset: true,
		// expand fails on command substitution when CmdSubst is nil, but
		// calls ProcSubst without checking it.
		ProcSubst: func(*syntax.ProcSubst) (string, error) { return "", errUnknownValue },
	}
}

// bashPathCollector gathers the paths named in one parsed command.
type bashPathCollector struct {
	cwd   string
	cfg   *expand.Config
	paths []string
}

func (c *bashPathCollector) add(p string) {
	c.paths = append(c.paths, resolvePath(c.cwd, p))
}

// literal expands w the way the shell would. ok is false when the value
// is empty or not known before the command runs (see expandConfig); the
// expansion error carries nothing else.
func (c *bashPathCollector) literal(w *syntax.Word) (string, bool) {
	s, err := expand.Literal(c.cfg, w)
	return s, err == nil && s != ""
}

func (c *bashPathCollector) visit(node syntax.Node) bool {
	switch n := node.(type) {
	case *syntax.Redirect:
		// The word after a redirection is the file it reads or writes,
		// whatever it looks like (`> out.txt`).
		if redirectNamesFile(n) {
			if s, ok := c.literal(n.Word); ok {
				c.add(s)
			}
		}
	case *syntax.Assign:
		if s, ok := c.literal(n.Value); ok && looksLikePath(s) {
			c.add(s)
		}
	case *syntax.WordIter:
		for _, w := range n.Items {
			if s, ok := c.literal(w); ok && looksLikePath(s) {
				c.add(s)
			}
		}
	case *syntax.CallExpr:
		c.args(n.Args)
	}
	return true
}

// args collects paths from a command's arguments: the directory of
// `git -C`, words that are paths, and the value of `--file=/x` or
// `of=/dev/sda`.
func (c *bashPathCollector) args(args []*syntax.Word) {
	isGit := len(args) > 0 && args[0].Lit() == "git"
	for i := 0; i < len(args); i++ {
		s, ok := c.literal(args[i])
		if !ok {
			continue
		}
		if isGit && i == 1 {
			if s == "-C" && i+1 < len(args) {
				if dir, ok := c.literal(args[i+1]); ok {
					c.add(dir)
				}
				i++
				continue
			}
			if dir, found := strings.CutPrefix(s, "-C"); found && dir != "" {
				c.add(dir)
				continue
			}
		}
		// A word that is itself a path wins over the `=` rule below, so
		// `./cache=/etc` stays one path.
		if looksLikePath(s) {
			c.add(s)
			continue
		}
		// The value after `=` of an option or assignment. Other words
		// containing `=` (URLs with `?path=/etc/x`) name no local file.
		// expand.Literal only expands a `~` at the start of a word, so
		// one right after the `=` (`prefix=~/opt`) is expanded here.
		if lhs, value, found := strings.Cut(s, "="); found && assignmentLHS.MatchString(lhs) && looksLikePathToken(value) {
			c.add(expandHome(value))
		}
	}
}

// fdOperand matches the operand of a duplicating redirection that names
// a file descriptor rather than a file: `1`, `2-` (move) or `-` (close).
var fdOperand = regexp.MustCompile(`^([0-9]+-?|-)$`)

// redirectNamesFile reports whether a redirection's word is a file name:
// not a here-doc or here-string, and for `>&` / `<&` not a descriptor.
// Bash treats `>& /tmp/out` as redirecting stdout and stderr to that
// file, and `2>&1` as duplicating a descriptor.
func redirectNamesFile(r *syntax.Redirect) bool {
	switch r.Op {
	case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
		return false
	case syntax.DplIn, syntax.DplOut:
		return !fdOperand.MatchString(r.Word.Lit())
	}
	return true
}

// assignmentLHS matches what may precede `=` in an option or variable
// assignment: `--file`, `-o`, `of`, `PREFIX`.
var assignmentLHS = regexp.MustCompile(`^(-{1,2}[A-Za-z0-9][A-Za-z0-9_-]*|[A-Za-z_][A-Za-z0-9_]*)$`)

// looksLikePath reports whether an expanded shell word is a path. It
// does not accept home-directory spellings: expansion has already
// replaced the ones the shell would expand, so any left are literal
// text (`"~/docs"`, `'$HOME'`).
func looksLikePath(s string) bool {
	return s == "." || s == ".." || filepath.IsAbs(s) ||
		strings.HasPrefix(s, "/") ||
		strings.HasPrefix(s, "./") ||
		strings.HasPrefix(s, "../")
}

// looksLikePathToken reports whether an unexpanded word is a path,
// counting a leading home-directory spelling.
func looksLikePathToken(token string) bool {
	if _, ok := cutHomePrefix(token); ok {
		return true
	}
	return looksLikePath(token)
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
