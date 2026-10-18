# Testing LINE CLI

Tests live beside the packages they exercise. Ordinary tests use synthetic data,
fake APIs, and temporary storage. They do not require a LINE login. Requirements
are Go 1.27.2; macOS also needs CGO and Xcode Command Line Tools for Keychain.
Linux and Windows support `CGO_ENABLED=0`.

## Test suites

| Suite | Location | Execution |
| --- | --- | --- |
| CLI, sessions, messaging, events, input, updates, protocol, and E2EE manager | `internal`, `cmd/line`, `pkg/line`, `pkg/e2ee` | Default contributor checks and the three-platform CI matrix |
| Shared signing and crypto runtime | `pkg`, `pkg/ltsm` | Separate command and dedicated Linux CI job; includes slow stress tests |
| Standalone systemd investigation tool | `scripts/systemd-probe` | Separate local command; ordinary tests use synthetic credentials and subprocess helpers |
| Native credential storage | OS-specific session tests | Linux requires an isolated private D-Bus session; Windows uses temporary DPAPI files |
| Boot, systemd, and migration integration | Linux session tests and systemd probe | Explicitly enabled inside a disposable, booted Linux VM |
| Live LINE history and watching | `internal/messaging/live_test.go`, `internal/events/live_test.go` | Explicit authorization and opt-in with a saved session |

Content tests cover authenticated payload precedence for text/location/filenames,
UTF-16 mentions (including emoji, mention-all, overlap and bounds), malformed or
missing location coordinates, attachment unknowns, contact/sticker allowlists,
and bounded rich traversal. CLI tests cover detailed envelopes, optional batched
names, storage/cancellation propagation, partial results, and legacy JSON
compatibility. Event tests verify the shared decoder with unchanged watch JSON.
No content test downloads media, registers keys, or sends read receipts.

Membership tests distinguish absent/null, complete empty, malformed, and populated
member maps; keep invitees separate; retain MIDs when profiles are unavailable;
and verify self identification, bounded name batches, cancellation, and read-only
behavior without crypto initialization. Outgoing mention tests cover full MIDs,
UTF-16 surrogate boundaries, overlap, explicit mention-all, local validation,
text/stdin/reply flows, encrypted/plaintext metadata, saved sequences, failed key
preparation, and lost responses without retries. Protocol tests verify `getChats`
request flags and reject malformed/missing success payloads.

### Main contributor suite

On macOS and Linux:

```sh
go test -race ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee
```

Windows CI uses the same package list without `-race`:

```sh
go test ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee
```

Structured error tests cover stderr envelopes, unchanged stdout arrays, storage
and signal exit codes, large message IDs, redaction, and failures after confirmed
delivery. Mutation tests cover pre-dispatch cancellation, sequence persistence,
authentication rejection followed by failed recovery, lost/missing receipts,
preparation side effects, and plaintext-file upload failures without replay.
All use synthetic APIs/transports; no live LINE mutation is authorized by them.

Request diagnostic tests distinguish transport, response-read, gateway,
envelope, message-decoding, and missing-receipt failures; verify redaction and
wrapped cancellation; and exercise the protocol-to-session-to-CLI error path
with a persisted sequence and exactly one send. History diagnostic tests retain
large returned IDs even for undecryptable messages, count null/malformed IDs,
exclude message content, and reject incompatible flags before session access.

Group encryption tests cover negotiated V1/V2 and the absent-version default
for text, mentions, replies, and files; refreshed negotiation; unsupported
versions; and cipher failures without sending or downgrading to plaintext.
The E2EE manager suite includes a real synthetic-key V1 roundtrip and checks
that V2 encryption errors cannot fall back to mislabeled V1 chunks.

History tests cover lossless IDs/delivered times, chat-bound cursors, inclusive
and exclusive anchors, short/empty/malformed/nonadvancing pages, bounded lookup,
authentication recovery, wrong-chat replies, ownership, and older targets across
reply/download/react/unsend. CLI tests preserve legacy arrays and partial-error
behavior alongside the opt-in page object. Protocol evidence is static Chrome
extension source; these tests do not establish live server interoperability.

Command cancellation tests use fake requests and synthetic subprocesses. Unix
terminal-restoration tests use Python 3's standard-library PTY support when
available. OS-specific files use suffixes such as `_linux_test.go` and
`_windows_test.go`; unsupported platforms exclude them automatically.

### Runner and LTSM regressions

```sh
go test -count=1 -timeout=20m ./pkg ./pkg/ltsm
```

This suite exercises the transpiled Go runtime and its typed runner interface.
It includes fixed signing/login-secret expectations, AES and message encryption
roundtrips, deterministic message vectors, Curve25519 key lifecycle checks, and
signing-cache stress tests. Run it without custom `SECURE_KEY` or `CLIENT_VERSION`
environment overrides, because fixed expectations use the default protocol
fixtures. It does not use saved personal LINE credentials or contact LINE.

The generated LTSM package can take substantial time to compile from an empty
cache. `-timeout` limits execution of each package's test binary, not compilation.
Use the normal Go build cache and avoid redundant concurrent builds of this
dependency. Some runner tests honor `-short`; a short run does not replace the
full regression command above.

Do not edit `pkg/ltsm/wbc_generated.go` or its embedded `data` files. The obsolete
`TestReplayLTSMVectors` placeholder was retired: it was always skipped and
referred to the removed Node/JSON IPC and wazero runners. Current executable
coverage includes `TestGetSignatureFromRunner`, `TestSignFullFlow`,
`TestAesKeyEncryptDecrypt`, `TestCurve25519KeyGenerateAndDestroy`, and the V1/V2
message encryption and vector tests.

### Standalone probe tests

```sh
go test ./scripts/systemd-probe
```

These tests exercise credential parsing, helper-process limits, redaction, and
synthetic fixture handling. Real systemd/TPM observations have a separate
[probe guide](../scripts/systemd-probe/README.md) and
[runtime record](../scripts/systemd-probe/RUNTIME.md).

## Platform integration

The default suite includes fake storage and failure-path tests. Actual native
storage and boot-time checks have additional requirements:

- **Linux Secret Service:** use a disposable private D-Bus session and a private
  keyring data/runtime directory. Enable `LINE_CLI_TEST_SECRET_SERVICE=1` and,
  for the lock check, `LINE_CLI_TEST_KEYRING_LOCK=1`. Run
  `go test -count=1 ./internal/session -run '^TestLinuxNativeSecretService$'`
  inside that session. The isolated setup is recorded in
  [the CI workflow](../.github/workflows/cli.yml).
- **Windows DPAPI:** `go test -count=1 ./internal/session -run '^TestDPAPI'`
  exercises temporary session files on Windows. This also runs in Windows CI.
- **Linux systemd/headless integration:** `LINE_CLI_TEST_SYSTEMD_CREDS=1` is
  reserved for a disposable booted VM. Persistent headless checks additionally
  require `LINE_CLI_TEST_HEADLESS_FIXTURE` and `LINE_CLI_TEST_HEADLESS_MODE`.
  Follow [the headless storage documentation](../internal/session/HEADLESS.md)
  and the probe guide; never reuse a real profile or production keyring as a
  fixture. VM fixtures and disks remain outside Git.

## Live LINE checks

Live tests are skipped by default. Do not enable them as part of ordinary local
checks or CI. They require explicit authorization for the account and intended
actions, plus a saved LINE session.

The current opt-ins are `LINE_CLI_LIVE_READ=1` for `TestLiveHistory` and
`LINE_CLI_LIVE_WATCH=1` for `TestLiveWatch`. History checks read real conversations;
watch checks read real events and update the saved watch cursor. Neither test
sends messages, registers group keys, or marks messages read. `LINE_CLI_LIVE_SECURITY_READ=1` enables
`TestLiveSecurityMediaFlow` for the explicit `LINE_CLI_LIVE_SECURITY_CHAT` ID. It
reads group negotiation capability (without registration), then media capability,
and for plaintext-only files verifies default
refusal with a transport guard that prevents mutation requests.

`LINE_CLI_LIVE_SECURITY_SEND=1` enables `TestLiveSecuritySend`. This requires
separate explicit authorization for the selected chat, text `Security test from
LINE CLI`, and `line-security-test.txt` containing that text plus a newline,
including upload, possible group-key registration, history reads, and download.
It checks encrypted-file capability before sending. Run only that named test
with `-count=1`; never enable it for ordinary suites. After an uncertain failure,
inspect history and obtain retry authorization. `LINE_CLI_LIVE_SECURITY_TEXT_ID`
can reference a confirmed text receipt for an authorized file-only retry.
The test checks envelopes in memory and emits only status/allowlisted diagnostics.

Any future live mutation test must likewise have explicit authorization for its
recipient, content, and actions. Do not print credentials, private message data, encrypted chunks,
or raw server bodies.

## Shared helpers and fixtures

Use package-local `test_helpers_test.go` for fake APIs, stores, setup functions,
and response helpers used by multiple test files. Keep a helper used by one
behavior test beside that test. Helpers with platform dependencies belong in a
matching file such as `test_helpers_linux_test.go`. Test-only helpers stay in
`_test.go` files and do not become production dependencies.

Prefer small synthetic fixtures in Go source. If larger reusable fixtures are
needed, put them in the owning package's `testdata` directory and check that Git
includes them: this repository ignores many JSON and text files by default.
Never add captured credentials or private LINE data as fixtures.

## Formatting, analysis, and CI

```sh
goimports -local "github.com/kongesque/line-cli" -w internal cmd/line pkg/line pkg/e2ee pkg/runner.go
go vet ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
staticcheck ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
./build.sh
./bin/line help
```

When changing runner or LTSM test files, format those files too. Exclude generated
`pkg/ltsm` from vet/staticcheck. The
[CI workflow](../.github/workflows/cli.yml) runs the main package suite on Linux,
macOS, and Windows, with native Linux Secret Service and Windows DPAPI checks.
Its separate crypto job runs the full runner/LTSM regression command with
`CGO_ENABLED=0`, a 20-minute test timeout, and a 30-minute job limit. Live LINE and
disposable booted-VM checks remain manual.

## Native still-image regression coverage

Synthetic image tests cover decoded JPEG/PNG validation, compressed size and
decoded-dimension limits, animation rejection, transparent backgrounds, EXIF
orientation, metadata removal, normalized full/preview dimensions, DM/group/room
envelopes, and authenticating both encrypted uploads. Transport tests assert the
media capability request, OBS SID/path/headers, and context cancellation.
CLI tests verify pre-session input validation, explicit image-reply rejection,
unchanged generic-file bytes, success JSON, and confirmed-message errors after
plaintext upload failure. Fault tests cover every upload/send boundary, missing
capabilities/keys/membership, missing receipts, lost responses, and no retries.

The original eight audit probes now have permanent corrected-behavior coverage:
`internal/messaging/cancellation_test.go` and `internal/cli/deadline_test.go`,
`internal/cli/history_test.go`/`details_test.go` (CLI JSON order/details), `history_test.go` (reply identity and timestamp cursors),
`content_test.go` (authenticated filenames, mentions and locations), and protocol
`history_test.go` (delivered timestamps). No `/tmp` audit overlay is needed.

## Security regression and release checks

Use `GOTOOLCHAIN=go1.27.2` when the host Go version differs. The build guard
checks the compiler selected by `GO`, and rejects an unreviewed toolchain.

```sh
./scripts/check-toolchain.sh
./build.sh
./scripts/security-check.sh ./bin/line
go test ./pkg/line -run '^$' -fuzz '^FuzzBoundedResponse$' -fuzztime=15s
go test ./pkg/line -run '^$' -fuzz '^FuzzSSELine$' -fuzztime=15s
```

Transport-limit tests include a loopback-only HTTP server to verify gzip and
chunked transfer decoding; they need permission to bind a local ephemeral port.
All request payloads and credentials are synthetic. Security tests cover outer
file envelopes, recipient attachment authentication, pre-upload image refusal,
plaintext consent, redirect rejection, and response/SSE budgets. Cross builds do
not substitute for native Linux keyring or Windows DPAPI integration tests.
