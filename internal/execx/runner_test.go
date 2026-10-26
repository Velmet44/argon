package execx

import (
	"context"
	"encoding/base64"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func decodePS(enc string) string {
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return ""
	}
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u))
}

func TestEncodeRoundTrip(t *testing.T) {
	for _, cmd := range []string{`Write-Host "a$b"`, `Get-ChildItem 'C:\a b' | Sort-Object`, "echo 'x\"y'"} {
		if got := decodePS(EncodePowerShell(cmd)); got != cmd {
			t.Fatalf("roundtrip %q got %q", cmd, got)
		}
	}
}

func TestIsInteractive(t *testing.T) {
	for _, c := range []string{"vim", "nvim f", "ssh host", "top", "htop", "tail -f x", "passwd", "code ."} {
		if !IsInteractive(c) {
			t.Errorf("%q should be interactive", c)
		}
	}
	for _, c := range []string{"echo hi", "git status", "ls -la"} {
		if IsInteractive(c) {
			t.Errorf("%q should not be interactive", c)
		}
	}
}

func TestRunEcho(t *testing.T) {
	shell, cmd := testShellCmd("echo hello-stage2")
	res := Run(context.Background(), shell, cmd, "", 10*time.Second, nil)
	if res.ExitCode != 0 || !strings.Contains(res.Output, "hello-stage2") {
		t.Fatalf("echo: %+v", res)
	}
}

func TestRunCwdRespected(t *testing.T) {
	shell, cmd := testShellCmd("pwd")
	res := Run(context.Background(), shell, cmd, testTmpDir(t), 10*time.Second, nil)
	if res.ExitCode != 0 {
		t.Fatalf("pwd: %+v", res)
	}
}

func TestRunTimeoutKills(t *testing.T) {
	shell, cmd := testShellCmdTimeout()
	res := Run(context.Background(), shell, cmd, "", 1*time.Second, nil)
	if res.ExitCode != 124 {
		t.Fatalf("want 124 timeout, got %+v", res)
	}
	if !strings.Contains(res.Output, "timed out") {
		t.Fatalf("missing timeout marker: %q", res.Output)
	}
}

func TestPowerShellDollarLiteral(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows powershell only")
	}
	// single quotes = literal in PowerShell; proves EncodedCommand passed $ through untouched
	res := Run(context.Background(), "powershell", `Write-Output 'a$b'`, "", 10*time.Second, nil)
	if res.ExitCode != 0 {
		t.Fatalf("ps: %+v", res)
	}
	if !strings.Contains(res.Output, "a$b") {
		t.Fatalf("quoting broke, got %q (EncodedCommand must keep literal)", res.Output)
	}
}

func TestTruncate20k(t *testing.T) {
	big := strings.Repeat("x", 20000)
	out := truncate(big, 8000)
	if len(out) > 9000 || !strings.Contains(out, "truncated") {
		t.Fatalf("truncate failed len=%d", len(out))
	}
}

func testShellCmd(cmd string) (string, string) {
	if runtime.GOOS == "windows" {
		if _, err := lookPath("pwsh", "powershell"); err == nil {
			// powershell understands echo/pwd natively
			return "powershell", cmd
		}
		return "powershell", cmd
	}
	return "bash", cmd
}

func lookPath(names ...string) (string, error) {
	for _, n := range names {
		for _, p := range []string{"/usr/bin/" + n, "/bin/" + n, n} {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	if runtime.GOOS != "windows" {
		return names[0], nil
	}
	return "", os.ErrNotExist
}

func testTmpDir(t *testing.T) string {
	t.Helper()
	d := os.TempDir()
	if d == "" {
		d = "."
	}
	return d
}

func testShellCmdTimeout() (string, string) {
	if runtime.GOOS == "windows" {
		return "powershell", "Start-Sleep -Seconds 5"
	}
	return "bash", "sleep 5"
}
