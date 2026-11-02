#!/usr/bin/env bash
# Generates the files that go next to the binary in a release: shell
# completions and the compressed man page. Run by goreleaser (before hook) and
# usable by hand. Output: build/assets/
set -euo pipefail
cd "$(dirname "$0")/.."
out=build/assets
rm -rf "$out"
mkdir -p "$out"
go build -o "$out/viewdi" ./cmd/viewdi
for sh in bash zsh fish; do "$out/viewdi" completion "$sh" > "$out/completion.$sh"; done
gzip -9 -n -c docs/man/viewdi.1 > "$out/viewdi.1.gz"
rm "$out/viewdi"
