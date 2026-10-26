package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func mockSSE(t *testing.T, chunks []string, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 200 {
			w.WriteHeader(status)
			fmt.Fprint(w, body)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", c)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
}

func TestStreamAccumulates(t *testing.T) {
	srv := mockSSE(t, []string{`{"thought":"hi",`, `"command":"echo hi", "run":true, "done":false, "message":"x", "risk_self":"read"}`}, 200, "")
	defer srv.Close()
	ch := make(chan Chunk, 16)
	go StreamChat(context.Background(), TurnRequest{BaseURL: srv.URL, Model: "m", Timeout: 5 * time.Second}, ch)
	got := ""
	for ck := range ch {
		if ck.Err != nil {
			t.Fatalf("stream err: %v", ck.Err)
		}
		got += ck.Text
		if ck.Done {
			break
		}
	}
	r, err := ParseReply(got)
	if err != nil {
		t.Fatalf("parse %q: %v", got, err)
	}
	if r.Thought != "hi" || r.Command == nil || *r.Command != "echo hi" {
		t.Fatalf("bad reply %+v", r)
	}
}

func TestNon2xxSurfaced(t *testing.T) {
	srv := mockSSE(t, nil, 410, `{"detail":"model EOL"}`)
	defer srv.Close()
	ch := make(chan Chunk, 4)
	go StreamChat(context.Background(), TurnRequest{BaseURL: srv.URL, Model: "m"}, ch)
	for ck := range ch {
		if ck.Err == nil {
			continue
		}
		if he, ok := ck.Err.(*HTTPError); !ok || he.Status != 410 {
			t.Fatalf("want HTTPError 410, got %v", ck.Err)
		}
		return
	}
	t.Fatal("want error")
}

func TestCompleteOnceZeroChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[]}`)
	}))
	defer srv.Close()
	_, err := CompleteOnce(context.Background(), TurnRequest{BaseURL: srv.URL, Model: "m", Timeout: 5 * time.Second})
	if err == nil {
		t.Fatal("want error")
	}
}

func TestParseToleratesFences(t *testing.T) {
	r, err := ParseReply("```json\n{\"thought\":\"t\",\"command\":null,\"run\":false,\"done\":true,\"message\":\"done\",\"risk_self\":\"unknown\"}\n```")
	if err != nil || !r.Done {
		t.Fatalf("fence parse: %+v %v", r, err)
	}
}
