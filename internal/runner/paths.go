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

// extractBashPaths parses command as Bash and collects the words in it
// that are paths (arguments, assignment and array values, loop items,
// test operands), redirection targets, and the paths a few argument
// forms name, including inside `$(...)`, `<(...)` and subshells.
// Here-doc bodies and comments are not words of the command, so paths
// in them are not collected.
func extractBashPaths(cwd string, command string) []string {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		// Not valid Bash: a zsh-only construct, or a command cut short.
		return fallbackBashPaths(cwd, command)
	}
	c := &bashPathCollector{cwd: cwd, cfg: expandConfig(), handled: map[*syntax.Word]bool{}}
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
// than turned into a wrong path ("$TMPDIR/x" is not "/x"). Every other
// variable counts as unset, so `${TMPDIR:-/tmp}` takes its default.
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
	// handled marks words a rule above the generic one already dealt
	// with (redirection operands, here-doc bodies, the git -C directory),
	// so visiting them as plain words neither repeats nor misreads them.
	handled map[*syntax.Word]bool
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

// visit runs on every node of the syntax tree. The walk continues into
// handled words too, because command substitutions inside them (even in
// an unquoted here-doc body) still run.
func (c *bashPathCollector) visit(node syntax.Node) bool {
	switch n := node.(type) {
	case *syntax.Redirect:
		// The word after a redirection is the file it reads or writes,
		// whatever it looks like (`> out.txt`).
		c.handled[n.Word] = true
		if n.Hdoc != nil {
			c.handled[n.Hdoc] = true
		}
		if redirectNamesFile(n) {
			if s, ok := c.literal(n.Word); ok {
				c.add(s)
			}
		}
	case *syntax.CallExpr:
		c.args(n.Args)
	case *syntax.ParamExp, *syntax.ArithmExp, *syntax.ArithmCmd, *syntax.BraceExp:
		c.markFragments(n)
	case *syntax.Word:
		if s, ok := c.literal(n); ok && !c.handled[n] && looksLikePath(s) {
			c.add(s)
		}
	}
	return true
}

// markFragments marks the words inside an expansion or arithmetic as
// handled. They are pieces of a larger value (the `/etc` default of
// `"./cache/${HOME:-/etc}"`), not paths of their own. Words in command
// and process substitutions are left alone, since those are commands.
func (c *bashPathCollector) markFragments(node syntax.Node) {
	syntax.Walk(node, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst:
			return false
		case *syntax.Word:
			c.handled[n] = true
		}
		return true
	})
}

// args collects what a command's arguments name besides the words that
// are paths themselves, which visit collects: the directory of `git -C`
// and the value after `=` of an option or assignment.
func (c *bashPathCollector) args(args []*syntax.Word) {
	if len(args) > 1 && args[0].Lit() == "git" {
		if s, ok := c.literal(args[1]); ok {
			switch {
			case s == "-C" && len(args) > 2:
				c.handled[args[2]] = true
				if dir, ok := c.literal(args[2]); ok {
					c.add(dir)
				}
			case len(s) > len("-C") && strings.HasPrefix(s, "-C"):
				c.add(strings.TrimPrefix(s, "-C"))
			}
		}
	}
	for _, w := range args {
		if value, ok := c.valueAfterEquals(w); ok && looksLikePath(value) {
			c.add(value)
		}
	}
}

// valueAfterEquals returns the value after the first `=` of an option or
// assignment word (`--file=/x`, `of=/dev/sda`). Quoted text in it stays
// literal (`--output='$HOME/out'`). Other words containing `=` (URLs with
// `?path=/etc/x`, paths such as `./cache=/etc`) have no such value.
func (c *bashPathCollector) valueAfterEquals(w *syntax.Word) (string, bool) {
	// An unquoted `=` in the leading literal: expand what follows it as a
	// word of its own, so a `~` right after the `=` expands
	// (`prefix=~/opt`), as Bash does for assignment-like arguments.
	if len(w.Parts) == 0 {
		return "", false
	}
	if lit, ok := w.Parts[0].(*syntax.Lit); ok {
		if lhs, rest, found := strings.Cut(lit.Value, "="); found {
			if !assignmentLHS.MatchString(lhs) {
				return "", false
			}
			parts := append([]syntax.WordPart{&syntax.Lit{Value: rest}}, w.Parts[1:]...)
			return c.literal(&syntax.Word{Parts: parts})
		}
	}
	// Otherwise the `=` is quoted (`'--output=/x'`). No tilde after it
	// expands, and expanding the whole word already applied the quoting,
	// so the result is split as it is.
	s, ok := c.literal(w)
	if !ok {
		return "", false
	}
	lhs, value, found := strings.Cut(s, "=")
	if !found || !assignmentLHS.MatchString(lhs) || value == "" {
		return "", false
	}
	return value, true
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
