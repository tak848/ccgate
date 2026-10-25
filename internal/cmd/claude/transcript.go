package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// recentTranscript holds recent user messages and tool operations from the session transcript.
type recentTranscript struct {
	UserMessages    []string `json:"user_messages,omitempty"`
	RecentToolCalls []string `json:"recent_tool_calls,omitempty"`
}

func (t recentTranscript) empty() bool {
	return len(t.UserMessages) == 0 && len(t.RecentToolCalls) == 0
}

const (
	maxUserMessages   = 3
	maxToolCalls      = 5
	maxUserMessageLen = 200

	// The tail window starts at initialTailBytes and grows by
	// tailGrowthFactor until it holds maxUserMessages user messages,
	// covers the whole file, or reaches maxTailBytes. Tool results make
	// up most of a transcript, so the user's recent messages are often
	// further back than the first window.
	initialTailBytes = 64 * 1024
	tailGrowthFactor = 4
	maxTailBytes     = 4 * 1024 * 1024
)

// interruptMarkerPrefix starts the text block Claude Code writes as a
// user turn when the user interrupts a response or a tool call. It
// records that an interruption happened, not anything the user asked for.
const interruptMarkerPrefix = "[Request interrupted by user"

// harnessTags are the wrapper tags Claude Code puts around text it
// writes as a user turn: slash-command echoes, local command output, and
// background-task results, none of which the user typed. Only these
// known tags are dropped, so a real request that happens to start with
// markup such as <div> still reaches the classifier.
var harnessTags = []string{
	"<command-name>",
	"<command-message>",
	"<local-command-stdout>",
	"<local-command-stderr>",
	"<local-command-caveat>",
	"<task-notification>",
}

func hasHarnessTag(text string) bool {
	for _, tag := range harnessTags {
		if strings.HasPrefix(text, tag) {
			return true
		}
	}
	return false
}

// transcriptEntry is the part of a Claude Code transcript JSONL line
// the loader reads. Tool calls are tool_use blocks inside assistant
// lines. User lines carry either a plain string or an array of blocks:
// tool_result blocks, plus a text block when the user typed feedback
// while answering a permission prompt.
type transcriptEntry struct {
	Type string `json:"type"`
	// IsMeta marks lines Claude Code generated itself (skill bodies,
	// slash-command expansions, local-command caveats).
	IsMeta bool `json:"isMeta"`
	// IsSidechain marks subagent lines, whose user turns are prompts
	// written by the parent agent rather than by the user.
	IsSidechain bool `json:"isSidechain"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
}

// loadRecentTranscript reads the tail of the transcript JSONL and extracts
// the most recent user messages and tool call names.
func loadRecentTranscript(path string) (recentTranscript, error) {
	if path == "" {
		return recentTranscript{}, nil
	}

	for window := int64(initialTailBytes); ; window *= tailGrowthFactor {
		window = min(window, maxTailBytes)
		data, wholeFile, err := readTail(path, window)
		if err != nil {
			return recentTranscript{}, fmt.Errorf("read transcript %s: %w", path, err)
		}
		result := parseTranscript(data)
		if len(result.UserMessages) >= maxUserMessages || wholeFile || window >= maxTailBytes {
			return result, nil
		}
	}
}

// parseTranscript extracts the latest user messages and tool call
// names from complete JSONL lines.
func parseTranscript(data []byte) recentTranscript {
	var result recentTranscript
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		var entry transcriptEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.IsMeta || entry.IsSidechain {
			continue
		}

		// Dispatch on the line type, not message.role: other line types
		// (e.g. api-request-blob, a copy of an API request) also carry a
		// message with a role, and would duplicate or invent turns.
		text, blocks := parseContent(entry.Message.Content)
		switch entry.Type {
		case "user":
			if msg := userText(text, blocks); msg != "" {
				result.UserMessages = appendLatest(result.UserMessages, truncate(msg, maxUserMessageLen), maxUserMessages)
			}
		case "assistant":
			for _, b := range blocks {
				if b.Type == "tool_use" && b.Name != "" {
					result.RecentToolCalls = appendLatest(result.RecentToolCalls, b.Name, maxToolCalls)
				}
			}
		}
	}
	return result
}

// parseContent decodes message.content, which is either a plain string
// or an array of content blocks. Any other shape yields nothing, the
// same way an unparsable line is skipped: the transcript is best-effort
// context.
func parseContent(raw json.RawMessage) (string, []contentBlock) {
	if len(raw) == 0 {
		return "", nil
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", nil
		}
		return s, nil
	case '[':
		var blocks []contentBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return "", nil
		}
		return "", blocks
	}
	return "", nil
}

// userText returns what the user typed in one user line, or "" when
// the line carries nothing the user wrote (tool results only, an
// interrupt marker, or harness-injected text).
func userText(text string, blocks []contentBlock) string {
	if text == "" {
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		}
		text = strings.Join(parts, "\n")
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, interruptMarkerPrefix) || hasHarnessTag(text) {
		return ""
	}
	return text
}

// appendLatest appends v and keeps only the last n values.
func appendLatest(values []string, v string, n int) []string {
	values = append(values, v)
	if len(values) > n {
		values = values[len(values)-n:]
	}
	return values
}

// readTail returns the last n bytes of the file, minus the partial
// line it starts in, and whether that covers the whole file.
func readTail(path string, n int64) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}

	size := info.Size()
	offset := max(size-n, 0)
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, false, err
	}

	buf := make([]byte, size-offset)
	nr, err := io.ReadFull(f, buf)
	// The transcript can shrink between Stat and Read; keep whatever
	// was read.
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, false, err
	}
	data := buf[:nr]

	if offset > 0 {
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
			data = data[idx+1:]
		}
	}
	return data, offset == 0, nil
}

// truncate cuts s to maxLen runes so multi-byte text (e.g. Japanese)
// is never split in the middle of a character.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
