#!/bin/sh
set -eu
cd "$(dirname "$0")"
./scripts/check-toolchain.sh
version=${VERSION:-$(git describe --tags --match 'v*' --match 'cli-v*' --always --dirty 2>/dev/null || printf dev)}
mkdir -p bin
"${GO:-go}" build -trimpath -ldflags "-w -X main.version=$version" -o bin/ "$@" ./cmd/line
