# ARGON (Argon Prompt) — Basic Spec v0.1 (draft, buildable, not final)

> Wrapper CLI + REPL. No GUI. Go. Windows + Linux (Ubuntu/Debian). Mac build-only.
> `argon "prompt"` one-shot, `argon` advanced REPL/TUI, `argon settings` TUI.

## 1. Goals / Non-goals

Goals:
- User types English, Argon crafts correct PowerShell / bash command(s) instantly.
- Safe by default: explain always, auto-run only READ, confirm WRITE, double-confirm DESTRUCTIVE.
- Works inside existing terminal (Windows Terminal, pwsh/powershell.exe, bash). No new terminal.
- Cloud LLM, BYOK, OpenAI-compatible + named presets.
- Single binary distribution.

Non-goals v0.1:
- No GUI app, no new terminal/PTY, no shell plugin (Ctrl+G comes in v0.2).
- No undo for file ops, no multi-machine sync, no telemetry.
- No Mac support commitment (build artifact only, untested).

## 2. Commands (CLI surface)

```
argon "find files >1GB in Downloads" [--flags]
argon                         # advanced REPL/TUI (no args)
argon settings                # full settings TUI (required)
argon config <get|set|list>   # non-interactive alt to settings
argon history [--clear]       # view/clear local log
argon upgrade                 # self-update from GitHub Releases
argon version
```

### 2.1 `argon "prompt"` — one-shot agent loop

Flow (streaming by default):
1. Collect context (Sec 5). Init agent `cwd_state` = detected cwd. Always load last 5 turns from own `history.jsonl` even with `--history` off.
2. LLM call with `stream:true` (Sec 6.1) with system prompt + user prompt.
3. Stream deltas live to spinner line, accumulate full text, parse JSON on `[DONE]` (Sec 6). Either `done:true` with final message, or `command` to run. Never execute partial JSON. On DONE, clear spinner line and re-render only `thought` (dim) + command block (yellow) — never show raw JSON unless `--verbose`.
4. Print thought (dim, streamed) + command (styled) + risk tier, enforce policy (Sec 7, deny-first on command+args).
5. If allowed: run via executor with live output streaming (Sec 8), capture output (8k head+tail per turn, 80k session cap), redact secrets, feed back to LLM as next turn. Track `cwd_state`: pure `cd/set-location/pushd` turns update state locally without exec.
6. Repeat until `done:true` or `--max-turns` (default 8) or timeouts (`--llm-timeout` per LLM turn, `--exec-timeout` per command).

Flags:
```
--provider string     # openai|gemini|groq|openrouter|nvidia|cerebras|mistral|deepseek|together|fireworks|xai|ollama|custom (default from config). anthropic via openrouter/custom in v0.1
--model string        # free-text override (defaults in Sec 4 are docs-only, will rot)
--shell auto|powershell|pwsh|bash|zsh (default auto-detect; cmd rejected in v0.1 with hint)
--max-turns int       # default 8, max 20
--llm-timeout int     # seconds per LLM turn, default 90
--exec-timeout int    # seconds per command, default 60
--history             # include shell history file in context (OFF by default, privacy)
--history-limit int   # last N history entries, default 50, max 200
--yes/-y              # auto-confirm WRITE (never DESTRUCTIVE)
--force               # required + type-YES double-confirm for DESTRUCTIVE, otherwise reject
--dry-run             # plan only, never execute
--verbose/-v          # show full JSON, system prompt, timings
--no-color            # plain output
--stream=true|false   # default true; false = wait for full JSON (fallback/debug)
```

Exit codes: 0 done, 1 LLM/exec error, 2 policy-block (includes needs---force).

### 2.4 Non-TTY + install-source guard (required)

- If `!isTTY` (piped, CI, VS Code task without TTY): force plain output (as `--no-color`), disable fullscreen REPL (one-shot only with error message if bare `argon`), and treat WRITE+ as `--dry-run` unless `--yes` is passed. No interactive confirm prompts when non-TTY.
- `argon upgrade` checks install source (`argon version --source`): `curl-script` → self-update allowed; `winget|apt|scoop|deb` → refuse with `use winget upgrade / apt upgrade` message. Never self-overwrite a managed install.

### 2.2 `argon` — advanced REPL/TUI

`BubbleTea + LipGloss`. No args enters fullscreen:
- Top: session context bar (OS/shell/cwd/provider/model/turns).
- Middle: scrollable transcript. Argon AI text = dim cyan `◈ argon:` prefix. Commands = bold yellow block. Output = default white. Errors = red.
- Bottom: input with history (Up/Down), file completion, `/` commands.
- No smaller font possible in terminal (ANSI limit) — dim+prefix simulates it.

Slash commands:
```
/help, /clear (clears view + session context, not history file), /shell, /model, /provider,
/verbose, /dry-run, /stream, /history [on|off], /settings (jump to settings TUI), /quit
```

Session log kept in memory + appended to `history.jsonl`. Up/Down = REPL history. `/clear` drops in-memory turns so next prompt cannot see cleared items.

### 2.3 `argon settings` — full settings TUI (required)

Single source of truth with `argon config`: both read/write same `config.go` struct + `~/.argon/config.toml` (TOML). TUI is interactive editor, `config` is script mirror. Sections (tabs):

1. Provider & Auth: preset select + `baseURL` + `apiKey` (masked input, env-var first / file second, 0600; OS keyring in v0.2) + `model` (free-text) + Test button.
2. Execution & Safety: default policy `auto-read|confirm-write`, allow `--yes` behavior, destructive mode `double-confirm` (locked in v0.1, visible but not changeable to weaker), blocklist preview, `--llm-timeout` + `--exec-timeout`.
3. Defaults: default provider/model, default shell, default max-turns, timeouts, `--history` default on/off, history-limit, `--stream` default.
4. Appearance: color on/off, verbose default, spinner, compact vs detailed explanations.
5. Advanced: config path, `history.jsonl` path + Clear, Export/Import, Reset to defaults, Check upgrade (disabled message when managed install).

Keys: Tab sections, arrows/Enter edit, Ctrl+S save, Esc back (prompt if dirty), `argon config list` masks secrets as `***` unless `--show` is passed.

## 3. History flag (`--history`)

- Default OFF. When ON, include shell history FILE only (v0.1, no scrollback/output capture). Own `history.jsonl` last 5 turns are always sent (Sec 5) regardless of this flag.
- Sources (open read-only + retry on lock, UTF-8 with replacement):
  - Windows PowerShell 5.1: `%APPDATA%\Microsoft\Windows\PowerShell\PSReadLine\ConsoleHost_history.txt`
  - PowerShell 7 Win: `%APPDATA%\Microsoft\Windows\PowerShell\PSReadLine\ConsoleHost_history.txt` (same) or `~/.local/share/powershell/PSReadLine/ConsoleHost_history.txt` if XDG
  - PowerShell 7 Linux: `~/.local/share/powershell/PSReadLine/ConsoleHost_history.txt`
  - bash: `~/.bash_history`, zsh: `~/.zsh_history` (parse `: epoch:0;cmd` format)
- Take last `--history-limit` lines, dedup consecutive, strip secrets via regex (`(?i)(api[_-]?key|token|passwd|secret)\s*[:=].*` → `[REDACTED]`), truncate each to 500 chars, total ≤8k chars.
- In REPL, `/clear` clears session turns; file history still included on next turn if history=on (document this). `argon history --clear` wipes `~/.argon/history.jsonl` + offers to truncate shell history file (explicit only).
- Never send history unless flag/session enabled. Show `+history(32)` indicator when active.

## 4. Providers (Groq default + custom OpenAI-compatible)

No bundled key. Precedence: env var → config file → prompt in settings. Model is free-text; selector offers presets, never validates (IDs rot).

Default: `preset=groq`, `model=openai/gpt-oss-120b`, `baseURL=https://api.groq.com/openai/v1`, key via `GROQ_API_KEY` env (free at https://console.groq.com/keys) or `argon settings`.

| preset key | baseURL | key env | model presets (selector) |
|---|---|---|---|
| groq | https://api.groq.com/openai/v1 | GROQ_API_KEY | openai/gpt-oss-120b (default), openai/gpt-oss-20b, qwen/qwen3.8-27b |
| custom | user input | ARGON_API_KEY | user free-text |

Simple settings screen: Groq model selector (3 above) + Groq API key field. Advanced: full editor restricted to `groq|custom` (baseURL + free-text model + key). No other vendors in v0.1. No-key flow prints the console.groq.com/keys link (one-shot exits 1, REPL stays alive).

*v0.1 implements `openai_compat` driver only.

Request: OpenAI chat-completions, temp 0.2, max_tokens 1024, `stream:true` by default (Sec 6.1). Do NOT set `response_format:json_object` in v0.1 (breaks streaming on some gateways). Prompt for JSON-only + validate on DONE + 1 repair retry. Per-turn `context.WithTimeout(llm-timeout)`, `http.Client{Timeout:0}` for streams. On SSE error fall back to single retry with `stream:false`. Handle 429/5xx with 1 backoff retry (2s, then surface error).

## 5. Context collector (per turn)

```json
{
  "os": "windows_11|ubuntu_22.04|debian_12",
  "shell": "powershell_7|powershell_5.1|bash|zsh",
  "cwd": "E:\\Project\\ARGON",
  "cwd_state": "E:\\Project\\ARGON",
  "ls_top": ["file (12KB)", "dir/"],
  "history": ["git status", "..."],
  "own_last_turns": [ {"prompt": "...", "commands": ["..."], "exit": 0} ],
  "turn": 1, "max_turns": 8
}
```

- `ls_top`: top 30 entries, name + dir flag + size, no recursion, skip hidden unless prompt asks.
- `cwd_state`: agent-owned cwd. Pure `cd/Set-Location/pushd/popd` lines update `cwd_state` locally (resolve `filepath.Abs`) and are NOT executed as subprocesses. All other commands exec with `Cmd.Dir = cwd_state`.
- `own_last_turns`: always include last 5 from `history.jsonl` (prompt+commands+exit, outputs truncated to 500 chars each). `--history` file entries are additional, never replacement.
- Never send file contents unless command output contains them. Never send apiKeys (redact Sec 7).

## 6. Agent JSON contract (LLM returns ONLY this)

```json
{
  "thought": "short, shown dim to user",
  "command": "single shell command or null",
  "run": true,
  "done": false,
  "message": "final answer when done=true, else progress note",
  "risk_self": "read|write|destructive|unknown"
}
```

Rules in system prompt: single shell LINE per turn (pipes allowed, e.g. `ls -lh | sort -k5 -h`); avoid `&&`/`;` multi-step chains unless trivial — prefer one step per turn so output can be observed. Use detected shell dialect (PowerShell vs bash differ — never emit bash for powershell target). No `rm -rf /`, no `diskpart/format/mkfs`, no curl-pipe-shell unless user explicitly asked. done:true REQUIRES command null + run false; the engine tolerates violations (runs the command, then finishes) but the prompt must never emit them. `run:false` with a command means display-only, never execute. After a write/delete turn, done:true is forbidden until a later read-only turn has confirmed the result (engine enforces one verification turn as backstop).

System prompt skeleton: role + shell dialect cheat-sheet + policy tiers + JSON-only + current context injected. Full v2 text lives in `internal/llm/prompt.go` (OPERATING RULES 1-7 + few-shots); do not paraphrase it here.

### 6.2 Per-turn user message (built by `llm.BuildUserMessage`)

One self-contained message per turn (no stacked per-step pairs):

```
CONTEXT: shell=.. os=.. cwd=.. step=N/M
ls: ...
[recent shell history (only with --history)]
TASK: <verbatim original task>
STEPS (yours, oldest first):
1. <cmd> -> exit <n>, <local digest: "N lines; first: ...">
   note: <model's prior message, verbatim>
[or "none yet"]
LATEST OUTPUT (step N-1, exit N[, truncated]):
<<<OUTPUT
<8k... no: head 2500 + tail 1500 with omission marker, OUTPUT>>> neutralized>
OUTPUT>>>
NEXT: Reply with one JSON object. Original task again: "<task>". Does LATEST OUTPUT already answer it? Yes -> done:true with the values. No -> the single next command. [Your last command modified the system — verify it with a read-only command now (done:false).] [FINAL STEP: set done:true now.]
```

Rules: task repeated verbatim top AND bottom; STEPS carry local digests + prior notes only (never raw old outputs, never model-written summaries); only the latest output travels in full; `mustVerify` order appended when the previous turn executed a write/destructive command; `FINAL STEP` at N==M. Repair turn: same system + assistant echo of the bad reply (truncated 600) + user = `RepairPromptFor(concrete parser error)`; repair is never persisted to STEPS.

### 6.1 Streaming (required, v0.1)

- Default ON (`--stream=true`) for both one-shot and REPL. `--stream=false` disables (full wait + parse).
- Transport (OpenAI-compat only in v0.1):
  - `POST {baseURL}/chat/completions` with `"stream":true`, `Accept: text/event-stream`. Parse `data: {...}` lines via `bufio.Reader`, extract `choices[0].delta.content`, end on `data: [DONE]`. Accumulate raw string (cap 32k/turn, 80k/session).
- Display-then-parse (never execute partial):
  - One-shot: print `◈ argon: ` spinner line then stream raw text dim-cyan as it arrives. On DONE, clear spinner line and re-render only final blocks (thought + yellow command block) to hide JSON syntax.
  - REPL (BubbleTea): stream goroutine sends `tea.Msg` per chunk, append to viewport auto-scroll. States: `streaming -> awaitingConfirm -> runningCmd -> streaming...`. Pause stream viewport while command runs.
- Parse only on DONE: `encoding/json` into Sec 6 schema, validate. On parse fail: show raw (verbose) + one repair retry (re-prompt "return valid JSON only").
- Command output streaming: `exec.Cmd.StdoutPipe` merged, live to viewport (white), plus 8k head+tail capture per turn (80k session cap) for next LLM turn. Redact output with secret regex before sending to LLM.
- Ctrl+C: cancel stream ctx + kill child, keep REPL alive. SSE failure → one retry with `stream:false`. Non-TTY forces `stream:false` plain output.

## 7. Risk policy (deny-first on command+args, enforced locally, LLM hint is advisory)

Order: deny-regex → destructive-list → write-list → read-only. Name-only matching is forbidden; always match with args normalized (lowercase, resolve aliases).

- DENY (exit 2, never run even with --force unless explicit `argon --force --dry-run` review? No — deny stays deny): `sudo|doas|runas`, `| sh`, `| bash`, `curl *|*`, `wget *|*`, b64 decode+exec (`certutil -decode`, `FromBase64String`, `base64 -d`), `IEX|Invoke-Expression`, `Set-MpPreference|Disable.*Defender`, `reg delete|reg add HKLM`, `rd /s /q C:\Windows`, `format|diskpart|mkfs`, `:(){:|:&}`, `> /dev/sd*`, `chmod -R 777 /`, `chown -R /*`, `C:\Windows\System32` deletes, `~/.ssh/id_rsa` exfil (any `id_rsa|id_ed25519|.pem` piped to network or sent to LLM — redact).
- READ auto-run: `ls,dir,pwd,echo,cat,type,ps,df,git status/diff/log,find,lsb_release,whoami` ONLY when no deny match and no write args (e.g. `cat > file` is WRITE, `find ... -delete` is DESTRUCTIVE).
- WRITE needs Enter (`--yes` skips): `mkdir,New-Item,touch,cp,Copy-Item,mv,npm/pip/apt install,git add/commit`. Unknown commands default here.
- DESTRUCTIVE double-confirm (locked): `rm -rf,Remove-Item -Recurse -Force,rm -r ~/.config,git push --force,git reset --hard,apt remove/purge,docker system prune`, plus wildcard deletes (`rm *.tmp`, `Remove-Item C:\Temp\*.tmp`, `Remove-Item` piped or with -Recurse). Flow: show explanation + command + require typed `YES` + `--force` flag together. Never with `--yes` alone. Exit 2 otherwise.
- Secrets: redact in prompts, history, AND command outputs before LLM send via regex (`(?i)(api[_-]?key|token|passwd|secret|bearer|aws_.*)\s*[:=].*` → `[REDACTED]`). `risk/policy.go` + `risk/redact.go`, unit-tested.
- PowerShell quoting: pass command via UTF-8 file or `-EncodedCommand` to avoid `$`/`"` interpolation bugs; log exact command string shown to user.

## 8. Executor

- Windows: `powershell -NoProfile -NonInteractive -Command <cmd>` via UTF-8 temp file or `-EncodedCommand` (avoid `$`/quote interpolation); `pwsh` if available else `powershell`. Linux: `bash -lc <cmd>`. `cmd.exe` is rejected in v0.1 with hint to use PowerShell. `zsh` accepted as `bash`-compatible on Linux.
- `Cmd.Dir = cwd_state` (Sec 5). Pure directory changes update state locally, not execed.
- Capture stdout+stderr merged, live-stream to UI (Sec 6.1) + 8k truncate (head+tail) per turn, 80k session cap, enforce `--exec-timeout` kill, no interactive commands in v0.1 (deny list: `vim,nano,ssh,less -R,top,passwd` with message).
- Print command bold yellow before run, output white (streamed), error red. Return exit code to agent for next turn.

## 9. Config / files

- `~/.argon/config.toml` (0600 Unix, user-only ACL Windows), `~/.argon/history.jsonl` (append `{ts,mode,provider,model,prompt,commands,exit,llm_ms}`).
- `%APPDATA%\argon\` on Windows maps to same logical paths.
- Precedence for secrets: env var → config file → settings prompt. `argon config list|get` masks as `***` unless `--show` passed. OS keyring (DPAPI/libsecret) is v0.2.
- Example config:
```toml
[llm]
preset="groq"
model="llama-3.3-70b-versatile"
base_url="https://api.groq.com/openai/v1"
# api_key via env GROQ_API_KEY, not stored unless user types in settings

[defaults]
shell="auto"
max_turns=8
llm_timeout_sec=90
exec_timeout_sec=60
history=false
history_limit=50
confirm_write=true

[ui]
color=true
verbose=false
stream=true
```

## 10. Go layout (initial)

```
cmd/argon/main.go
internal/cli/ (root.go, oneshot.go, repl.go, settings.go, config.go, history.go, upgrade.go)
internal/context/ (os.go, shell.go, ls.go, history.go)
internal/llm/ (client.go, stream.go, presets.go, prompt.go, schema.go)
internal/risk/ (policy.go, redact.go)
internal/exec/ (runner.go, cwd.go)
internal/tui/ (repl.go, settings.go, styles.go)
internal/config/ (load.go, save.go)
internal/update/ (source.go, check.go)
```

Deps: `cobra, bubbletea, lipgloss, bubbles, huh, toml (BurntSushi), gopsutil (light)`. Go 1.23+, CGO off.

## 11. Distribution (GoReleaser, brief)

GoReleaser builds win amd64/arm64, linux amd64/arm64, darwin build-only (untested). Artifacts: exe, tar.gz, .deb/.rpm via nFPM, checksums. Publish: GitHub Releases → winget (auto-PR) + Scoop bucket + apt repo (Pages/R2) + install.ps1/install.sh. `argon upgrade` + background check respect install source (Sec 2.4); `argon version --source` reports `curl|winget|apt|scoop|deb|go-install`.

## 12. v0.1 build order

1. CLI skeleton + config (masked list/get) + context collector (cwd_state + own-turns) + runner (EncodedCommand, cwd_state, output streaming) + redacted history reader (lock-safe).
2. OpenAI-compat client only + streaming SSE + JSON loop (no response_format) + deny-first risk + one-shot with split timeouts + non-TTY guard.
3. REPL basic + streaming viewport + styles + `/clear` semantics.
4. `argon settings` TUI (5 tabs, incl. stream on/off) + `config` single-source mirror.
5. Presets table (docs-only models) + custom + Test button + 429/backoff + source-aware upgrade.
6. GoReleaser + upgrade/source + history cmds + tests for risk/redact/exec/stream-parse/quoting/cwd.

Out for v0.1: PTY, shell plugin, undo, scrollback capture, usage billing, native Anthropic driver, OS keyring, `cmd.exe` support.
