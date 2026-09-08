#!/bin/sh
set -eu
cd "$(dirname "$0")"
version=${VERSION:-$(git describe --tags --match 'cli-v*' --always --dirty 2>/dev/null || printf dev)}
mkdir -p bin
"${GO:-go}" build -trimpath -ldflags "-s -w -X main.version=$version" -o bin/ "$@" ./cmd/line
