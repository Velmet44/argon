package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// TurnRequest is one agent turn.
type TurnRequest struct {
	BaseURL string
	APIKey  string
	Model   string
	Extra   map[string]string // e.g. OpenRouter HTTP-Referer headers
	System  string
	History []Msg
	User    string
	Stream  bool
	Timeout time.Duration
}

type Msg struct {
	Role, Content string
}

// AgentReply mirrors SPEC Sec 6.
type AgentReply struct {
	Thought  string  `json:"thought"`
	Command  *string `json:"command"`
	Run      bool    `json:"run"`
	Done     bool    `json:"done"`
	Message  string  `json:"message"`
	RiskSelf string  `json:"risk_self"`
}

type Chunk struct {
	Text string
	Done bool
	Err  error
}

// StreamChat POSTs chat/completions with stream=true, parses SSE
// data: {...} lines, extracts choices[0].delta.content. Plain net/http,
// no SDK. Non-2xx returns detailed status+body for --verbose debugging.
func StreamChat(ctx context.Context, req TurnRequest, out chan<- Chunk) {
	defer close(out)
	body := map[string]any{
		"model":       req.Model,
		"messages":    buildMessages(req),
		"temperature": 0.2,
		"max_tokens":  1024,
		"stream":      true,
	}
	raw, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(req.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		out <- Chunk{Err: err}
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	for k, v := range req.Extra {
		httpReq.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 0}).Do(httpReq)
	if err != nil {
		out <- Chunk{Err: err}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		out <- Chunk{Err: &HTTPError{Status: resp.StatusCode, Body: readSnippet(resp, 2048)}}
		return
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			out <- Chunk{Done: true}
			return
		}
		var ev struct {
			Choices []struct {
				Delta struct {
					Content *string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil || len(ev.Choices) == 0 {
			// provider error event, e.g. {"error":{...}} — surface it
			if strings.Contains(data, `"error"`) {
				out <- Chunk{Err: fmt.Errorf("llm stream event: %.300s", data)}
				return
			}
			continue
		}
		if t := ev.Choices[0].Delta.Content; t != nil && *t != "" {
			out <- Chunk{Text: *t}
		}
	}
	if err := sc.Err(); err != nil {
		out <- Chunk{Err: err}
		return
	}
	out <- Chunk{Done: true}
}

// HTTPError carries status + body snippet for --verbose debugging
// (critical for NVIDIA/Groq/Cerebras compat where model names drift).
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("llm http %d: %.500s", e.Status, e.Body)
}

// CompleteOnce is the --stream=false path: single JSON response.
func CompleteOnce(ctx context.Context, req TurnRequest) (string, error) {
	body := map[string]any{
		"model": req.Model, "messages": buildMessages(req), "temperature": 0.2, "max_tokens": 1024, "stream": false,
	}
	raw, _ := json.Marshal(body)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(req.BaseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	for k, v := range req.Extra {
		httpReq.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: req.Timeout}).Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", &HTTPError{Status: resp.StatusCode, Body: readSnippet(resp, 2048)}
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("bad llm decode: %v", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("bad llm response: zero choices (model=%q — check provider model id)", req.Model)
	}
	return out.Choices[0].Message.Content, nil
}

func buildMessages(r TurnRequest) []any {
	msgs := []any{map[string]string{"role": "system", "content": r.System}}
	for _, h := range r.History {
		msgs = append(msgs, map[string]string{"role": h.Role, "content": h.Content})
	}
	return append(msgs, map[string]string{"role": "user", "content": r.User})
}

func readSnippet(resp *http.Response, n int) string {
	buf := make([]byte, n)
	m, _ := resp.Body.Read(buf)
	return strings.TrimSpace(string(buf[:m]))
}

// ParseReply validates the accumulated stream text as AgentReply.
// Tolerates ```json fences and leading/trailing prose: extracts the first
// BALANCED {...} object (brace-aware, string-aware) so nested braces in
// commands (e.g. PowerShell @{...}) don't break extraction.
func ParseReply(s string) (AgentReply, error) {
	var r AgentReply
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	if obj, ok := extractBalanced(s); ok {
		s = obj
	}
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return r, err
	}
	if r.Command == nil && !r.Done && strings.TrimSpace(r.Message) == "" && strings.TrimSpace(r.Thought) == "" {
		return r, fmt.Errorf("empty reply")
	}
	return r, nil
}

// extractBalanced returns the first {...} with matched braces, skipping
// over "..." strings (with \ escapes). Reports false when unbalanced.
func extractBalanced(s string) (string, bool) {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return "", false
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
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
