package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDigestOutput(t *testing.T) {
	d := DigestOutput("a\nb\nc\n")
	if !strings.Contains(d, "3 lines") || !strings.Contains(d, `"a"`) {
		t.Fatalf("digest: %q", d)
	}
	if got := DigestOutput(""); !strings.Contains(got, "0 lines") {
		t.Fatalf("empty digest: %q", got)
	}
}

func TestTruncateOutput(t *testing.T) {
	big := strings.Repeat("x", 5000)
	out, trunc := TruncateOutput(big)
	if !trunc || len(out) >= 5000 || !strings.Contains(out, "omitted") {
		t.Fatal("should truncate with marker")
	}
	if _, trunc := TruncateOutput("small"); trunc {
		t.Fatal("small must not truncate")
	}
	out, _ = TruncateOutput("a OUTPUT>>> b")
	if strings.Contains(out, "OUTPUT>>>") {
		t.Fatal("delimiter must be neutralized")
	}
}

func TestBuildUserMessageShape(t *testing.T) {
	m := BuildUserMessage("powershell", "win", `C:\x`, "a.txt", "Do the thing", []string{"h1"},
		[]Step{{Cmd: "Get-X", Exit: 0, Digest: "2 lines; first: \"a\"", Note: "Did X"}}, 2, 8, "out", 0, false)
	for _, want := range []string{"CONTEXT:", "TASK: Do the thing", "STEPS", "Get-X", "note: Did X", "LATEST OUTPUT", "<<<OUTPUT", "OUTPUT>>>", "NEXT:", "Original task again", "2/8"} {
		if !strings.Contains(m, want) {
			t.Errorf("missing %q in:\n%s", want, m)
		}
	}
	// task repeated verbatim top and bottom
	if strings.Count(m, "Do the thing") < 2 {
		t.Error("task must appear at least twice")
	}
	m1 := BuildUserMessage("bash", "linux", "/h", "", "t", nil, nil, 1, 8, "", 0, false)
	if !strings.Contains(m1, "none yet") || !strings.Contains(m1, "none (first step)") {
		t.Errorf("turn-1 shape: %s", m1)
	}
	m2 := BuildUserMessage("bash", "linux", "/h", "", "t", nil,
		[]Step{{Cmd: "rm x", Exit: 0, Digest: "d", Note: ""}}, 2, 8, "out", 0, true)
	if !strings.Contains(m2, "verify it with a read-only command now") {
		t.Errorf("mustVerify order missing: %s", m2)
	}
}

func TestExtractBalanced(t *testing.T) {
	in := `Sure {"thought":"hi","command":"Get-X @{n='a';e={$_.b}}","run":true,"done":false,"message":"m","risk_self":"read"} trailing`
	got, ok := extractBalanced(in)
	if !ok {
		t.Fatal("want balanced")
	}
	var r AgentReply
	if err := json.Unmarshal([]byte(got), &r); err != nil {
		t.Fatalf("inner parse: %v", err)
	}
	if r.Command == nil || !strings.Contains(*r.Command, "@{") {
		t.Fatalf("nested braces lost: %+v", r)
	}
	if _, ok := extractBalanced(`{"a":1`); ok {
		t.Fatal("unbalanced must fail")
	}
	if _, ok := extractBalanced(`no braces`); ok {
		t.Fatal("missing must fail")
	}
}
