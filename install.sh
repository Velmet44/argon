#!/bin/sh
set -eu
repo="Velmet44/argon"
arch="$(uname -m)"
case "$arch" in x86_64) arch="amd64";; aarch64|arm64) arch="arm64";; *) echo "unsupported arch $arch"; exit 1;; esac
url="https://github.com/$repo/releases/latest/download/argon-linux-${arch}.tar.gz"
tmp="$(mktemp -d)"
curl -fsSL "$url" -o "$tmp/argon.tgz"
tar -xzf "$tmp/argon.tgz" -C "$tmp"
mkdir -p "$HOME/.argon/bin"
mv "$tmp/argon" "$HOME/.argon/bin/argon"
echo "argon installed to ~/.argon/bin — add it to PATH, then run: argon version"
