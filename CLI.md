# LINE CLI

A standalone command-line client built on `beeper/line`. Supports account login,
contact/chat discovery, recent text history, and text sending. It uses your personal LINE
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

# Replace CHAT_ID with an ID from contacts or chats.
./bin/line messages CHAT_ID --limit 20 --json
./bin/line send CHAT_ID --text "Hello" --json
./bin/line send CHAT_ID --stdin < message.txt

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

`messages` reads 1–100 recent messages in the order returned by LINE. It restores
Letter Sealing keys from Keychain and fetches the exact device/group keys needed
for each message. Reading does not mark messages read, register group keys, or
save message history locally. Older messages can remain unreadable when their
original device keys are no longer available.

`send` supports direct chats, rooms, and groups. It checks blocked contacts for
direct messages and encrypts using Letter Sealing when available. Plaintext is
allowed only for an explicitly non-E2EE login or an explicit LINE capability
response. Missing/malformed keys, membership failures, and network errors stop
the send instead of downgrading encryption.

For a group without a usable shared key, an explicit send may register a fresh
group key for all current members. If LINE does not provide complete membership,
the CLI fails rather than creating a key for an incomplete member list.

Request sequences are saved before transmission and shared across CLI processes.
A send is attempted once; if its response is lost, inspect history before retrying
because the message may already have been delivered. Sends and key registration
are not automatically replayed after errors. This version does not persist an
outbox or provide exactly-once delivery across manual retries.

`--text` and `--stdin` are mutually exclusive. Input must be nonempty UTF-8 text
within the CLI's 10,000 UTF-16-unit limit. Stdin preserves newlines and avoids
placing the text directly in process arguments or shell history.

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
- `messages --json`: message ID, sender/recipient, timestamp, content type, text,
  encryption flag, and status (`plaintext`, `decrypted`, `unsupported`, or
  `decryption_failed`). Images, stickers, and other non-text content are listed
  by type; their payloads are not decoded in this milestone.
- `send --json`: server message ID, chat ID, encryption flag, and request sequence.

If some history entries cannot be decrypted, the command still writes the full
JSON array with per-message errors and exits 1. It never substitutes encrypted
chunks or server fallback text for a successfully decrypted message.

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
An opt-in read-only integration check also restored saved keys and decrypted both
direct and group text history. Live sending, fresh group-key registration, and
token rotation still need validation. Tests of outgoing message construction,
capability fallback, blocked contacts, persistent sequencing, and failed-send
handling use fakes and never transmit a message.

To repeat the live read check after signing in (requires recent encrypted direct
and group conversations):

```sh
LINE_CLI_LIVE_READ=1 go test ./internal/messaging -run '^TestLiveHistory$' -v -count=1 -timeout=4m
```

This explicitly enabled test reads active chat previews and a bounded history
sample, logs only counts/statuses, and skips automatically during ordinary tests.

Live events, attachments, multiple accounts, and Linux/Windows credential storage
are subsequent milestones. Detailed progress
is tracked in the local, Git-ignored `PLAN.md`.
