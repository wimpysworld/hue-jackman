#!/usr/bin/env bash
set -euo pipefail

VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
mkdir -p bin
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o bin/hue-jackman ./cmd/hue-jackman
