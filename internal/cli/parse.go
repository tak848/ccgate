package cli

import (
	"flag"
	"fmt"
	"io"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultDays        = 7
	defaultDetails     = 10
	suggestionDistance = 2
)

type command struct {
	path    []string
	show    string
	project bool
	output  string
	force   bool
	days    int
	json    bool
	details int
}

func (c command) name() string { return strings.Join(c.path, " ") }

func (c command) target() string {
	switch c.name() {
	case "clm":
		return "claude"
	case "com":
		return "codex"
	case "dvm":
		return "devin"
	default:
		return c.path[0]
	}
}

func (c command) action() string {
	switch c.name() {
	case "clm", "com", "dvm":
		return "metrics"
	}
	if len(c.path) == 2 {
		return c.path[1]
	}
	if len(c.path) == 1 && (c.path[0] == "init" || c.path[0] == "metrics") {
		return "deprecated"
	}
	return "hook"
}

type flagSpec struct {
	name, short, value, help string
}

func (c command) flags() []flagSpec {
	flags := []flagSpec{
		{name: "help", short: "h", help: "Show context-sensitive help."},
		{name: "version", help: "Print version and exit."},
	}
	if len(c.path) == 0 {
		return flags
	}
	switch c.action() {
	case "init":
		flags = append(flags,
			flagSpec{name: "project", short: "p", help: "Output the project-local configuration template instead of the global one."},
			flagSpec{name: "output", short: "o", value: "STRING", help: "Write to FILE instead of stdout."},
			flagSpec{name: "force", short: "f", help: "Overwrite an existing file at --output."},
		)
	case "metrics":
		flags = append(flags,
			flagSpec{name: "days", value: strconv.Itoa(defaultDays), help: "Show last N days."},
			flagSpec{name: "json", help: "Output as JSON."},
			flagSpec{name: "details", value: strconv.Itoa(defaultDetails), help: "Show top-N fallthrough/deny commands per section. Use 0 to hide both sections."},
		)
	}
	return flags
}

// boolValue preserves the CLI's yes/no spellings without reflection.
type boolValue bool

func (v *boolValue) String() string { return strconv.FormatBool(bool(*v)) }

func (v *boolValue) Set(s string) error {
	switch strings.ToLower(s) {
	case "true", "1", "yes":
		*v = true
	case "false", "0", "no":
		*v = false
	default:
		return fmt.Errorf("bool value must be true, 1, yes, false, 0 or no but got %q", strings.ToLower(s))
	}
	return nil
}

// parseCommand handles the fixed root/target/action layout. FlagSet owns value
// decoding; the small token loop preserves short clusters and flag scope.
func parseCommand(args []string) (command, error) {
	c := command{days: defaultDays, details: defaultDetails}
	fs := flag.NewFlagSet("ccgate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var help, version boolValue
	fs.Var(&help, "help", "")
	fs.Var(&version, "version", "")
	fs.Var((*boolValue)(&c.project), "project", "")
	fs.StringVar(&c.output, "output", "", "")
	fs.Var((*boolValue)(&c.force), "force", "")
	fs.IntVar(&c.days, "days", defaultDays, "")
	fs.Var((*boolValue)(&c.json), "json", "")
	fs.IntVar(&c.details, "details", defaultDetails, "")
	flagsEnded, outputSet := false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !flagsEnded && arg == "--" {
			flagsEnded = true
			continue
		}
		if flagsEnded || arg == "-" || !strings.HasPrefix(arg, "-") {
			choices := []string{}
			switch len(c.path) {
			case 0:
				choices = []string{"claude", "codex", "devin", "clm", "com", "dvm", "init", "metrics"}
			case 1:
				if c.action() == "hook" {
					choices = []string{"hook", "init", "metrics"}
				}
			}
			found := false
			for _, choice := range choices {
				if arg == choice {
					found = true
					break
				}
			}
			if !found {
				return command{}, suggest(arg, choices, "unexpected argument "+arg)
			}
			c.path = append(c.path, arg)
			continue
		}
		long := strings.HasPrefix(arg, "--")
		token := strings.TrimPrefix(arg, "-")
		if long {
			token = strings.TrimPrefix(token, "-")
		}
		for token != "" {
			var name, value string
			var hasValue bool
			if long {
				name, value, hasValue = strings.Cut(token, "=")
				token = ""
			} else {
				name, token = token[:1], token[1:]
			}
			var spec *flagSpec
			var candidates []string
			for _, f := range c.flags() {
				candidates = append(candidates, "--"+f.name)
				if f.short != "" {
					candidates = append(candidates, "-"+f.short)
				}
				if (long && name == f.name) || (!long && name == f.short) {
					copy := f
					spec = &copy
				}
			}
			prefix := "-"
			if long {
				prefix = "--"
			}
			if spec == nil {
				return command{}, suggest(prefix+name, candidates, "unknown flag "+prefix+name)
			}
			if spec.value == "" {
				if !hasValue {
					value = "true"
				}
			} else if !hasValue {
				if !long && token != "" {
					value, token = token, ""
				} else {
					kind := "int"
					if spec.name == "output" {
						kind = "file"
					}
					if i+1 == len(args) {
						return command{}, fmt.Errorf("--%s: expected %s value but got %q (<EOL>)", spec.name, kind, "EOL")
					}
					i++
					value = args[i]
					if strings.HasPrefix(value, "-") && value != "-" {
						flagKind := "short flag"
						if strings.HasPrefix(value, "--") {
							flagKind = "long flag"
						}
						return command{}, fmt.Errorf("--%s: expected %s value but got %q (%s); perhaps try --%s=%q?", spec.name, kind, value, flagKind, spec.name, value)
					}
				}
			}
			if err := fs.Set(spec.name, value); err != nil {
				if spec.name == "days" || spec.name == "details" {
					return command{}, fmt.Errorf("--%s: expected a valid %d bit int but got %q", spec.name, strconv.IntSize, value)
				}
				return command{}, fmt.Errorf("--%s: %w", spec.name, err)
			}
			if (spec.name == "help" || spec.name == "version") && c.show == "" {
				c.show = spec.name
			}
			outputSet = outputSet || spec.name == "output"
		}
	}
	if len(c.path) == 0 && c.show == "" {
		//nolint:staticcheck // Preserve the established abbreviated command list.
		return command{}, fmt.Errorf("expected one of %q, %q, %q, %q, %q, ...", "claude", "codex", "devin", "clm", "com")
	}
	if outputSet && c.output != "-" {
		var err error
		c.output, err = expandOutputPath(c.output)
		if err != nil {
			return command{}, err
		}
	}
	return c, nil
}

func expandOutputPath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	if strings.HasPrefix(path, "~/") {
		account, err := user.Current()
		if err != nil {
			return "", fmt.Errorf("resolve output home directory: %w", err)
		}
		return filepath.Join(account.HomeDir, path[2:]), nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve output path: %w", err)
	}
	return absolute, nil
}

func suggest(needle string, choices []string, message string) error {
	var matches []string
	for _, choice := range choices {
		if strings.HasPrefix(choice, needle) || editDistance(needle, choice) <= suggestionDistance {
			matches = append(matches, strconv.Quote(choice))
		}
	}
	switch len(matches) {
	case 0:
		return fmt.Errorf("%s", message)
	case 1:
		return fmt.Errorf("%s, did you mean %s?", message, matches[0])
	default:
		return fmt.Errorf("%s, did you mean one of %s?", message, strings.Join(matches, ", "))
	}
}

func editDistance(a, b string) int {
	previous := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := range len(a) {
		current := make([]int, len(b)+1)
		current[0] = i + 1
		for j := range len(b) {
			cost := 0
			if a[i] != b[j] {
				cost = 1
			}
			current[j+1] = min(current[j]+1, previous[j+1]+1, previous[j]+cost)
		}
		previous = current
	}
	return previous[len(b)]
}
