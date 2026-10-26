// Package update detects install source so `argon upgrade` never fights
// winget/apt/scoop (SPEC 2.4).
package update

import (
	"os"
	"path/filepath"
	"strings"
)

// InstallSource reports curl|winget|apt|scoop|deb|go-install|unknown.
func InstallSource() string {
	exe, _ := os.Executable()
	exe = strings.ToLower(exe)
	switch {
	case strings.Contains(exe, "go-build") || strings.Contains(exe, "go_run"):
		return "go-install"
	case strings.Contains(exe, "winget") || strings.Contains(exe, "windowsapps"):
		return "winget"
	case strings.Contains(exe, string(filepath.Separator)+"scoop"+string(filepath.Separator)):
		return "scoop"
	case strings.HasPrefix(exe, "/usr/bin/") || strings.HasPrefix(exe, "/usr/local/bin/"):
		return "apt"
	case strings.Contains(exe, ".argon"):
		return "curl"
	}
	return "unknown"
}
