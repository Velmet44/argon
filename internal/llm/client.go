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
// no SDK. Falls back to non-stream on SSE error (caller decides retry).
func StreamChat(ctx context.Context, req TurnRequest, out chan<- Chunk) {
	defer close(out)
	body := map[string]any{
		"model":       req.Model,
		"messages":    buildMessages(req),
		"temperature": 0.2,
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
		out <- Chunk{Err: fmt.Errorf("llm http %d", resp.StatusCode)}
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
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil || len(ev.Choices) == 0 {
			continue
		}
		if t := ev.Choices[0].Delta.Content; t != "" {
			out <- Chunk{Text: t}
		}
	}
	if err := sc.Err(); err != nil {
		out <- Chunk{Err: err}
		return
	}
	out <- Chunk{Done: true}
}

// CompleteOnce is the --stream=false path: single JSON response.
func CompleteOnce(ctx context.Context, req TurnRequest) (string, error) {
	body := map[string]any{
		"model": req.Model, "messages": buildMessages(req), "temperature": 0.2, "stream": false,
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
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("bad llm response")
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

// ParseReply validates the accumulated stream text as AgentReply.
func ParseReply(s string) (AgentReply, error) {
	var r AgentReply
	s = strings.TrimSpace(s)
	// tolerate ```json fences from sloppy models
	s = strings.TrimPrefix(strings.TrimSuffix(s, "```"), "```json")
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(s)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return r, err
	}
	return r, nil
}
