#!/bin/sh
# Source scan on the selected platform; optionally scan one built binary.
set -eu
cd "$(dirname "$0")/.."
./scripts/check-toolchain.sh
"${GO:-go}" run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./cmd/line ./pkg/line/... ./pkg/e2ee ./pkg
if [ "$#" -gt 0 ]; then
  "${GO:-go}" version -m "$1"
  "${GO:-go}" run golang.org/x/vuln/cmd/govulncheck@v1.9.0 -mode=binary "$1"
fi
