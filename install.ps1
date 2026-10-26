#Requires -Version 5.1
$ErrorActionPreference = "Stop"
$tag = if ($env:ARGON_VERSION) { $env:ARGON_VERSION } else { "latest" }
$repo = "Velmet44/argon"
$url = "https://github.com/$repo/releases/latest/download/argon-windows-amd64.zip"
$dest = "$env:TEMP\argon.zip"
Invoke-WebRequest $url -OutFile $dest
$bin = "$env:USERPROFILE\.argon\bin"
New-Item -ItemType Directory -Force -Path $bin | Out-Null
Expand-Archive $dest -DestinationPath $bin -Force
Write-Host "argon installed to $bin — add it to PATH, then run: argon version"
