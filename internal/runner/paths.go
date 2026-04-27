package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

func referencedPaths(input HookInput) []string {
	switch input.ToolName {
	case "Read", "Write", "Edit", "MultiEdit":
		return uniqueNonEmpty(expandPaths(input.Cwd, input.ToolInput.FilePath))
	case "Glob":
		return uniqueNonEmpty(expandPaths(input.Cwd, input.ToolInput.Path, input.ToolInput.Pattern))
	case "Grep":
		return uniqueNonEmpty(expandPaths(input.Cwd, input.ToolInput.Path))
	case "Bash":
		return uniqueNonEmpty(extractBashPaths(input.Cwd, input.ToolInput.Command))
	default:
		return nil
	}
}

func expandPaths(cwd string, values ...string) []string {
	var out []string
	for _, value := range values {
		if value == "" {
			continue
		}
		if after, ok := strings.CutPrefix(value, "~/"); ok {
			if home, err := os.UserHomeDir(); err == nil {
				value = filepath.Join(home, after)
			}
		}
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
		if strings.HasPrefix(token, "--") && strings.Contains(token, "=") {
			_, rhs, found := strings.Cut(token, "=")
			if found && looksLikePathToken(rhs) {
				candidates = append(candidates, rhs)
				continue
			}
		}
		if looksLikePathToken(token) {
			candidates = append(candidates, token)
		}
	}

	return expandPaths(cwd, candidates...)
}

func looksLikePathToken(token string) bool {
	if token == "." || token == ".." {
		return true
	}
	return strings.HasPrefix(token, "/") ||
		strings.HasPrefix(token, "./") ||
		strings.HasPrefix(token, "../") ||
		strings.HasPrefix(token, "~/")
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
