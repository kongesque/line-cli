# Development and releases

Maintainer reference for builds, tests, fixtures, CI, and release publication.
Start with [AGENTS.md](../AGENTS.md) for package ownership and contributor rules;
users should use the [CLI guide](CLI.md).

[Build and checks](#build-and-checks) · [Test suites](#test-suites) ·
[Platform integration](#platform-integration) · [Live tests](#live-line-checks) ·
[Release checklist](#release-checklist)

Tests live beside the packages they exercise. Ordinary tests use synthetic data,
fake APIs, and temporary storage. They do not require a LINE login. Requirements
are Go 1.27.2; macOS also needs CGO and Xcode Command Line Tools for Keychain.
Linux and Windows support `CGO_ENABLED=0`.

## Build and checks

```sh
goimports -local "github.com/kongesque/line-cli" -w internal cmd/line pkg/line pkg/e2ee pkg/runner.go
go vet ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
staticcheck ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
./build.sh
./bin/line version
./bin/line help
./bin/line send --help
./bin/line messages --help
git diff --check
```

When changing runner or LTSM test files, format those files too. Exclude generated
`pkg/ltsm` from vet/staticcheck. The
[CI workflow](https://github.com/kongesque/line-cli/blob/main/.github/workflows/cli.yml) runs the main package suite on Linux,
macOS, and Windows, with native Linux Secret Service and Windows DPAPI checks.
Its separate crypto job runs the full runner/LTSM regression command with
`CGO_ENABLED=0`, a 20-minute test timeout, and a 30-minute job limit. Live LINE and
disposable booted-VM checks remain manual.

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
[probe guide](https://github.com/kongesque/line-cli/blob/main/scripts/systemd-probe/README.md) and
[runtime record](https://github.com/kongesque/line-cli/blob/main/scripts/systemd-probe/RUNTIME.md).

## Platform integration

The default suite includes fake storage and failure-path tests. Actual native
storage and boot-time checks have additional requirements:

- **Linux Secret Service:** use a disposable private D-Bus session and a private
  keyring data/runtime directory. Enable `LINE_CLI_TEST_SECRET_SERVICE=1` and,
  for the lock check, `LINE_CLI_TEST_KEYRING_LOCK=1`. Run
  `go test -count=1 ./internal/session -run '^TestLinuxNativeSecretService$'`
  inside that session. The isolated setup is recorded in
  [the CI workflow](https://github.com/kongesque/line-cli/blob/main/.github/workflows/cli.yml).
- **Windows DPAPI:** `go test -count=1 ./internal/session -run '^TestDPAPI'`
  exercises temporary session files on Windows. This also runs in Windows CI.
- **Linux systemd/headless integration:** `LINE_CLI_TEST_SYSTEMD_CREDS=1` is
  reserved for a disposable booted VM. Persistent headless checks additionally
  require `LINE_CLI_TEST_HEADLESS_FIXTURE` and `LINE_CLI_TEST_HEADLESS_MODE`.
  Follow [the headless storage documentation](SESSION.md#headless-storage-engine)
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

## Native still-image regression coverage

Synthetic image tests cover decoded JPEG/PNG validation, compressed size and
decoded-dimension limits, animation rejection, transparent backgrounds, EXIF
orientation, metadata removal, normalized full/preview dimensions, DM/group/room
envelopes, and synthetic encrypted-media authentication. Encrypted native-image
sends remain disabled before remote mutations; fixture coverage does not enable
that path or establish live interoperability. Transport tests assert the
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
plaintext consent (terminal accept/decline/EOF, released prompt locks, changed
accounts, fresh preparation, script suppression, and no mutation replay), redirect
rejection, and response/SSE budgets. Cross builds do
not substitute for native Linux keyring or Windows DPAPI integration tests.

## Release checklist

This checklist is for maintainers. Users should start with the
[update guide](CLI.md#updating-line-cli). Keep changes under **Unreleased** in
the [changelog](../CHANGELOG.md) until a version and release date are chosen.

### 1. Choose the release scope

- Compare the last release with the candidate commit. Describe user-visible
  additions, changed defaults, restrictions, and upgrade actions in the changelog.
- Use a minor version for new commands/options or behavior changes, and a patch
  version for compatible fixes. Do not promise live interoperability based only
  on synthetic tests or static protocol evidence.
- For this candidate, call out encryption consent, file-specific capabilities,
  disabled encrypted native images, and stopping watchers before upgrading.
- Keep detailed evidence in [SECURITY.md](SECURITY.md#security-change-record); release notes
  should explain the effect on users and link to that record.

### 2. Review documentation

- Check examples and limitations against actual command help and implementation.
  Keep English, Thai, Japanese, and Traditional Chinese READMEs/guides aligned.
  Translate prose, but preserve command flags, JSON fields, and stable anchors.
- Check relative links and heading anchors, including inside the extracted
  release archive. Source/development references may link to GitHub.
- Keep the Go module, compiler guard, build instructions, CI and release pins in
  sync. The reviewed toolchain is currently **Go 1.27.2**, not a minimum-version
  claim that accepts every future compiler.
- Review stdout/stderr, partial results, exit codes, and mutation outcomes in
  the [JSON reference](JSON.md). Existing legacy JSON shapes
  should not be described as new envelopes.
- Run `git diff --check`. Record checks actually run and any remaining gates.

### 3. Validate the candidate

Use a clean checkout and the reviewed Go compiler. Run the
[main contributor suite](#main-contributor-suite),
[runner/LTSM regressions](#runner-and-ltsm-regressions),
[build and checks](#build-and-checks), and
[security scans](#security-regression-and-release-checks). Also run:

```sh
sh -n scripts/install-release.sh
```

`staticcheck` must be installed separately. The test sections above describe
platform-specific requirements, opt-ins, and fixtures. Do not edit generated
`pkg/ltsm` code or include it in vet/staticcheck.

Require the [checks workflow](https://github.com/kongesque/line-cli/blob/main/.github/workflows/cli.yml)
to pass on the exact candidate commit, including the native Linux Secret Service
and Windows DPAPI tests. Cross-compilation does not replace native CI.
Ordinary validation uses synthetic data and fake APIs. Live LINE tests need
explicit authorization for recipient, content, and actions; this checklist
does not authorize messages, uploads, logins, or other live actions.

### 4. Rehearse packaging without publishing

From GitHub Actions, run **LINE CLI release artifacts** on the candidate branch,
or use GitHub CLI after replacing `CANDIDATE_BRANCH`:

```sh
gh workflow run cli-release.yml --ref CANDIDATE_BRANCH
```

Select a branch, not a version tag. The publish job runs on `refs/tags/v*`;
dispatching against a version tag can publish a release. A branch dispatch only
builds and uploads Actions artifacts, with a branch-name development version.

Inspect all six target artifacts: Linux, Windows, and macOS, each amd64/arm64.
Each outer Actions artifact contains `line-OS-ARCH.tar.gz`. Extract that tarball
and check:

- `line` or `line.exe` is at the archive root with the expected target/architecture.
- `SHA256SUMS` verifies the executable. Run `sha256sum -c SHA256SUMS` or
  `shasum -a 256 -c SHA256SUMS` inside the extracted directory.
- `LICENSE`, all `README*.md`, `banner.png`, `CHANGELOG.md`, `AGENTS.md`, `CONTRIBUTING.md`, and
  the complete `docs/` tree are included. Open the guide and follow its relative
  links. Do not recreate removed documentation paths as duplicate copies.
- On matching native hosts, run the extracted binary's `version` and `help`.
  No LINE login is needed. Review source and binary scan results for every target.

Release binaries remain unsigned and unnotarized; checksums verify correspondence
to the release assets and are not a code-signing identity.

### 5. Tag and publish

Before tagging, move **Unreleased** entries to the chosen version/date, update
comparison links, and commit those final notes. Recheck CI on this exact commit.

**Pushing any `v*` tag starts packaging and automatically creates a public GitHub
release after the package matrix passes.** The workflow currently uses generated
notes and does not automatically mark prerelease tags as prereleases. Do not push
a rehearsal or prerelease tag through this path without first adjusting the
publication workflow. Use the branch rehearsal above.

Create an annotated stable-version tag on the validated commit and push only
that tag when ready to publish. See the
[release workflow](https://github.com/kongesque/line-cli/blob/main/.github/workflows/cli-release.yml).

### 6. Verify distribution

- Confirm the public tag, version string, six archives, and `SHA256SUMS.txt`
  identify the intended release. This root checksum file covers archives;
  each archive's `SHA256SUMS` covers its executable.
- Replace generated commit listings with curated highlights, upgrade actions,
  security restrictions, and links to the versioned changelog/security record.
- Update `kongesque/tap` with the release URLs and archive checksums; verify
  `brew upgrade line-cli`. Check standalone installers and `line update --check`
  against the published assets on supported hosts.
- Check installation and `version`/`help` without replacing a real user's saved
  LINE session. Restart existing automation only after the upgrade is verified.
- Record native-host checks and outstanding live interoperability limits in the
  release notes. Do not present a release as validated solely because packaging passed.
