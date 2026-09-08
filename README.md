# LINE CLI

Use your personal LINE account from the terminal: find conversations, read and
send messages, share files, reply, react, and watch live events. Run a command
without arguments for guided prompts, or provide flags for scripts.

```sh
line login
line chats
line messages
line send
```

LINE CLI is an independent client derived from [beeper/line](https://github.com/beeper/line).
It needs no Matrix homeserver, Beeper account, libolm, or ffmpeg.

**Before signing in:** LINE allows one Chrome-style session. Logging in here can
replace a LINE Chrome extension or another Chrome-style client session. You need
an email address and password configured on your LINE account, and your phone
for login approval. This CLI currently stores **one account** per OS user.

## Contents

- [Install](#install)
- [First login](#first-login)
- [Command overview](#command-overview)
- [Using guided prompts](#using-guided-prompts)
- [Find people and chats](#find-people-and-chats)
- [Read and send messages](#read-and-send-messages)
- [Files, replies, reactions, and unsend](#files-replies-reactions-and-unsend)
- [Watch live events](#watch-live-events)
- [JSON and scripting](#json-and-scripting)
- [Encryption and saved credentials](#encryption-and-saved-credentials)
- [Limits and current scope](#limits-and-current-scope)
- [Troubleshooting](#troubleshooting)
- [Update or uninstall](#update-or-uninstall)
- [Development and validation](#development-and-validation)

## Install

Building from source requires **Git and Go 1.26 or newer**. Check Go with
`go version`. The commands below install the executable; they do not sign in or
send messages. Shell examples use zsh/bash unless labeled PowerShell.

The guided commands described here are currently on `feat/cli`. The clone
commands below select that branch.

### macOS

Install Xcode Command Line Tools if they are missing:

```sh
xcode-select --install
```

Then clone and install:

```sh
git clone --branch feat/cli https://github.com/kongesque/line-cli.git
cd line-cli
./install.sh
line help
```

The installer uses `~/.local/bin`. If `line` is not found, run:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

Add that line to `~/.zshrc` to keep it in future zsh sessions. For bash, use your
shell's startup file. The installer does not edit these files automatically.

macOS builds need CGO enabled for Keychain. Keep the binary at a stable path;
Keychain may ask for access again after rebuilding or moving it.

### Linux

On Debian/Ubuntu, install the Secret Service helper:

```sh
sudo apt-get update
sudo apt-get install libsecret-tools
```

An **unlocked Secret Service keyring** must be available in your desktop D-Bus
session, for example GNOME Keyring. Installing `secret-tool` alone does not start
or unlock a keyring. A headless/SSH session without that service cannot store
credentials; there is no plaintext fallback.

```sh
git clone --branch feat/cli https://github.com/kongesque/line-cli.git
cd line-cli
CGO_ENABLED=0 ./install.sh
export PATH="$HOME/.local/bin:$PATH"
line help
```

Add the PATH line to your shell's startup file if needed. On other distributions,
install the package providing `secret-tool` and use an available Secret Service
keyring.

### Windows — PowerShell

Build into a user-owned directory:

```powershell
git clone --branch feat/cli https://github.com/kongesque/line-cli.git
cd line-cli
$lineBin = Join-Path $env:LOCALAPPDATA 'Programs\line-cli'
New-Item -ItemType Directory -Force $lineBin | Out-Null
$env:CGO_ENABLED = '0'
go build -trimpath -o (Join-Path $lineBin 'line.exe') ./cmd/line
$env:Path = "$lineBin;$env:Path"
line help
```

This updates PATH for the current PowerShell session. For future terminals, add
`%LOCALAPPDATA%\Programs\line-cli` to your **user Path** in Windows Environment
Variables. Credential protection uses Windows DPAPI; no separate keyring helper
is needed.

### Build without installing

From the repository directory:

```sh
./build.sh
./bin/line help
```

On Windows: `go build -trimpath -o bin/line.exe ./cmd/line`, then
`.\bin\line.exe help`. You can also choose a macOS/Linux installation directory:

```sh
./install.sh /absolute/path/to/your/bin
```

The installer refuses to replace an existing executable unless you pass `--force`.

### Build artifacts

The [release workflow](.github/workflows/cli-release.yml) can build amd64 and
arm64 archives for macOS, Linux, and Windows on a `cli-v*` tag or manual dispatch.
When a run provides artifacts, extract its `.tar.gz` archive before using the
binary. It includes `LICENSE`, `CLI.md`, and `SHA256SUMS` for the executable.
These are workflow artifacts, not automatically published GitHub Releases.
The workflow does not sign or notarize binaries.

## First login

```sh
line login
```

1. Enter the email configured in LINE's account settings.
2. Enter your password at the prompt; typed characters are hidden.
3. Follow the instructions to approve on your phone or enter the displayed PIN.
4. Wait for `Signed in as …`, then try `line whoami` and `line chats`.

If you already have a valid saved session, bare `line login` offers to keep it.
You can also supply your email explicitly:

```sh
line login --email you@example.com
```

The password is never saved and there is no password flag. Login requires an
interactive terminal. After login, subsequent commands restore the saved session
and refresh tokens when possible; you do not need to log in before every command.

## Command overview

| Command | What it does |
| --- | --- |
| `line login` | Sign in or keep your existing session. |
| `line whoami` | Show the signed-in account; add `--show-ids` for its ID. |
| `line contacts` | List contact names; supports search and row limits. |
| `line chats` | List recent conversations, unread counts, and activity times. |
| `line messages` | Choose a conversation and read recent messages. |
| `line send` | Choose a recipient and type a message. |
| `line logout` | Remove the session saved on this device. |
| `line react` | Choose a message, then add or remove your reaction. |
| `line download` | Choose a generic file attachment and save it. |
| `line unsend` | Choose one of your own messages to retract. |
| `line watch` | Stream live events as newline-delimited JSON. |
| `line version` | Show the installed build version. |
| `line help` | Show command help and examples. |

Use `line COMMAND --help` for the complete flags, for example `line send --help`.
Help and version do not access your account.

## Using guided prompts

```text
$ line send

Choose a chat
  1  Alice · Direct
  2  Family group · Group · 3 unread

Number or /search (n: next, p: previous, q: cancel): 1
To: Alice
Message (Enter to send; Ctrl-C to cancel): Hello!
Sent to Alice · encrypted
```

| Input | In a chooser |
| --- | --- |
| A displayed number | Select that row on the current page. |
| A name or `/name` | Search, case-insensitively. |
| `n` / `p` | Go to the next / previous page. |
| `q` | Cancel. |
| `/n`, `/p`, `/q` | Search those literal names. |

Choosers show 20 results per page. Their numbers are temporary; do not reuse them
as chat IDs in another command. Duplicate names include IDs so you can choose
explicitly. Chat choices include inactive conversations and contacts without a
previous conversation.

**At the message prompt, Enter sends.** Empty input does not send. Ctrl-C cancels;
EOF before a complete input line also cancels. To compose multiple lines, use
`--stdin` as shown below. Guided prompts require terminal stdin and stdout; they
are disabled for redirected I/O, `--json`, and `--stdin`.

## Find people and chats

```sh
line contacts
line contacts --search "Alice"
line contacts --limit 0
line contacts --search "Alice" --show-ids

line chats
line chats --search "Family"
line chats --all --limit 50
line chats --limit 0
line chats --show-ids
```

Contacts and recent chats show **20 rows by default**. `--limit 0` shows all rows
in the selected scope. For chats, `--all` includes inactive conversations; search
also includes inactive chats. Last-activity times use your local timezone.

Commands accepting a chat also accept a **unique exact name**, ignoring case:

```sh
line messages "Family group"
line send "Alice" --text "Hello"
```

Quote names containing spaces. Partial names are searches inside a chooser;
command-line recipient arguments must be exact. Duplicate names are rejected,
not guessed. Use `--show-ids` on `contacts` or `chats` to find the full ID. For
long-lived scripts, IDs stay useful when a contact changes their name.
`id:CHAT_ID` explicitly selects an ID format that is not recognized automatically.

## Read and send messages

### Read a conversation

```sh
line messages
line messages "Alice" --limit 10
line messages "Family group" --limit 100 --show-ids
```

Human output shows oldest to newest within the fetched window, with date
separators, sender names, and `You` for your messages. IDs are hidden unless you
request them. Messages retain their line breaks. Reading does **not** mark
messages read, and the CLI does not save a local message-history database.

### Send text

```sh
line send                         # Choose a recipient, then type
line send "Alice"                 # Type a message to Alice
line send "Alice" --text "Hello!"  # Send immediately
line send "GF" --text "สวัสดี"
```

`Alice`, `Family group`, and `GF` are examples; replace them with names in your
account. Explicit `--text` and `--file` commands send without another prompt.

### Send multiline text

```sh
line send "Alice" --stdin < message.txt
```

Or use a quoted heredoc in zsh/bash:

```sh
line send "Alice" --stdin <<'MESSAGE'
Hello Alice,
Are you free tomorrow?
MESSAGE
```

Stdin preserves newlines. Choose exactly one of `--text`, `--stdin`, or `--file`.
Text must be nonempty UTF-8, within **10,000 UTF-16 code units**; some emoji use
more than one unit. `--stdin` avoids putting message contents in process arguments.

## Files, replies, reactions, and unsend

### Send and download files

```sh
line send "Alice" --file ./report.pdf
line send "Alice" --file ./photo.jpg
line download
```

Attachments are **generic files up to 20 MiB**. A photo, video, or audio clip sent
this way appears as a file, not a specialized media message.

For a specific file message:

```sh
line messages "Alice" --show-ids
line download "Alice" --message MESSAGE_ID --output ./received.pdf
```

Replace `MESSAGE_ID` with the numeric **message ID**, not the chat/contact ID.
Choose an existing parent directory and a new filename. Downloads never overwrite
an existing path, and the filesystem must support hard links. Encrypted files
are authenticated before saving.

### Reply to a message

```sh
line messages "Alice" --show-ids
line send "Alice" --text "Yes, that works" --reply-to MESSAGE_ID
```

You can also combine `--reply-to` with `--file` or `--stdin`. If LINE rejects the
reply reference, the command fails; it does not resend as an unrelated message.

### Add or remove a reaction

```sh
line react
line react "Alice" --message MESSAGE_ID --reaction love
line react "Alice" --message MESSAGE_ID --reaction "👍"
line react "Alice" --message MESSAGE_ID --remove
```

Supported reactions: `like` 👍, `love` ❤️, `laugh` 😆, `surprise` 😮, `sad` 😢,
and `angry` 😡. Custom/paid reaction sending is not implemented.

### Unsend one of your messages

```sh
line unsend
line unsend "Alice" --message MY_MESSAGE_ID
```

The guided chooser lists only your messages and asks before retracting the chosen
one. The explicit command acts immediately. LINE's server decides whether a
message is still eligible to be unsent; the CLI does not override its rules.

Downloads, reactions, and unsend locate targets among the chat's **100 most
recent messages**. Older targets return a not-found error.

## Watch live events

```sh
line watch
line watch --json --timeout 30s
line watch --limit 10
line watch --from-now
```

Watch always emits **one JSON object per line** (NDJSON). The first run starts at
LINE's current revision; subsequent runs resume the saved revision. `--from-now`
discards that resume position. `--limit` counts emitted events, and Ctrl-C stops
the stream. Only one watcher may run for the account at a time.

Event types are `message`, `operation`, and `resync_required`. A resync event
signals a possible gap; refresh recent history rather than assuming all events
were recovered. A crash after output but before checkpointing can repeat an
event on restart, so consumers should deduplicate by `revision`.

Watch reconnects with backoff and checks authentication periodically. It does not
send messages, mark messages read, or register encryption keys. The saved cursor
is not a local archive of your conversations. See [CLI.md](CLI.md#live-events)
for event fields and recovery details.

## JSON and scripting

```sh
line whoami --json
line contacts --json
line chats --search "Family" --json
line messages "Alice" --limit 20 --json
line send "Alice" --text "Hello" --json
line watch --json > events.ndjson
```

The redirect above writes message/event data to a file you control. Treat exported
history and event files as private conversation data.

| Behavior | Contract |
| --- | --- |
| Output | JSON data goes to stdout; prompts/diagnostics go to stderr. |
| Lists | Empty lists are `[]`. |
| History | JSON preserves LINE's ordering; human output is chronological. |
| Contacts | JSON is unlimited by default unless `--limit` is explicit. |
| Chats | Bare `chats --json` returns all chats in server order with IDs/types/unread counts. Search enriches results with names. |
| Watch | Always NDJSON, even without `--json`. |
| Missing arguments | Scripted commands fail promptly instead of prompting. |
| Exit status | Success/help: `0`; errors: `1`. Chooser/EOF cancellation: `0`; Ctrl-C outside watch: `130`; Ctrl-C in watch: `0`. |

History can emit an array containing `decryption_failed` entries **and exit 1**.
Check both the exit code and individual message statuses. A send result confirms
LINE's server response; it does not prove recipient delivery or that someone read
it. See [CLI.md](CLI.md#output) for all result schemas.

## Encryption and saved credentials

LINE CLI uses **Letter Sealing** when available. Plaintext is permitted only when
the account or LINE's capability response explicitly indicates no E2EE support.
Missing keys and network failures do not silently downgrade an encrypted send.
Human send output tells you whether the message was encrypted.

`send --json` includes `encrypted` and `group_key_registered`. For an encrypted
group send, `group_key_registered: false` means an existing key was reused;
`true` means the send registered a new key. The CLI does not force fresh key
creation just for testing. Older encrypted messages can be unreadable when their
original device keys are unavailable.

| Platform | Saved session |
| --- | --- |
| macOS | Keychain: service `io.github.kongesque.line-cli`, account `default`. |
| Linux | AES-GCM encrypted `~/.config/line-cli/session.enc` (or under `$XDG_CONFIG_HOME`); the wrapping key is in Secret Service. |
| Windows | Current-user DPAPI encrypted `%LOCALAPPDATA%\line-cli\session.dpapi`. |

The session contains tokens, verification certificate, account identity, exported
Letter Sealing keys, and watch/sequencing state. **Your password is never saved.**
A process lock protects concurrent session updates. If you change accounts while
choosing a recipient or typing, the command stops before continuing with the
replacement account.

```sh
line logout
```

Logout removes this device's saved CLI session. It does not delete your LINE
account, log your phone out, or revoke the session on LINE's servers.

## Limits and current scope

- One saved account per OS user; no account switching profiles.
- Recent history only: 1–100 messages per read, not an unlimited archive.
- Generic file upload/download: 20 MiB. Specialized image/video/audio rendering
  and sticker sending are not implemented.
- Six standard reactions; no custom/paid reaction sending.
- Reading history does not mark it read. There is no mark-read command.
- No automatic send retries, persistent outbox, or exactly-once delivery guarantee
  across manual retries.
- Fresh group-key registration, some token-refresh scenarios, and live watch
  message-event delivery still have validation gaps; see [validation notes](CLI.md#development-and-validation).
- The client depends on LINE's Chrome-style protocol; server-side changes can
  affect compatibility.

**If a send or upload fails, inspect LINE before retrying.** A lost response can
hide a successful send. Encrypted file upload happens before message creation,
while plaintext upload happens afterward; failures can leave an unattached upload
or a message without its file. The CLI never silently retries, converts to ZIP,
or sends a duplicate as a fallback.

## Troubleshooting

| Symptom | What to do |
| --- | --- |
| `line: command not found` | Add the installation directory to PATH, reopen the terminal, and check `command -v line` (PowerShell: `Get-Command line`). |
| Installer says the executable exists | Use `./install.sh --force` to update it deliberately. |
| `go` is missing or too old | Install Go 1.26+ and confirm `go version`. The build/installer also accept `GO=/absolute/path/to/go`. |
| macOS Keychain prompts after a rebuild | Check that the request is for your installed CLI and allow access if you trust that build. Use a stable executable path. |
| Linux cannot read/save Secret Service | Install `secret-tool`, unlock the desktop keyring, and run in its D-Bus session. A bare SSH session may not have one. |
| No saved session, expired session, or forced logout | Run `line login`. Avoid signing in repeatedly from competing Chrome-style clients. |
| Missing email on your LINE account | Configure it in LINE's phone app under Settings → Account before using CLI login. |
| A name is missing or ambiguous | Search `line chats` or `line contacts` with `--search NAME --show-ids`; use the exact name or full ID. |
| Bare commands report missing arguments | Run in a terminal, or supply explicit flags when piping/redirecting. |
| Session is busy | Let the other command finish, then retry. Do not delete lock files while a command is active. |
| Another watcher is already running | Stop the existing `line watch` process before starting a second. |
| Message cannot be decrypted | Its original device/group key may be unavailable. Readable messages are still shown; the failure remains explicit. |
| Action target not found | Confirm the chat and numeric message ID; the target must be within the latest 100 messages. |
| Download destination exists | Choose another path. Also check parent-directory permissions and hard-link support. |
| Account changed during a prompt | Restart the command, confirm `line whoami`, and choose the recipient again. |

For a bug report, include `line version`, your OS, the command shape, and a
sanitized error. Do not post passwords, tokens, credential files, or unredacted
conversation data. Use [GitHub issues](https://github.com/kongesque/line-cli/issues).

## Update or uninstall

### Update a source installation

From your checkout on the branch you want to use:

```sh
git pull --ff-only
./install.sh --force
line version
```

For Windows, pull the changes and repeat the PowerShell build into the same
installation directory. If Git reports local changes or divergent history,
resolve those deliberately instead of discarding your work.

### Uninstall

Run `line logout` first if you also want the locally saved session removed.
Then remove the executable you installed. For the default macOS/Linux location:

```sh
rm "$HOME/.local/bin/line"
```

For the Windows location used above:

```powershell
Remove-Item (Join-Path $env:LOCALAPPDATA 'Programs\line-cli\line.exe')
```

Deleting the executable alone leaves saved credentials in place. Remove any PATH
entry you added if you no longer need that directory. Removing the source
checkout is optional.

## Development and validation

```sh
./build.sh
go test -race ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee
go vet ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
```

Routine CLI tests use synthetic data and fake LINE APIs. CI tests Linux, macOS,
and Windows; native credential checks use temporary DPAPI files and a disposable
Linux Secret Service keyring. Never run the opt-in Linux keyring integration test
against your personal keyring.

Authorized live checks verified login/discovery, encrypted history, text sending,
group replies, encrypted file roundtrips, reactions/removal, and unsend. Guided UX
has fake-API regression tests for cancellation, duplicate names, session changes,
and JSON compatibility. See [CLI.md](CLI.md#development-and-validation) for the
precise scope of completed and pending live checks.

| Path | Purpose |
| --- | --- |
| `cmd/line` | Executable and terminal handling. |
| `internal/cli` | Commands, choosers, and readable/JSON output. |
| `internal/session` | Login, native credential storage, locks, and token refresh. |
| `internal/messaging` | Messages, files, actions, and encryption-key selection. |
| `internal/events` | SSE watching, reconnects, and checkpoints. |
| `pkg/line` | LINE protocol and OBS transport. |
| `pkg/e2ee`, `pkg/runner.go`, `pkg/ltsm` | Letter Sealing and signing runtime. |

Generated LTSM code and its embedded data are required dependencies; do not remove
or edit them as unused code. The separate runner/LTSM regression suites include
slow crypto stress tests.

Further reading: [complete CLI reference](CLI.md), [contributor guidance](AGENTS.md),
[Letter Sealing notes](readme/LETTER_SEALING.md), and [UX design](CLI_UX.md).
`PLAN.md` tracks local implementation progress and is intentionally Git-ignored.

## License and provenance

Distributed under the [MIT license](LICENSE). Shared LINE protocol and crypto code
retain upstream copyright and provenance from [beeper/line](https://github.com/beeper/line).
The Matrix connector and Beeper deployment stack have been removed from this CLI.
