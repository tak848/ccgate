package runner

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
// which extractBashPaths handles with their quoting).
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

func extractBashPaths(cwd string, command string) []string {
	words := shellSplit(command)
	var out []string
	add := func(p string) { out = append(out, resolvePath(cwd, p)) }

	for i := 0; i < len(words); i++ {
		w := words[i]

		// The word after a redirection operator is the file it reads or
		// writes, whatever it looks like (`> out.txt`), except for fd
		// duplication (`2>&1`) and here-doc delimiters (`<<EOF`).
		if w.redirect {
			if i+1 < len(words) && !words[i+1].redirect {
				if redirectNamesFile(w.text, words[i+1].text) {
					target := words[i+1]
					if p, ok := target.pathAt(0); ok {
						add(p)
					} else {
						add(target.text)
					}
				}
				i++
			}
			continue
		}

		if w.text == "git" && i+2 < len(words) && words[i+1].text == "-C" {
			add(words[i+2].pathOrText())
			i += 2
			continue
		}
		if w.text == "git" && i+1 < len(words) && strings.HasPrefix(words[i+1].text, "-C") && len(words[i+1].text) > 2 {
			if p, ok := words[i+1].pathAt(len("-C")); ok {
				add(p)
			} else {
				add(strings.TrimPrefix(words[i+1].text, "-C"))
			}
			i++
			continue
		}

		// A word that is itself a path wins over the `=` rule below, so
		// `./cache=/etc` stays one path.
		if p, ok := w.pathAt(0); ok {
			add(p)
			continue
		}
		// `--file=/x`, `of=/dev/sda`, `PREFIX=~/opt`: the value after `=`
		// of an option or assignment. Other words containing `=` (URLs
		// with `?path=/etc/x`) name no local file.
		if k := assignmentValueIndex(w.text); k >= 0 {
			if p, ok := w.pathAt(k); ok {
				add(p)
			}
		}
	}
	return out
}

// fdOperand matches the operand of a duplicating redirection that names
// a file descriptor rather than a file: `1`, `2-` (move) or `-` (close).
var fdOperand = regexp.MustCompile(`^([0-9]+-?|-)$`)

// redirectNamesFile reports whether operand, the word after the
// redirection operator op, is a file name. `>&` and `<&` duplicate a
// descriptor only when the operand is one (`2>&1`); Bash treats
// `>& /tmp/out` as redirecting stdout and stderr to that file.
func redirectNamesFile(op, operand string) bool {
	if strings.HasPrefix(op, "<<") {
		return false
	}
	if strings.HasSuffix(op, "&") {
		return !fdOperand.MatchString(operand)
	}
	return true
}

// assignmentLHS matches what may precede `=` in an option or variable
// assignment: `--file`, `-o`, `of`, `PREFIX`.
var assignmentLHS = regexp.MustCompile(`^(-{1,2}[A-Za-z0-9][A-Za-z0-9_-]*|[A-Za-z_][A-Za-z0-9_]*)$`)

// assignmentValueIndex returns the byte index just after `=` when text
// is an option or assignment, and -1 otherwise.
func assignmentValueIndex(text string) int {
	lhs, _, found := strings.Cut(text, "=")
	if !found || !assignmentLHS.MatchString(lhs) {
		return -1
	}
	return len(lhs) + 1
}

func looksLikePathToken(token string) bool {
	if token == "." || token == ".." {
		return true
	}
	if _, ok := cutHomePrefix(token); ok {
		return true
	}
	return strings.HasPrefix(token, "/") ||
		strings.HasPrefix(token, "./") ||
		strings.HasPrefix(token, "../")
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

// quoteState is how one byte of a shell word was written.
type quoteState byte

const (
	unquoted quoteState = iota
	singleQuoted
	doubleQuoted
	backslashEscaped
)

// shellWord is one word of a shell command, or an unquoted redirection
// operator. quotes records how each byte of text was quoted, because
// the shell expands `~` and `$HOME` and recognises `<` / `>` only in
// some of those states.
type shellWord struct {
	text     string
	quotes   []quoteState
	redirect bool
}

// pathAt returns the path that starts at byte i of the word, with a
// leading home-directory spelling expanded when the shell would expand
// it there: `~` only unquoted, `$HOME` / `${HOME}` unquoted or inside
// double quotes. ok is false when that part of the word does not look
// like a path.
func (w shellWord) pathAt(i int) (string, bool) {
	s := w.text[i:]
	if !looksLikePathToken(s) {
		return "", false
	}
	if _, isHome := cutHomePrefix(s); isHome {
		q := w.quotes[i]
		if q != unquoted && (strings.HasPrefix(s, "~") || q != doubleQuoted) {
			return "", false
		}
		return expandHome(s), true
	}
	return s, true
}

func (w shellWord) pathOrText() string {
	if p, ok := w.pathAt(0); ok {
		return p
	}
	return w.text
}

// shellSplit splits a command into words the way a POSIX shell would for
// the purpose of finding paths: whitespace and unquoted redirection
// operators separate words, quotes and backslashes are removed but
// remembered per byte. It does not expand variables or split on `|`,
// `;` or `&&`.
func shellSplit(input string) []shellWord {
	var words []shellWord
	var cur strings.Builder
	var quotes []quoteState
	inSingle := false
	inDouble := false
	escaped := false

	add := func(r rune, q quoteState) {
		n := cur.Len()
		cur.WriteRune(r)
		for range cur.Len() - n {
			quotes = append(quotes, q)
		}
	}
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, shellWord{text: cur.String(), quotes: quotes})
		}
		cur.Reset()
		quotes = nil
	}

	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case escaped:
			add(r, backslashEscaped)
			escaped = false
		case r == '\\' && !inSingle:
			escaped = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case inSingle:
			add(r, singleQuoted)
		case inDouble:
			add(r, doubleQuoted)
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		case r == '<' || r == '>':
			// A file descriptor number or `&` right before the operator is
			// part of it (`2>`, `&>`); anything else ends the word.
			if isRedirectPrefix(cur.String(), quotes) {
				cur.Reset()
				quotes = nil
			} else {
				flush()
			}
			j := i
			for j < len(runes) && strings.ContainsRune("<>|&", runes[j]) {
				j++
			}
			words = append(words, shellWord{text: string(runes[i:j]), redirect: true})
			i = j - 1
		default:
			add(r, unquoted)
		}
	}
	flush()
	return words
}

// isRedirectPrefix reports whether the pending word is an unquoted fd
// number or `&` that belongs to the redirection operator after it.
func isRedirectPrefix(text string, quotes []quoteState) bool {
	if text == "" {
		return false
	}
	for i, c := range []byte(text) {
		if quotes[i] != unquoted || (c != '&' && (c < '0' || c > '9')) {
			return false
		}
	}
	return text == "&" || !strings.Contains(text, "&")
}
