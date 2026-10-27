package cli

import (
	"fmt"
	"go/doc/comment"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

const (
	defaultHelpWidth = 80
	helpIndent       = 2
	helpColumnGap    = 4
)

func helpWidth(w io.Writer) int {
	if width, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && width > 0 {
		return width
	}
	if f, ok := w.(*os.File); ok {
		if width, _, err := term.GetSize(int(f.Fd())); err == nil && width > 0 {
			return width
		}
	}
	return defaultHelpWidth
}

func targetLabel(target string) string {
	switch target {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex CLI"
	default:
		return "Devin"
	}
}

func commandDescription(name string) string {
	parts := strings.Fields(name)
	if len(parts) == 0 {
		return "ccgate -- PermissionRequest hook for AI coding tools (Claude Code, OpenAI Codex CLI, Devin).\nNo args + stdin pipe = Claude Code hook (legacy invocation, permanent)."
	}
	if len(parts) == 1 {
		switch name {
		case "clm", "com", "dvm":
			c := command{path: parts}
			return "Shortcut for 'ccgate " + c.target() + " metrics'."
		case "init", "metrics":
			return "[removed in v0.6] Use 'ccgate claude " + name + "', 'ccgate codex " + name + "', or 'ccgate devin " + name + "' instead."
		case "claude":
			return "Run the Claude Code PermissionRequest hook (or manage its config / metrics). With no sub-sub-command, runs the hook from stdin."
		case "codex":
			return "Run the OpenAI Codex CLI PermissionRequest hook (experimental). With no sub-sub-command, runs the hook from stdin."
		default:
			return "Run the Devin PermissionRequest hook. With no sub-sub-command, runs the hook from stdin."
		}
	}
	label := targetLabel(parts[0])
	switch parts[1] {
	case "init":
		return "Output the embedded " + label + " default configuration."
	case "metrics":
		return "Show " + label + " usage metrics."
	default:
		invocation := "'ccgate " + parts[0] + "'"
		if parts[0] == "claude" {
			invocation = "bare 'ccgate' or " + invocation
		}
		return "Run the " + label + " hook from stdin (default; same as " + invocation + ")."
	}
}

func writeHelp(w io.Writer, c command) error {
	var out strings.Builder
	line := func(s string) { out.WriteString(strings.TrimRight(s, " ") + "\n") }
	width := helpWidth(w)
	wrap := func(text, prefix string, limit int) []string {
		parser := comment.Parser{}
		printer := comment.Printer{TextWidth: limit, TextCodePrefix: "    "}
		text = strings.TrimSpace(string(printer.Text(parser.Parse(text))))
		lines := strings.Split(text, "\n")
		for i := range lines {
			lines[i] = prefix + lines[i]
		}
		return lines
	}
	usage := "Usage: ccgate"
	if c.name() != "" {
		usage += " " + c.name()
	}
	group := len(c.path) == 0 || (len(c.path) == 1 && c.action() == "hook")
	if group {
		usage += " <command>"
	}
	if !strings.HasSuffix(c.name(), " hook") {
		usage += " [flags]"
	}
	line(usage)
	line("")
	for _, text := range wrap(commandDescription(c.name()), "", width) {
		line(text)
	}
	line("")
	line("Flags:")
	flags := c.flags()
	labels := make([]string, len(flags))
	leftWidth := 0
	for i, f := range flags {
		label := "    --" + f.name
		if f.short != "" {
			label = "-" + f.short + ", --" + f.name
		}
		if f.value != "" {
			label += "=" + f.value
		}
		labels[i] = label
		leftWidth = max(leftWidth, len(label))
	}
	for i, f := range flags {
		if i == 2 {
			line("")
		}
		texts := wrap(f.help, "", width-helpIndent-leftWidth-helpColumnGap)
		line(strings.Repeat(" ", helpIndent) + fmt.Sprintf("%-*s", leftWidth, labels[i]) + strings.Repeat(" ", helpColumnGap) + texts[0])
		for _, text := range texts[1:] {
			line(strings.Repeat(" ", helpIndent+leftWidth+helpColumnGap) + text)
		}
	}
	if group {
		line("")
		line("Commands:")
		targets := []string{"claude", "codex", "devin"}
		if len(c.path) == 1 {
			targets = c.path
		}
		var names []string
		for _, target := range targets {
			for _, action := range []string{"hook", "init", "metrics"} {
				names = append(names, target+" "+action)
			}
		}
		if len(c.path) == 0 {
			names = append(names, "clm", "com", "dvm", "init", "metrics")
		}
		for i, name := range names {
			if i > 0 {
				line("")
			}
			summary := name
			if !strings.HasSuffix(name, " hook") {
				summary += " [flags]"
			}
			line("  " + summary)
			for _, text := range wrap(commandDescription(name), "    ", width-helpColumnGap) {
				line(text)
			}
		}
	}
	if len(c.path) == 0 {
		line("")
		line(`Run "ccgate <command> --help" for more information on a command.`)
	}
	if _, err := io.WriteString(w, out.String()); err != nil {
		return fmt.Errorf("write help: %w", err)
	}
	return nil
}
