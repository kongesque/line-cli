# LINE CLI

A standalone command-line client built on `beeper/line`. Supports account login,
contact/chat discovery, recent text history, text sending, and live events. It uses your personal LINE
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
./bin/line chats --search "Alice"
./bin/line chats --all --limit 50
./bin/line chats --show-ids

./bin/line whoami --json
./bin/line contacts --json
./bin/line chats --json

# Use a unique exact name (case-insensitive), or a full ID.
./bin/line messages "Alice" --limit 20
./bin/line send "Family group" --text "Hello"
./bin/line messages CHAT_ID --limit 20 --json
./bin/line send CHAT_ID --text "Hello" --json
./bin/line send CHAT_ID --stdin < message.txt

./bin/line watch --json
./bin/line watch --json --timeout 30s

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
serializes session updates to protect token rotation and logout. The watcher uses
short session locks, so other commands can run while its stream is open. A second
watcher is rejected to protect the shared resume position.
Initialization and individual key/authentication lookups may briefly return a
session-busy error to another command; retry that command when the lookup ends.

`messages` reads 1–100 recent messages in the order returned by LINE. It restores
Letter Sealing keys from Keychain and fetches the exact device/group keys needed
for each message. Reading does not mark messages read, register group keys, or
save message history locally. Older messages can remain unreadable when their
original device keys are no longer available.

`chats` shows the 20 most recently active conversations, with contact/group names,
unread counts, chat type, and last activity in your local time. `--limit 0` shows
all rows; `--all` includes inactive chats. `--search TEXT` searches names across
active and inactive conversations, case-insensitively, before applying the limit.
Names remain in memory only. Message previews are used for timestamps; no message
text is displayed or saved by this command. Missing names show a full ID instead.

`messages` and `send` accept a unique, exact chat name. Quote names containing
spaces. Matching ignores case; partial names and duplicate names are rejected.
Use `chats --search TEXT --show-ids` to choose a full ID if names overlap. A full
ID also lets you address contacts without an existing chat. Standard LINE IDs
are recognized automatically; `id:CHAT_ID` explicitly selects an ID with another
format. Name selection uses current LINE names, so use IDs in long-lived scripts.

`send` supports direct chats, rooms, and groups. It checks blocked contacts for
direct messages and encrypts using Letter Sealing when available. Plaintext is
allowed only for an explicitly non-E2EE login or an explicit LINE capability
response. Missing/malformed keys, membership failures, and network errors stop
the send instead of downgrading encryption.

For a group without a usable shared key, an explicit send may register a fresh
group key for all current members. If LINE does not provide complete membership,
the CLI fails rather than creating a key for an incomplete member list.

To verify which path a send used, include `--json` and inspect
`group_key_registered`. `true` means LINE accepted a new group-key registration
during that send. With `encrypted: true` for a group, `false` means an existing
key was reused. This field is also `false` for direct sends and sends without
registration. Human output labels encrypted group sends as either “new group key
registered” or “existing group key.” The field is observational: it does not force
key creation. Older send results cannot establish which path ran.

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

## Live events

`watch` writes one JSON object per line to stdout. Status and reconnect messages
go to stderr. The first run starts at LINE's current operation revision; later
runs resume the revision saved in Keychain. Use `--from-now` to discard the saved
position and start at the current revision. `--limit N` stops after N emitted
events; `--timeout 30s` stops after a duration. Ctrl-C/SIGTERM stop cleanly with
exit status 0. Watch output is always NDJSON (`--json` is optional).

Event formats:

- `message`: `revision` (a decimal string), LINE operation `type` (25 sent,
  26 received), `chat_id`, and a `message` object with the same fields/statuses as
  history. Letter Sealing text uses the same exact device/group-key decoder.
- `operation`: `revision` and LINE operation `type` for other notifications.
  This milestone does not interpret their parameters or emit raw payloads.
- `resync_required`: `revision` is the next position requested by LINE's fullSync.
  There may be missing events; refresh chats and recent history to inspect current
  state. The watcher reports this gap and continues from that revision. It does
  not reconstruct an unlimited history or claim lossless delivery.

Replayed revisions are suppressed. Each resume checkpoint is saved only after
the complete JSON line is written. If the process crashes between writing and
saving, the last event can repeat; consumers should deduplicate by `revision`.
Successfully writing stdout does not confirm that a downstream consumer processed
the event. Broken output, invalid events, and checkpoint failures stop the watcher.
Unreadable encrypted messages are emitted with `decryption_failed` and an empty
text field, and their revisions are checkpointed; they are not retried forever.

The watcher reconnects with a delay of 1–30 seconds after disconnections and
probes Talk authentication every 30 seconds. Token rotation preserves the cursor;
forced logout stops the watcher. Local logout or a new login stops an existing
watcher at its next event or probe. Watch never sends messages, marks them read,
or registers group keys. Only the resume revision and a local login-generation
identifier are added to Keychain; event payloads are not saved by the CLI.

## Output

Read commands print tables by default. With `--json`, stdout contains one JSON
value and stderr contains diagnostics. Empty lists are `[]`. Errors return exit
status 1; help and successful commands return 0.

- `whoami --json`: LINE profile fields, including `mid` and `displayName`.
- `contacts --json`: contacts sorted by effective display name, including `mid`.
- `chats --json`: preserves the original unlimited list of all chats with `id`,
  `type`, and `unread_count`, in server order. An explicit `--limit` caps it.
  Adding `--search TEXT` resolves and filters names, sorts newest first, and adds
  `name` and, when available, `updated_at` (Unix milliseconds). JSON search results
  are unlimited unless `--limit` is supplied. `--show-ids` affects human output only.
- `messages --json`: message ID, sender/recipient, timestamp, content type, text,
  encryption flag, and status (`plaintext`, `decrypted`, `unsupported`, or
  `decryption_failed`). Images, stickers, and other non-text content are listed
  by type; their payloads are not decoded in this milestone.
- `send --json`: server message ID, chat ID, encryption flag,
  `group_key_registered` boolean, and request sequence.
- `watch --json`: a stream of newline-delimited events as described above.

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
direct and group text history. On 2026-09-07, the user also reported a successful
live direct text send: the CLI returned a server message ID with `encrypted: false`.
This confirms server acceptance of a plaintext send; recipient delivery/read status
was not checked. The user subsequently reported an encrypted direct send selected
by exact chat name, with a server message ID and `encrypted: true`. This verifies
server acceptance of encrypted sending; the user also confirmed recipient-side delivery.
The user also reported an encrypted group send with a server message ID and
`encrypted: true`. Group recipient delivery was not confirmed, and the result
does not distinguish existing-key reuse from fresh group-key registration.
Live fresh group-key registration and token rotation still need validation.
Tests of outgoing message construction,
capability fallback, blocked contacts, persistent sequencing, and failed-send
handling use fakes and never transmit a message.

To repeat the live read check after signing in (requires recent encrypted direct
and group conversations):

```sh
LINE_CLI_LIVE_READ=1 go test ./internal/messaging -run '^TestLiveHistory$' -v -count=1 -timeout=4m
```

This explicitly enabled test reads active chat previews and a bounded history
sample, logs only counts/statuses, and skips automatically during ordinary tests.

Watcher tests cover revision replay, resume after interrupted output, fullSync
gaps, token refresh, forced logout, session replacement, group-key changes, and
concurrent session updates. A read-only live check on 2026-09-07 received an SSE
keepalive and successfully queried the account while the stream was open.
Separate CLI checks verified timeout, restart, Ctrl-C, and rejection of a second
watcher. No message events arrived during these short checks, so live message-event
delivery remains unverified. To repeat the bounded stream/concurrency check:

```sh
LINE_CLI_LIVE_WATCH=1 go test ./internal/events -run '^TestLiveWatch$' -v -count=1 -timeout=55s
```

This test updates the saved watch cursor and discards event output. It logs only
startup timing and frame counts; it sends no messages and skips unless enabled.

Attachments, multiple accounts, and Linux/Windows credential storage
are subsequent milestones. Detailed progress
is tracked in the local, Git-ignored `PLAN.md`.
