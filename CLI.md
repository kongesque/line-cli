# LINE CLI

A standalone command-line client built on `beeper/line`. The first milestone
supports account login and contact/chat discovery. It uses your personal LINE
account and does not require a Matrix homeserver or Beeper account.

## Build

Requirements: macOS, Go 1.26 or newer, and Xcode Command Line Tools (`xcode-select
--install`). CGO must be enabled to access macOS Keychain. This CLI does not need
libolm, Docker, or the Matrix bridge configuration.

```sh
go build -trimpath -o bin/line ./cmd/line
./bin/line help
```

Keep the executable at a stable path. macOS may ask you to allow Keychain access;
rebuilding or moving the binary may cause another access prompt.

## Commands

```sh
./bin/line login --email you@example.com
./bin/line whoami
./bin/line contacts
./bin/line chats

./bin/line whoami --json
./bin/line contacts --json
./bin/line chats --json

./bin/line logout
```

Login prompts for your password with terminal echo disabled, then displays phone
verification instructions. Your LINE account needs an email address configured.
The password is used only for that invocation and is never saved. This version
requires interactive login; there is no password command-line flag.

LINE allows one Chrome-style client session. Signing in here may replace an
existing LINE Chrome extension or bridge session. The CLI stops using credentials
when LINE reports a forced logout; it does not automatically sign back in.

Session data (tokens, verification certificate, account identity, exported Letter
Sealing keys) is kept in a single macOS Keychain generic-password item:
service `io.github.kongesque.line-cli`, account `default`. One LINE account is
supported in this milestone. A process lock in the user's cache directory
serializes commands to protect token rotation and logout.

`logout` removes the local Keychain item. It does **not** revoke the session on
LINE's servers. The upstream remote logout method is currently unimplemented.

## Output

Read commands print tables by default. With `--json`, stdout contains one JSON
value and stderr contains diagnostics. Empty lists are `[]`. Errors return exit
status 1; help and successful commands return 0.

- `whoami --json`: LINE profile fields, including `mid` and `displayName`.
- `contacts --json`: contacts sorted by effective display name, including `mid`.
- `chats --json`: objects with `id`, `type`, and `unread_count`. This initial
  command lists IDs and unread counts; it does not resolve chat titles or decode
  message previews.

Contact retrieval is batched. Chat retrieval follows pagination and fails without
partial stdout output if the server repeats a cursor. These discovery commands
do not send messages or mark them read.

Raw upstream login logs and HTTP response bodies are suppressed because they may
contain credentials. CLI errors identify the failed operation without printing
server bodies. If a token cannot be refreshed, run `line login` again.

## Development and validation

```sh
go test -race ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee
go vet ./internal/... ./cmd/line
go build -trimpath -o bin/line ./cmd/line
```

Tests use fake API and credential-store implementations; they never log into LINE,
send messages, or read/write your Keychain. After a user completed interactive
login, separate CLI processes successfully loaded the saved Keychain session and
ran `whoami`, `contacts --json`, and `chats --json` against LINE on 2026-09-07.
Live token rotation and encrypted message decryption still need validation.

Sending, decrypted message history, live events, attachments, multiple accounts,
and Linux/Windows credential storage are subsequent milestones. Detailed progress
is tracked in the local, Git-ignored `PLAN.md`.
