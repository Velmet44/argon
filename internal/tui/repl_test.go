package tui

import (
	"path/filepath"
	"testing"
)

func TestTUIResolveCD(t *testing.T) {
	base := filepath.Clean("/a/b")
	if got := tuiResolveCD(base, "cd foo"); got != filepath.Clean("/a/b/foo") {
		t.Errorf("cd foo: %q", got)
	}
	if got := tuiResolveCD(base, `Set-Location "sub dir"`); got != filepath.Clean("/a/b/sub dir") {
		t.Errorf("quoted: %q", got)
	}
	if got := tuiResolveCD(base, "popd"); got != base {
		t.Errorf("popd: %q", got)
	}
	if got := tuiResolveCD(base, "cd /"); !isAbsTUI(got) {
		t.Errorf("abs: %q", got)
	}
}

func isAbsTUI(p string) bool {
	return filepath.IsAbs(p) || (len(p) > 0 && (p[0] == '/' || p[0] == '\\'))
}

func TestDisabledSet(t *testing.T) {
	m := disabledSet([]string{"read-ls"})
	if !m["read-ls"] || m["other"] {
		t.Fatalf("bad set: %v", m)
	}
}
