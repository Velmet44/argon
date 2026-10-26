# Argon — Natural-language shell (Go, v0.1 draft)

`argon "find files >1GB"` → proposes the PowerShell/bash command, streams
reasoning, asks before running anything risky. `argon` alone opens the
advanced REPL. `argon settings` opens the full settings TUI.

See `SPEC.md` for the buildable spec. This scaffold is the maximum releasable
skeleton: CLI parses, config loads, risk policy enforces, dry-run works
without an API key.

## Quick start (after Go is installed)

```powershell
# Windows (PowerShell)
$env:ARGON_API_KEY="..."
go run ./cmd/argon -- --help
go run ./cmd/argon -- "list big files" --dry-run
go run ./cmd/argon -- settings
```

```bash
# Linux
export GROQ_API_KEY=...
go run ./cmd/argon "list big files" --dry-run
```

Keys are resolved env-var first, then config file (see `argon config list`).
