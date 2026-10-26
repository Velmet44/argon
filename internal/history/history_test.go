package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isoEnv(t *testing.T) {
	t.Helper()
	d := t.TempDir()
	t.Setenv("ARGON_HOME", d)
}

func TestAppendReadRoundTrip(t *testing.T) {
	isoEnv(t)
	if err := Append("oneshot", "list big files", []string{"Get-ChildItem"}, []string{"a.txt\nb.txt"}, 0, 800, 200); err != nil {
		t.Fatal(err)
	}
	if err := Append("oneshot", "second", []string{"echo hi"}, []string{"hi"}, 0, 800, 200); err != nil {
		t.Fatal(err)
	}
	ents, err := LoadRecent(10)
	if err != nil || len(ents) != 2 {
		t.Fatalf("got %d, err %v", len(ents), err)
	}
	if ents[0].Prompt != "list big files" || len(ents[0].Outputs) != 1 {
		t.Fatalf("order/content: %+v", ents[0])
	}
	if ents[0].Mode != "oneshot" || ents[0].Exit != 0 {
		t.Fatalf("meta: %+v", ents[0])
	}
}

func TestRedactAndTruncate(t *testing.T) {
	isoEnv(t)
	big := "ok\napi_key=SECRET1234567890\n" + strings.Repeat("x", 5000)
	if err := Append("repl", "q", []string{"c"}, []string{big}, 0, 100, 200); err != nil {
		t.Fatal(err)
	}
	ents, err := LoadRecent(5)
	if err != nil || len(ents) != 1 {
		t.Fatalf("got %d, err %v", len(ents), err)
	}
	got := ents[0].Outputs[0]
	if strings.Contains(got, "SECRET1234567890") {
		t.Fatal("secret leaked")
	}
	if len(got) > 100 {
		t.Fatalf("not truncated: %d", len(got))
	}
}

func TestPrune(t *testing.T) {
	isoEnv(t)
	for i := 0; i < 5; i++ {
		if err := Append("oneshot", "p", []string{"c"}, []string{"o"}, 0, 800, 3); err != nil {
			t.Fatal(err)
		}
	}
	ents, err := LoadRecent(10)
	if err != nil || len(ents) != 3 {
		t.Fatalf("want 3 after prune, got %d (%v)", len(ents), err)
	}
	raw, _ := os.ReadFile(filepath.Join(os.Getenv("ARGON_HOME"), "history.jsonl"))
	if n := len(strings.Split(strings.TrimSpace(string(raw)), "\n")); n != 3 {
		t.Fatalf("file has %d lines", n)
	}
}

func TestLegacyAndCorruptTolerated(t *testing.T) {
	isoEnv(t)
	p := filepath.Join(os.Getenv("ARGON_HOME"), "history.jsonl")
	os.MkdirAll(filepath.Dir(p), 0o700)
	legacy := `{"ts":"2026-01-01T00:00:00Z","mode":"oneshot","prompt":"old","commands":["ls"],"exit":0}` + "\n"
	os.WriteFile(p, []byte(legacy+"this is not json\n"), 0o600)
	ents, err := LoadRecent(10)
	if err != nil || len(ents) != 1 || ents[0].Prompt != "old" {
		t.Fatalf("got %v %v", ents, err)
	}
}

func TestRecentTasks(t *testing.T) {
	isoEnv(t)
	_ = Append("oneshot", "task one", []string{"a", "b"}, []string{"out1", "out2"}, 0, 800, 200)
	_ = Append("oneshot", "task two", []string{"c"}, []string{"out3"}, 1, 800, 200)
	ts, err := RecentTasks(5, 50)
	if err != nil || len(ts) != 2 {
		t.Fatalf("got %v %v", ts, err)
	}
	if ts[0].Prompt != "task one" || ts[0].Cmd != "a ; b" {
		t.Fatalf("combined: %+v", ts[0])
	}
	ts1, _ := RecentTasks(1, 800)
	if len(ts1) != 1 || ts1[0].Prompt != "task two" {
		t.Fatalf("limit: %+v", ts1)
	}
	if _, err := RecentTasks(0, 0); err != nil {
		t.Fatal(err)
	}
}

func TestClear(t *testing.T) {
	isoEnv(t)
	_ = Append("oneshot", "p", nil, nil, 0, 800, 200)
	ok, err := Clear()
	if err != nil || !ok {
		t.Fatalf("clear %v %v", ok, err)
	}
	ok, _ = Clear()
	if ok {
		t.Fatal("second clear should report nothing")
	}
}
