package xcontext

import (
	"os"
	"path/filepath"
	"strings"
)

// DetectShell returns powershell|pwsh|bash|zsh. cmd is never returned:
// cmd targets are redirected to powershell in v0.1.
func DetectShell() string {
	if v := os.Getenv("ARGON_SHELL"); v != "" {
		return strings.ToLower(v)
	}
	// PowerShell sets these; bash/zsh set SHELL.
	if os.Getenv("PSModulePath") != "" {
		if _, err := lookPath("pwsh"); err == nil {
			return "pwsh"
		}
		return "powershell"
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		b := strings.ToLower(filepath.Base(sh))
		if b == "zsh" {
			return "zsh"
		}
		return "bash"
	}
	return "powershell"
}

func lookPath(n string) (string, error) { return lookpath(n) }
