# LINE CLI contributor guidance

## Project

A standalone Go CLI for a personal LINE account. It identifies as a LINE Chrome
Extension client, so logging in can replace an existing Chrome-style session.
This repository contains no Matrix connector or homeserver integration.

## Build and checks

Requirements: Go 1.26+. macOS needs CGO and Xcode Command Line Tools for Keychain.
Linux and Windows support CGO_ENABLED=0. No libolm or ffmpeg is required.

```sh
./build.sh
./bin/line help
go test -race ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee
goimports -local "github.com/kongesque/line-cli" -w internal cmd/line pkg/line pkg/e2ee pkg/runner.go
go vet ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
staticcheck ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
```

The runner and LTSM regression suites can be run separately with
`go test ./pkg ./pkg/ltsm`; some crypto stress tests are slow. Exclude generated
`pkg/ltsm` from vet/staticcheck. Do not edit `pkg/ltsm/wbc_generated.go` or its
embedded `data/` files.

## Package layout

- `cmd/line`: executable, terminal handling, and exit status.
- `internal/cli`: argument parsing, chat selection, readable/JSON output.
- `internal/session`: login, credential storage, process locks, token refresh,
  saved keys, and persistent request sequences.
- `internal/messaging`: history/decryption, sends/replies, generic files,
  reactions, unsend, and peer/group key negotiation.
- `internal/events`: SSE, reconnect, deduplication, and revision checkpoints.
- `pkg/line`: LINE HTTP/Thrift APIs, OBS transfers, and SSE transport.
- `pkg/e2ee`: Letter Sealing manager.
- `pkg/runner.go` and `pkg/ltsm`: shared signing/crypto runtime.

## Behavior and validation

Preserve explicit capability checks before plaintext fallback. Missing or malformed
keys and transport failures must not silently downgrade encryption. Only an
explicit send may register a group key, and it needs complete membership.
Remote mutations are attempted once with a persisted request sequence; reads can
recover authentication and retry. Never add automatic mutation retries.

Ordinary tests use synthetic data and fake APIs. Linux native keyring integration
requires a disposable private D-Bus session; Windows DPAPI tests use temporary
files. Live LINE tests require explicit authorization for the recipient, content,
and actions. Do not print credentials, encrypted chunks, or raw server bodies.

Keep upstream copyright/license notices and protocol provenance. Consult the
repo-local LINE implementation skill when new behavior needs protocol evidence.
CLI usage and validation notes are in CLI.md. PLAN.md is local and Git-ignored.
