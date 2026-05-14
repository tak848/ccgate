// Package codexoauth implements llm.Provider by driving the Codex
// app-server over stdio. The app-server owns ChatGPT subscription
// authentication and token refresh; ccgate only supplies a
// classification prompt and parses the structured final answer.
package codexoauth

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tak848/ccgate/internal/llm"
)

const (
	defaultCodexBin           = "codex"
	serviceName               = "ccgate"
	threadSandboxModeReadOnly = "read-only"
	turnSandboxPolicyReadOnly = "readOnly"
	maxLineBytes              = 16 * 1024 * 1024
)

var (
	// ErrAuthUnavailable means the Codex app-server could not find a
	// ChatGPT-authenticated account in CODEX_HOME, or the cached
	// ChatGPT token was rejected. runner.decide treats it as a
	// credential_unavailable fallthrough rather than an exit-1 error.
	ErrAuthUnavailable = errors.New("codex-oauth: ChatGPT Codex auth unavailable")
	// ErrWrongAuthMode means the Codex app-server is logged in with
	// an API key, which would defeat provider.name="codex-oauth"'s
	// contract of using ChatGPT subscription auth.
	ErrWrongAuthMode = errors.New("codex-oauth: Codex account is not ChatGPT auth")
)

// Client talks to `codex app-server` over stdio.
type Client struct {
	CodexBin        string
	CodexHome       string
	EnsureCodexHome bool
}

// Decide sends a single classification request through Codex and
// parses the app-server's structured final response.
func (c *Client) Decide(ctx context.Context, p llm.Prompt) (llm.Result, error) {
	if p.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(p.TimeoutMS)*time.Millisecond)
		defer cancel()
	}

	if c.EnsureCodexHome {
		if err := ensureManagedCodexHome(c.CodexHome); err != nil {
			return llm.Result{}, fmt.Errorf("prepare codex home: %w", err)
		}
	}

	s, err := startAppServer(ctx, c.CodexBin, c.CodexHome)
	if err != nil {
		return llm.Result{}, err
	}
	defer s.Close()

	if err := s.initialize(); err != nil {
		return llm.Result{}, fmt.Errorf("initialize app-server: %w", err)
	}
	if err := s.requireChatGPTAccount(); err != nil {
		return llm.Result{}, err
	}

	threadID, err := s.startThread(p)
	if err != nil {
		return llm.Result{}, fmt.Errorf("start thread: %w", err)
	}
	defer s.archiveThread(threadID)

	turnID, err := s.startTurn(threadID, p)
	if err != nil {
		return llm.Result{}, fmt.Errorf("start turn: %w", err)
	}
	text, err := s.waitTurn(threadID, turnID)
	if err != nil {
		return llm.Result{}, err
	}

	out, err := parseOutput(text)
	if err != nil {
		return llm.Result{}, err
	}
	if out.Behavior == llm.BehaviorDeny && strings.TrimSpace(out.DenyMessage) == "" {
		out.DenyMessage = llm.DefaultDenyMessage
	}
	return llm.Result{Output: out}, nil
}

func ensureManagedCodexHome(home string) error {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", home, err)
	}
	configPath := filepath.Join(home, "config.toml")
	if _, err := os.Stat(configPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", configPath, err)
	}
	// Keep the managed ccgate CODEX_HOME self-contained. The file
	// contains no secret, but mode 0600 matches the adjacent auth.json
	// sensitivity and avoids leaking user configuration details.
	if err := os.WriteFile(configPath, []byte("cli_auth_credentials_store = \"file\"\n"), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", configPath, err)
	}
	return nil
}

type appServer struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	stderr *bytes.Buffer
	nextID int
	done   chan error
}

func startAppServer(ctx context.Context, codexBin, codexHome string) (*appServer, error) {
	bin := strings.TrimSpace(codexBin)
	if bin == "" {
		bin = defaultCodexBin
	}
	cmd := exec.CommandContext(ctx, bin, "app-server")
	cmd.Env = codexAppServerEnv(os.Environ(), codexHome)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open app-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open app-server stdout: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s app-server: %w", bin, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	return &appServer{
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReaderSize(stdout, 64*1024),
		stderr: &stderr,
		nextID: 1,
		done:   done,
	}, nil
}

func codexAppServerEnv(base []string, codexHome string) []string {
	out := make([]string, 0, len(base)+1)
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "CODEX_API_KEY", "OPENAI_API_KEY", "CCGATE_OPENAI_API_KEY", "CODEX_HOME":
			continue
		default:
			out = append(out, kv)
		}
	}
	if strings.TrimSpace(codexHome) != "" {
		out = append(out, "CODEX_HOME="+codexHome)
	}
	return out
}

func (s *appServer) Close() {
	_ = s.stdin.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	select {
	case err := <-s.done:
		if err != nil {
			slog.Debug("codex app-server exited", "error", err)
		}
	case <-time.After(2 * time.Second):
		slog.Debug("codex app-server did not exit promptly")
	}
}

func (s *appServer) initialize() error {
	if _, err := s.call("initialize", map[string]any{
		"clientInfo": map[string]any{
			"name":    serviceName,
			"title":   "ccgate",
			"version": "0.1.0",
		},
	}); err != nil {
		return err
	}
	return s.notify("initialized", map[string]any{})
}

func (s *appServer) requireChatGPTAccount() error {
	raw, err := s.call("account/read", map[string]any{"refreshToken": true})
	if err != nil {
		return fmt.Errorf("read account: %w", err)
	}
	return validateChatGPTAccount(raw)
}

func validateChatGPTAccount(raw json.RawMessage) error {
	var res struct {
		Account *struct {
			Type     string `json:"type"`
			PlanType string `json:"planType"`
			Email    string `json:"email"`
		} `json:"account"`
		RequiresOpenAIAuth bool `json:"requiresOpenaiAuth"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("parse account/read result: %w", err)
	}
	if res.Account == nil {
		return fmt.Errorf("%w: run `CODEX_HOME=<provider.codex_home> codex login` first", ErrAuthUnavailable)
	}
	switch res.Account.Type {
	case "chatgpt", "chatgptAuthTokens":
		return nil
	default:
		return fmt.Errorf("%w: got account type %q", ErrWrongAuthMode, res.Account.Type)
	}
}

func (s *appServer) startThread(p llm.Prompt) (string, error) {
	raw, err := s.call("thread/start", threadStartParams(p))
	if err != nil {
		return "", err
	}
	var res struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("parse thread/start result: %w", err)
	}
	if res.Thread.ID == "" {
		return "", errors.New("thread/start result missing thread.id")
	}
	return res.Thread.ID, nil
}

func threadStartParams(p llm.Prompt) map[string]any {
	return map[string]any{
		"model":          p.Model,
		"cwd":            promptCwd(p),
		"approvalPolicy": "never",
		"sandbox":        threadSandboxModeReadOnly,
		"serviceName":    serviceName,
	}
}

func (s *appServer) archiveThread(threadID string) {
	if threadID == "" {
		return
	}
	if _, err := s.call("thread/archive", map[string]any{"threadId": threadID}); err != nil {
		slog.Debug("codex app-server thread/archive failed", "error", err)
	}
}

func (s *appServer) startTurn(threadID string, p llm.Prompt) (string, error) {
	raw, err := s.call("turn/start", turnStartParams(threadID, p))
	if err != nil {
		return "", err
	}
	var res struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("parse turn/start result: %w", err)
	}
	if res.Turn.ID == "" {
		return "", errors.New("turn/start result missing turn.id")
	}
	return res.Turn.ID, nil
}

func turnStartParams(threadID string, p llm.Prompt) map[string]any {
	return map[string]any{
		"threadId":       threadID,
		"input":          []map[string]string{{"type": "text", "text": classificationPrompt(p)}},
		"cwd":            promptCwd(p),
		"approvalPolicy": "never",
		"sandboxPolicy": map[string]any{
			"type":          turnSandboxPolicyReadOnly,
			"networkAccess": false,
		},
		"model":        p.Model,
		"outputSchema": outputSchema(),
	}
}

func classificationPrompt(p llm.Prompt) string {
	return strings.Join([]string{
		"You are running inside ccgate's codex-oauth provider.",
		"Classify the PermissionRequest only from the supplied policy and payload.",
		"Do not inspect files, run commands, use tools, browse, edit files, or ask follow-up questions.",
		"Return only the structured JSON requested by outputSchema.",
		"",
		"System policy:",
		p.System,
		"",
		"PermissionRequest payload:",
		p.User,
	}, "\n")
}

func outputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"behavior": map[string]any{
				"type": "string",
				"enum": []string{llm.BehaviorAllow, llm.BehaviorDeny, llm.BehaviorFallthrough},
			},
			"reason": map[string]any{
				"type": "string",
			},
			"deny_message": map[string]any{
				"type": "string",
			},
		},
		"required":             []string{"behavior", "reason", "deny_message"},
		"additionalProperties": false,
	}
}

func promptCwd(p llm.Prompt) string {
	var payload struct {
		Context struct {
			Cwd string `json:"cwd"`
		} `json:"context"`
	}
	if err := json.Unmarshal([]byte(p.User), &payload); err == nil && payload.Context.Cwd != "" {
		return payload.Context.Cwd
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func (s *appServer) waitTurn(threadID, turnID string) (string, error) {
	var deltas strings.Builder
	var finalText string
	for {
		msg, err := s.read()
		if err != nil {
			return "", err
		}
		if len(msg.ID) > 0 && msg.Method != "" {
			_ = s.handleServerRequest(msg)
			continue
		}
		switch msg.Method {
		case "item/agentMessage/delta":
			if delta := agentDeltaText(msg.Params); delta != "" {
				deltas.WriteString(delta)
			}
		case "item/completed":
			if text := completedAgentMessageText(msg.Params); text != "" {
				finalText = text
			}
		case "turn/completed":
			status, text, err := completedTurn(msg.Params, threadID, turnID)
			if status == "" && text == "" && err == nil {
				continue
			}
			if text != "" {
				finalText = text
			}
			if err != nil {
				return "", err
			}
			if status == "completed" {
				if strings.TrimSpace(finalText) != "" {
					return finalText, nil
				}
				if strings.TrimSpace(deltas.String()) != "" {
					return deltas.String(), nil
				}
				return "", errors.New("codex-oauth: turn completed without agent message")
			}
			return "", fmt.Errorf("codex-oauth: turn ended with status %q", status)
		}
	}
}

func agentDeltaText(raw json.RawMessage) string {
	var p struct {
		Delta string `json:"delta"`
		Text  string `json:"text"`
	}
	_ = json.Unmarshal(raw, &p)
	if p.Delta != "" {
		return p.Delta
	}
	return p.Text
}

func completedAgentMessageText(raw json.RawMessage) string {
	var p struct {
		Item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return ""
	}
	if p.Item.Type == "agentMessage" {
		return p.Item.Text
	}
	return ""
}

func completedTurn(raw json.RawMessage, threadID, turnID string) (string, string, error) {
	var p struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message        string `json:"message"`
				CodexErrorInfo *struct {
					Type           string `json:"type"`
					HTTPStatusCode int    `json:"httpStatusCode"`
				} `json:"codexErrorInfo"`
			} `json:"error"`
			Items []json.RawMessage `json:"items"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", "", fmt.Errorf("parse turn/completed: %w", err)
	}
	if p.ThreadID != "" && p.ThreadID != threadID {
		return "", "", nil
	}
	if p.Turn.ID != "" && p.Turn.ID != turnID {
		return "", "", nil
	}
	if p.Turn.Status == "failed" && p.Turn.Error != nil {
		if p.Turn.Error.CodexErrorInfo != nil {
			if p.Turn.Error.CodexErrorInfo.Type == "Unauthorized" || p.Turn.Error.CodexErrorInfo.HTTPStatusCode == 401 {
				return "", "", fmt.Errorf("%w: %s", ErrAuthUnavailable, p.Turn.Error.Message)
			}
		}
		return "", "", fmt.Errorf("codex-oauth: turn failed: %s", p.Turn.Error.Message)
	}
	for _, rawItem := range p.Turn.Items {
		var item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(rawItem, &item); err == nil && item.Type == "agentMessage" && item.Text != "" {
			return p.Turn.Status, item.Text, nil
		}
	}
	return p.Turn.Status, "", nil
}

func (s *appServer) handleServerRequest(msg rpcMessage) error {
	var result any
	switch msg.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		result = "decline"
	case "item/tool/requestUserInput":
		result = map[string]any{"decision": "cancel"}
	default:
		result = map[string]any{"error": "unsupported request"}
	}
	return s.respond(msg.ID, result)
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *appServer) call(method string, params any) (json.RawMessage, error) {
	id := s.nextID
	s.nextID++
	if err := s.write(map[string]any{"method": method, "id": id, "params": params}); err != nil {
		return nil, err
	}
	wantID := strconv.Itoa(id)
	for {
		msg, err := s.read()
		if err != nil {
			return nil, err
		}
		if len(msg.ID) > 0 && msg.Method != "" {
			_ = s.handleServerRequest(msg)
			continue
		}
		if string(msg.ID) != wantID {
			continue
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("app-server %s: %s", method, msg.Error.Message)
		}
		return msg.Result, nil
	}
}

func (s *appServer) notify(method string, params any) error {
	return s.write(map[string]any{"method": method, "params": params})
}

func (s *appServer) respond(id json.RawMessage, result any) error {
	return s.write(map[string]any{"id": json.RawMessage(id), "result": result})
}

func (s *appServer) write(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal app-server message: %w", err)
	}
	data = append(data, '\n')
	if _, err := s.stdin.Write(data); err != nil {
		return fmt.Errorf("write app-server message: %w", err)
	}
	return nil
}

func (s *appServer) read() (rpcMessage, error) {
	line, err := s.reader.ReadBytes('\n')
	if err != nil {
		select {
		case waitErr := <-s.done:
			if waitErr != nil {
				return rpcMessage{}, fmt.Errorf("codex app-server exited: %w; stderr: %s", waitErr, truncate(s.stderr.String()))
			}
		default:
		}
		return rpcMessage{}, fmt.Errorf("read app-server message: %w; stderr: %s", err, truncate(s.stderr.String()))
	}
	if len(line) > maxLineBytes {
		return rpcMessage{}, fmt.Errorf("app-server message exceeded %d bytes", maxLineBytes)
	}
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return s.read()
	}
	var msg rpcMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return rpcMessage{}, fmt.Errorf("parse app-server message: %w", err)
	}
	return msg, nil
}

func parseOutput(text string) (llm.Output, error) {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return llm.Output{}, errors.New("codex-oauth: empty final response")
	}
	var out llm.Output
	if err := json.Unmarshal([]byte(clean), &out); err == nil {
		return out, nil
	}
	obj, ok := firstJSONObject(clean)
	if !ok {
		return llm.Output{}, fmt.Errorf("parse Codex response: no JSON object in %q", truncate(clean))
	}
	if err := json.Unmarshal([]byte(obj), &out); err != nil {
		return llm.Output{}, fmt.Errorf("parse Codex response JSON: %w", err)
	}
	return out, nil
}

func firstJSONObject(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

func truncate(s string) string {
	const max = 500
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
