#!/bin/sh
# Keep release builds on the explicitly reviewed security toolchain. Inspect the
# compiler chosen by GO/GOTOOLCHAIN, not an unrelated executable on PATH.
set -eu
compiler=$("${GO:-go}" env GOVERSION)
if [ "$compiler" != "go1.27.3" ]; then
  printf '%s\n' "LINE CLI requires the reviewed Go 1.27.3 toolchain (selected: $compiler). Set GOTOOLCHAIN=go1.27.3." >&2
  exit 1
fi
