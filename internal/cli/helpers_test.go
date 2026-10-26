package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Velmet44/argon/internal/risk"
)

func TestResolveCD(t *testing.T) {
	base := filepath.Clean("/a/b")
	home := base // ~ may differ; only check suffix handling below
	_ = home
	cases := []struct{ cmd, want string }{
		{"cd foo", filepath.Clean("/a/b/foo")},
		{"cd ..", filepath.Clean("/a")},
		{"cd", base}, // bare cd tested loosely (home or base)
		{`Set-Location "sub dir"`, filepath.Clean("/a/b/sub dir")},
		{"pushd ../x", filepath.Clean("/a/x")},
		{"popd", base},
		{`cd 'quoted'`, filepath.Clean("/a/b/quoted")},
	}
	for _, c := range cases {
		got := resolveCD(base, c.cmd)
		if c.cmd == "cd" {
			continue // home-dependent
		}
		if got != c.want {
			t.Errorf("%q: got %q want %q", c.cmd, got, c.want)
		}
	}
	if got := resolveCD(base, "cd /"); !isAbsCross(got) {
		t.Errorf("abs: %q not abs", got)
	}
	// quoted multi-word still single target
	if got := resolveCD(base, `Set-Location "a b" c`); got != filepath.Join(base, "a b") && got != filepath.Clean(filepath.Join(base, "a b")) {
		t.Errorf("quoted precedence: %q", got)
	}
}

func TestRedactOutputBeforeLLM(t *testing.T) {
	out := "ok\napi_key=SECRET123\ntoken: abc\n"
	red := risk.Redact(out)
	if strings.Contains(red, "SECRET123") || strings.Contains(red, "abc") {
		t.Fatalf("not redacted: %q", red)
	}
}
