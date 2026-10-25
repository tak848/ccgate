package runner

import (
	"bytes"
	"os"
	"path/filepath"
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

// homePrefixes are the spellings of the home directory a shell command
// commonly starts a path with. shellSplit keeps `$HOME` literal, so
// they are expanded here.
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

func expandPaths(cwd string, values ...string) []string {
	var out []string
	for _, value := range values {
		if value == "" {
			continue
		}
		value = expandHome(value)
		if filepath.IsAbs(value) {
			out = append(out, filepath.Clean(value))
			continue
		}
		if cwd != "" {
			out = append(out, filepath.Clean(filepath.Join(cwd, value)))
			continue
		}
		out = append(out, filepath.Clean(value))
	}
	return out
}

func extractBashPaths(cwd string, command string) []string {
	tokens := shellSplit(command)
	if len(tokens) == 0 {
		return nil
	}

	var candidates []string
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token == "" {
			continue
		}

		if token == "git" && i+2 < len(tokens) && tokens[i+1] == "-C" {
			candidates = append(candidates, tokens[i+2])
			i += 2
			continue
		}
		if token == "git" && i+1 < len(tokens) && strings.HasPrefix(tokens[i+1], "-C") && len(tokens[i+1]) > 2 {
			candidates = append(candidates, strings.TrimPrefix(tokens[i+1], "-C"))
			i++
			continue
		}
		// Redirections written without a space (`>/etc/x`, `2>>~/log`,
		// `cmd>/x`): the target follows the operator inside the token.
		if before, target, ok := splitRedirect(token); ok {
			for _, part := range []string{before, target} {
				if looksLikePathToken(part) {
					candidates = append(candidates, part)
				}
			}
			continue
		}
		// `--file=/x`, `of=/dev/sda`, `PREFIX=~/opt`: the value after `=`.
		if _, rhs, found := strings.Cut(token, "="); found && looksLikePathToken(rhs) {
			candidates = append(candidates, rhs)
			continue
		}
		if looksLikePathToken(token) {
			candidates = append(candidates, token)
		}
	}

	return expandPaths(cwd, candidates...)
}

// splitRedirect splits a token at its first shell redirection operator
// (`>`, `>>`, `<`, `>|`, `&>`, `2>&`, ...) into what precedes the
// operator and the redirection target after it.
func splitRedirect(token string) (before, target string, ok bool) {
	i := strings.IndexAny(token, "<>")
	if i < 0 {
		return "", "", false
	}
	j := i
	for j < len(token) && strings.ContainsRune("<>|&", rune(token[j])) {
		j++
	}
	return token[:i], token[j:], true
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

func shellSplit(input string) []string {
	var fields []string
	var current bytes.Buffer
	inSingle := false
	inDouble := false
	escaped := false

	flush := func() {
		if current.Len() == 0 {
			return
		}
		fields = append(fields, current.String())
		current.Reset()
	}

	for _, r := range input {
		switch {
		case escaped:
			current.WriteRune(r)
			escaped = false
		case r == '\\' && !inSingle:
			escaped = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case (r == ' ' || r == '\t' || r == '\n') && !inSingle && !inDouble:
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return fields
}
