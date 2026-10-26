package tui

import (
	"strings"
	"testing"
	"time"
)

func TestCommandLinesWrap(t *testing.T) {
	long := "Get-PSDrive -PSProvider FileSystem | Select-Object Name, @{Name='FreeGB';Expression={[math]::Round($_.Free/1GB,2)}} | Sort-Object Free -Descending"
	lines := CommandLines(false, long, "read", 60)
	if len(lines) < 2 {
		t.Fatalf("want wrapped lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "$ Get-PSDrive") {
		t.Errorf("first line must start with $: %q", lines[0])
	}
	found := false
	for _, l := range lines[1:] {
		if strings.Contains(l, "↳") {
			found = true
		}
	}
	if !found {
		t.Errorf("continuation lines must carry ↳: %q", lines)
	}
	if !strings.Contains(lines[len(lines)-1], "[read]") {
		t.Errorf("tier rides last line: %q", lines[len(lines)-1])
	}
}

func TestThoughtBoxLines(t *testing.T) {
	lines := ThoughtBoxLines(false, 2, 1200*time.Millisecond, "Use Get-PSDrive to list drives.", 60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "thought 2") || !strings.Contains(joined, "1.2s") {
		t.Errorf("title must carry number+elapsed: %q", joined)
	}
	if !strings.Contains(joined, "Get-PSDrive") {
		t.Errorf("body missing: %q", joined)
	}
	if !strings.Contains(lines[0], "╭") || !strings.Contains(lines[len(lines)-1], "╰") {
		t.Errorf("must be boxed: %q", lines)
	}
}

func TestErrBoxLines(t *testing.T) {
	lines := ErrBoxLines(false, "blocked", []string{"reason here"}, 60)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "✖ blocked") {
		t.Errorf("title: %q", lines)
	}
}

func TestClampWidth(t *testing.T) {
	if clampWidth(10) != 40 || clampWidth(500) != 100 || clampWidth(70) != 70 {
		t.Fatal("clamp broken")
	}
}

func stripANSI(s string) string {
	for {
		i := strings.IndexByte(s, 0x1b)
		if i < 0 {
			return s
		}
		j := strings.IndexByte(s[i:], 'm')
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + s[i+j+1:]
	}
}
