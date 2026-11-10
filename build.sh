#!/usr/bin/env bash
# Baut home-map.exe (Windows, amd64) – auch unter Linux/macOS, ohne cgo.
set -euo pipefail
cd "$(dirname "$0")/app"
VERSION=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}
mkdir -p ../dist
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags "-H windowsgui -s -w -X main.version=$VERSION" -o ../dist/home-map.exe .
echo "dist/home-map.exe ($VERSION)"
