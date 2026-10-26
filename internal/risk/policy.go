// Package risk implements deny-first classification on command+args.
// Order: DENY regex -> DESTRUCTIVE list -> WRITE (default) -> READ (explicit safe only).
// Name-only matching is forbidden; every rule sees the full lowercased command line.
// Users can extend/disable via [risk] extra_deny / extra_destructive / disabled in config.
package risk

import (
	"regexp"
	"strings"
)

type Tier string

const (
	TierRead       Tier = "read"
	TierWrite      Tier = "write"
	TierDestr      Tier = "destructive"
	TierDeny       Tier = "deny"
)

type Rule struct {
	ID     string
	Tier   Tier
	Re     *regexp.Regexp
	Reason string
}

func must(pat string) *regexp.Regexp { return regexp.MustCompile(`(?is)` + pat) }

// DefaultRules is the built-in list. IDs are stable; users may disable any
// via config [risk] disabled=["id",...]. Edit with care — changes weaken safety.
func DefaultRules() []Rule {
	return []Rule{
		// ---- DENY: privilege escalation / shell escapes ----
		{ID: "deny-sudo", Tier: TierDeny, Re: must(`\bsudo\b|\bdoas\b|\brunas\b`), Reason: "elevation is never auto-handled in v0.1"},
		{ID: "deny-pipe-shell", Tier: TierDeny, Re: must(`\|\s*(sh|bash|zsh|powershell|pwsh)(\.exe)?\b`), Reason: "piping into a shell is a classic takeover"},
		{ID: "deny-curl-pipe", Tier: TierDeny, Re: must(`(curl|wget|iwr|invoke-webrequest)[\s\S]*\|\s*(sh|bash|powershell|pwsh|iex)`), Reason: "curl|sh without explicit review"},
		{ID: "deny-iex", Tier: TierDeny, Re: must(`\biex\b|invoke-expression|invoke-command`), Reason: "dynamic code execution"},
		{ID: "deny-b64exec", Tier: TierDeny, Re: must(`frombase64string|certutil\s+-decode|base64\s+-d[\s\S]*\|\s*(sh|bash|powershell)`), Reason: "obfuscated payload execution"},
		{ID: "deny-defender", Tier: TierDeny, Re: must(`set-mppreference|add-mppreference|disable.*defender|stop-service\s+windefend`), Reason: "disables malware protection"},
		{ID: "deny-reg-hklm", Tier: TierDeny, Re: must(`reg\s+(delete|add)\s+HKLM|regedit\s+/s`), Reason: "system registry writes"},
		{ID: "deny-format", Tier: TierDeny, Re: must(`\bformat\b\s+[a-z]:|diskpart|mkfs(\.\w+)?\s|dd\s+.*of=/dev/`), Reason: "disk destructive"},
		{ID: "deny-forkbomb", Tier: TierDeny, Re: must(`:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}`), Reason: "fork bomb"},
		{ID: "deny-devdisk", Tier: TierDeny, Re: must(`>\s*/dev/(sd|hd|nvme|vd)[a-z]*`), Reason: "raw disk overwrite"},
		{ID: "deny-sys32", Tier: TierDeny, Re: must(`(system32|syswow64).*\\(del|remove-item|rd\b)|rd\s+/s\s+/q\s+c:\\windows|del\s+/f\s+/s\s+/q\s+c:\\windows`), Reason: "Windows system dir delete"},
		{ID: "deny-root-delete", Tier: TierDeny, Re: must(`rm\s+.*-rf?\s+/(?:\s|$)|rm\s+.*-rf?\s+/\*|rm\s+.*-rf?\s+~/?(?:\s|$)`), Reason: "recursive delete of / or ~"},
		{ID: "deny-chmod-root", Tier: TierDeny, Re: must(`chmod\s+-R\s+777\s+/|chown\s+-R\s+[^ ]+\s+/`), Reason: "recursive permission breakage"},
		{ID: "deny-key-exfil", Tier: TierDeny, Re: must(`(id_rsa|id_ed25519|\.pem)[\s\S]*(\|\s*(curl|wget|nc|ncat|ssh)|/dev/tcp/)`), Reason: "private key exfiltration"},
		{ID: "deny-nc-bind", Tier: TierDeny, Re: must(`\bnc\b.*-l|\bncat\b.*-l|/dev/tcp/.*/0`), Reason: "reverse/bind shell"},
		// ---- DESTRUCTIVE: double-confirm (type YES + --force) ----
		{ID: "destr-rm-rf", Tier: TierDestr, Re: must(`rm\s+-[a-z]*r[a-z]*f|remove-item\s+.*-recurse.*-force`), Reason: "recursive force delete"},
		{ID: "destr-git-hard", Tier: TierDestr, Re: must(`git\s+(reset\s+--hard|push\s+--force|push\s+-f|clean\s+-fd)`), Reason: "history-destroying git op"},
		{ID: "destr-pkg-remove", Tier: TierDestr, Re: must(`apt\s+(remove|purge)|pip\s+uninstall\s+-y|npm\s+uninstall\s+-g|docker\s+system\s+prune`), Reason: "package/data removal"},
		{ID: "destr-rm-config", Tier: TierDestr, Re: must(`rm\s+-r\s+~\/\.(config|local|ssh)`), Reason: "dot-config wipe"},
		// ---- READ: explicit safe only (args that write are re-checked in Classify) ----
		{ID: "read-ls", Tier: TierRead, Re: must(`^\s*(ls|dir|pwd|whoami|lsb_release|df|ps|echo|type|cat|find)\b`), Reason: "read-only probe"},
	}
}

// Classify returns the highest-severity matching tier. Unknown -> WRITE.
func Classify(cmd string, extraDeny, extraDestr []string, disabled map[string]bool) (Tier, string) {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	for _, r := range DefaultRules() {
		if disabled[r.ID] {
			continue
		}
		if r.Re.MatchString(lower) {
			// READ match with write-args is not really read (RE2 has no lookahead).
			if r.Tier == TierRead && (strings.Contains(lower, ">") || strings.Contains(lower, "-delete") || strings.Contains(lower, "-exec")) {
				return TierWrite, "default: read-like command with write args, confirm required"
			}
			return r.Tier, r.ID + ": " + r.Reason
		}
	}
	for _, pat := range extraDeny {
		re, err := regexp.Compile(`(?is)` + pat)
		if err != nil {
			continue
		}
		if re.MatchString(lower) {
			return TierDeny, "user-extra-deny: " + pat
		}
	}
	for _, pat := range extraDestr {
		re, err := regexp.Compile(`(?is)` + pat)
		if err != nil {
			continue
		}
		if re.MatchString(lower) {
			return TierDestr, "user-extra-destructive: " + pat
		}
	}
	return TierWrite, "default: unknown mutating command, confirm required"
}
