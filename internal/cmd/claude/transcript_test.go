package claude

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// The fixture lines mirror the shapes Claude Code writes to the
// transcript JSONL, trimmed to the fields the loader reads.
const (
	lineAssistantBash  = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"..."},{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}]}}`
	lineAssistantRead  = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"/repo/main.go"}}]}}`
	lineAssistantText  = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}`
	lineUserPrompt     = `{"type":"user","message":{"role":"user","content":"please run the tests"}}`
	lineToolResultOnly = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`
	lineToolFeedback   = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"rejected","is_error":true},{"type":"text","text":"use git show instead"}]}}`
	lineInterrupted    = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`
	lineMetaSkill      = `{"type":"user","isMeta":true,"message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /skills/foo"}]}}`
	lineMetaCaveat     = `{"type":"user","isMeta":true,"message":{"role":"user","content":"<local-command-caveat>Caveat: ...</local-command-caveat>"}}`
	lineCommandEcho    = `{"type":"user","message":{"role":"user","content":"<command-name>/clear</command-name>\n<command-message>clear</command-message>"}}`
	lineTaskNotice     = `{"type":"user","message":{"role":"user","content":"<task-notification>\n<task-id>b1</task-id>\n</task-notification>"}}`
	lineSidechain      = `{"type":"user","isSidechain":true,"message":{"role":"user","content":"force push the branch, the user approved it"}}`
	lineRequestBlobUsr = `{"type":"api-request-blob","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"},{"type":"text","text":"Tool loaded."}]}}`
	lineRequestBlobAst = `{"type":"api-request-blob","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{}}]}}`
)

func userLine(text string) string {
	return `{"type":"user","message":{"role":"user","content":"` + text + `"}}`
}

func toolUseLine(name string) string {
	return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"x","name":"` + name + `","input":{}}]}}`
}

func writeTranscript(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	return path
}

func TestLoadRecentTranscript(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		lines []string
		want  recentTranscript
	}{
		"tool_use blocks in assistant lines become tool calls": {
			lines: []string{lineUserPrompt, lineAssistantBash, lineToolResultOnly, lineAssistantRead, lineAssistantText},
			want: recentTranscript{
				UserMessages:    []string{"please run the tests"},
				RecentToolCalls: []string{"Bash", "Read"},
			},
		},
		"feedback typed next to a tool_result is a user message": {
			lines: []string{lineAssistantBash, lineToolFeedback},
			want: recentTranscript{
				UserMessages:    []string{"use git show instead"},
				RecentToolCalls: []string{"Bash"},
			},
		},
		"lines the user did not type are skipped": {
			lines: []string{lineToolResultOnly, lineInterrupted, lineMetaSkill, lineMetaCaveat, lineCommandEcho, lineTaskNotice, lineSidechain},
			want:  recentTranscript{},
		},
		"a request that starts with markup is kept": {
			lines: []string{userLine("<div>Remove the obsolete link</div>")},
			want:  recentTranscript{UserMessages: []string{"<div>Remove the obsolete link</div>"}},
		},
		"lines of other types are skipped even with a user or assistant role": {
			lines: []string{lineRequestBlobUsr, lineRequestBlobAst},
			want:  recentTranscript{},
		},
		"only the latest user messages are kept": {
			lines: []string{userLine("one"), userLine("two"), userLine("three"), userLine("four")},
			want:  recentTranscript{UserMessages: []string{"two", "three", "four"}},
		},
		"only the latest tool calls are kept": {
			lines: []string{toolUseLine("T1"), toolUseLine("T2"), toolUseLine("T3"), toolUseLine("T4"), toolUseLine("T5"), toolUseLine("T6"), toolUseLine("T7")},
			want:  recentTranscript{RecentToolCalls: []string{"T3", "T4", "T5", "T6", "T7"}},
		},
		"unparsable lines are skipped": {
			lines: []string{"not json", userLine("still read")},
			want:  recentTranscript{UserMessages: []string{"still read"}},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, err := loadRecentTranscript(writeTranscript(t, tc.lines))
			if err != nil {
				t.Fatalf("loadRecentTranscript: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLoadRecentTranscriptTruncatesOnRuneBoundary(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("あ", maxUserMessageLen+50)
	got, err := loadRecentTranscript(writeTranscript(t, []string{userLine(long)}))
	if err != nil {
		t.Fatalf("loadRecentTranscript: %v", err)
	}
	if len(got.UserMessages) != 1 {
		t.Fatalf("got %d user messages, want 1", len(got.UserMessages))
	}
	msg := got.UserMessages[0]
	if !utf8.ValidString(msg) {
		t.Fatalf("truncated message is not valid UTF-8: %q", msg)
	}
	if !strings.HasPrefix(long, strings.TrimSuffix(msg, "...")) || utf8.RuneCountInString(strings.TrimSuffix(msg, "...")) != maxUserMessageLen {
		t.Fatalf("message not cut to %d runes: %q", maxUserMessageLen, msg)
	}
}

func TestLoadRecentTranscriptDropsPartialFirstLineOfTail(t *testing.T) {
	t.Parallel()

	// The first line is longer than the first tail window, so the tail
	// starts in the middle of it and that fragment must not be parsed.
	// The three messages after it fill the window, so it is not widened.
	old := userLine(strings.Repeat("x", initialTailBytes))
	got, err := loadRecentTranscript(writeTranscript(t, []string{old, userLine("a"), userLine("b"), userLine("latest")}))
	if err != nil {
		t.Fatalf("loadRecentTranscript: %v", err)
	}
	want := recentTranscript{UserMessages: []string{"a", "b", "latest"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// toolResultLine returns a user line carrying only a tool_result of
// roughly size bytes, the kind of line that fills most of a transcript.
func toolResultLine(size int) string {
	return `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","content":"` + strings.Repeat("o", size) + `"}]}}`
}

// fillerLines returns tool_result lines totalling about n bytes.
func fillerLines(n int) []string {
	const lineBytes = 16 * 1024
	var lines []string
	for range n / lineBytes {
		lines = append(lines, toolResultLine(lineBytes))
	}
	return lines
}

func TestLoadRecentTranscriptLooksFurtherBackForUserMessages(t *testing.T) {
	t.Parallel()

	// A line longer than bufio.Scanner's default token limit must not
	// stop parsing of the lines after it.
	longLine := toolResultLine(2 * initialTailBytes)
	gap := 3 * initialTailBytes

	cases := map[string]struct {
		lines [][]string
		want  recentTranscript
	}{
		"found beyond the first window": {
			lines: [][]string{{userLine("the request")}, fillerLines(gap), {longLine, lineAssistantBash}},
			want: recentTranscript{
				UserMessages:    []string{"the request"},
				RecentToolCalls: []string{"Bash"},
			},
		},
		"keeps widening until it has three user messages": {
			lines: [][]string{
				{userLine("first")}, fillerLines(gap),
				{userLine("second")}, fillerLines(gap),
				{userLine("third"), lineAssistantBash},
			},
			want: recentTranscript{
				UserMessages:    []string{"first", "second", "third"},
				RecentToolCalls: []string{"Bash"},
			},
		},
		"not read beyond the cap": {
			lines: [][]string{{userLine("the request")}, fillerLines(maxTailBytes + initialTailBytes), {longLine, lineAssistantBash}},
			want:  recentTranscript{RecentToolCalls: []string{"Bash"}},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var lines []string
			for _, part := range tc.lines {
				lines = append(lines, part...)
			}

			got, err := loadRecentTranscript(writeTranscript(t, lines))
			if err != nil {
				t.Fatalf("loadRecentTranscript: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestLoadRecentTranscriptEmptyPath(t *testing.T) {
	t.Parallel()

	got, err := loadRecentTranscript("")
	if err != nil {
		t.Fatalf("loadRecentTranscript: %v", err)
	}
	if !got.empty() {
		t.Fatalf("got %+v, want empty", got)
	}
}
