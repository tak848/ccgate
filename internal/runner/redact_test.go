package runner

import (
	"strings"
	"testing"
)

// TestRedactedUserMessageStripsSensitivePayloads documents which
// payload keys must be wiped before the user message reaches
// ccgate.log. Both the parsed canonical view (tool_input) AND the
// verbatim upstream bytes (tool_input_raw) carry the same secrets
// (Edit/Write content_updates, Bash command arguments, apply_patch
// hunks); redacting only one leaves the other on disk. The
// permission_suggestions / recent_transcript values can include
// quoted user prompts that we likewise do not want logged.
func TestRedactedUserMessageStripsSensitivePayloads(t *testing.T) {
	t.Parallel()

	const sentinel = "ZZZ-secret-do-not-log-ZZZ"

	user := `{
  "tool_name": "Edit",
  "tool_input": {"file_path": "secrets.txt", "content": "` + sentinel + `"},
  "tool_input_raw": {"file_path": "secrets.txt", "content": "` + sentinel + `"},
  "permission_suggestions": ["` + sentinel + `"],
  "recent_transcript": {"entries": ["` + sentinel + `"]},
  "context": {"cwd": "/work/repo"}
}`

	got := redactedUserMessage(user)

	if strings.Contains(got, sentinel) {
		t.Fatalf("redactedUserMessage leaked sentinel %q to log output:\n%s", sentinel, got)
	}
	if !strings.Contains(got, "/work/repo") {
		t.Errorf("redaction wiped non-sensitive context (cwd should still be present): %s", got)
	}
}
