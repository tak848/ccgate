package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// RecentTranscript holds recent user messages and tool operations from the session transcript.
type RecentTranscript struct {
	UserMessages    []string `json:"user_messages,omitempty"`
	RecentToolCalls []string `json:"recent_tool_calls,omitempty"`
}

const (
	maxUserMessages = 3
	maxToolCalls    = 5
	tailBytes       = 64 * 1024
)

// LoadRecentTranscript reads the tail of the transcript JSONL and extracts
// the most recent user messages and tool call summaries.
func LoadRecentTranscript(path string) (RecentTranscript, error) {
	if path == "" {
		return RecentTranscript{}, nil
	}

	data, err := readTail(path, tailBytes)
	if err != nil {
		return RecentTranscript{}, fmt.Errorf("read transcript %s: %w", path, err)
	}

	var result RecentTranscript
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	for scanner.Scan() {
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"message"`
			ToolName string `json:"tool_name"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}

		switch {
		case entry.Type == "user" || entry.Message.Role == "user":
			if s, ok := entry.Message.Content.(string); ok && s != "" {
				result.UserMessages = append(result.UserMessages, truncate(s, 200))
				if len(result.UserMessages) > maxUserMessages {
					result.UserMessages = result.UserMessages[1:]
				}
			}
		case entry.ToolName != "":
			result.RecentToolCalls = append(result.RecentToolCalls, entry.ToolName)
			if len(result.RecentToolCalls) > maxToolCalls {
				result.RecentToolCalls = result.RecentToolCalls[1:]
			}
		}
	}
	return result, nil
}

func readTail(path string, n int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	size := info.Size()
	offset := size - n
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, 0); err != nil {
		return nil, err
	}

	buf := make([]byte, size-offset)
	nr, err := f.Read(buf)
	if err != nil {
		return nil, err
	}
	data := buf[:nr]

	if offset > 0 {
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
			data = data[idx+1:]
		}
	}
	return data, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
