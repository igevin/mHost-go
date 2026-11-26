#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

proxy="$root/resources/darwin-arm64/bin/mhost-dns-proxy"
mkdir -p "$(dirname "$proxy")"
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -o "$proxy" ./cmd/mhost-dns-proxy
go tool mygo build -platform darwin/arm64
