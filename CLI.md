# LINE CLI Guide

LINE CLI lets you use a personal LINE account from a terminal. It supports
interactive use, JSON output for scripts, Letter Sealing, file transfers,
replies, reactions, unsend, and live events.

> [!IMPORTANT]
> LINE allows one Chrome-style session. Signing in with `line` may replace an
> existing LINE Chrome extension or another Chrome-style client session.

## Install

### macOS and Linux

```sh
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.sh | sh
```

The installer selects the correct release for your computer, verifies its
checksum, installs `line` into `~/.local/bin`, and configures `PATH` for common
shells. Reopen the terminal if prompted.

On Linux, install credential storage before logging in. For Debian or Ubuntu:

```sh
sudo apt install libsecret-tools gnome-keyring
```

The Secret Service keyring must be running and unlocked in the same D-Bus
session. This is especially important on SSH and headless systems.

Homebrew is also available on macOS:

```sh
brew install kongesque/tap/line-cli
```

### Windows

Run in PowerShell:

```powershell
irm https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.ps1 | iex
```

The installer places `line.exe` in `%LOCALAPPDATA%\line-cli\bin` and adds that
directory to your user `PATH`. Reopen PowerShell if `line` is not immediately
available.

Release binaries are currently unsigned. You can also download and inspect them
from [GitHub Releases](https://github.com/kongesque/line-cli/releases/latest).

Verify the installation:

```sh
line version
line help
```

## Quick start

Your LINE account must have an email address and password. Login requires phone
approval.

```sh
line login
line whoami
line chats
line messages "Family group"
line send "Alice" --text "Hello!"
```

Your password is used only during login and is never saved. Run `line login`
again if the saved session expires.

## Interactive use

Most everyday commands guide you when information is missing:

```sh
line messages
line send
line download
line react
line unsend
```

Chat and message choosers accept a displayed number or a name search. Use `n`
and `p` to change pages, `q` to cancel, and Ctrl-C to stop at any time.

Prompts require an interactive terminal. They are disabled with `--json` and
`--stdin`, so scripts must provide every required argument.

## Finding chats and contacts

```sh
line contacts
line contacts --search "Alice"
line chats
line chats --search "Family"
line chats --all --limit 50
line chats --show-ids
```

`contacts` and `chats` show 20 results by default. Use `--limit 0` for all
results. `chats --all` includes inactive conversations.

Commands that accept `CHAT` support either:

- A unique exact name, matched without case sensitivity.
- A full contact, room, or group ID.

Quote names containing spaces. Partial and duplicate names are rejected; use
`line chats --search NAME --show-ids` to find the correct full ID. IDs are the
safer choice for long-lived scripts because display names can change.

## Reading messages

```sh
line messages "Alice"
line messages "Alice" --limit 50
line messages "Alice" --show-ids
line messages CHAT_ID --json
```

`messages` fetches 1–100 recent messages. Human output is displayed oldest to
newest. `--show-ids` reveals message IDs needed for replies and message actions.

Reading history does not mark messages as read or save message text locally.
Older encrypted messages may be unavailable if LINE no longer provides their
original device keys.

## Sending messages and files

Send text directly:

```sh
line send "Alice" --text "Hello!"
```

Read multiline text from standard input:

```sh
line send "Alice" --stdin < message.txt
```

Reply to a message or send a file:

```sh
line send "Alice" --text "Sounds good" --reply-to MESSAGE_ID
line send "Family group" --file ./report.pdf
line send "Family group" --file ./report.pdf --reply-to MESSAGE_ID
```

Choose only one of `--text`, `--stdin`, or `--file`. Text must be nonempty and
within LINE's 10,000 UTF-16-unit limit. Generic file attachments are limited to
20 MiB. Images, video, and audio sent with `--file` appear as ordinary files.

LINE CLI uses Letter Sealing when the conversation supports it. Missing keys,
incomplete group membership, or transport failures stop an encrypted send; they
do not silently downgrade it to plaintext. An explicit group send may register
a new group key when every current member is known.

Use JSON output to inspect the result:

```sh
line send "Alice" --text "Hello" --json
```

The result includes `encrypted`, `group_key_registered`, the server message ID,
and the request sequence.

> [!CAUTION]
> A send or other remote mutation is attempted once. If the response is lost,
> inspect LINE before retrying because the action may already have succeeded.

## Downloads, reactions, and unsend

Use a message ID from `line messages CHAT --show-ids` or `--json`:

```sh
line download "Alice" --message MESSAGE_ID --output ./received.pdf
line react "Alice" --message MESSAGE_ID --reaction love
line react "Alice" --message MESSAGE_ID --remove
line unsend "Alice" --message MY_MESSAGE_ID
```

Available reactions are `like`, `love`, `laugh`, `surprise`, `sad`, and `angry`.
`unsend` works only for your own messages and remains subject to LINE's server
rules.

These commands search the latest 100 messages in the selected chat. Downloads
never overwrite an existing destination and verify encrypted data before saving.
Remote metadata never controls the output path.

## Live events

`watch` writes newline-delimited JSON to stdout:

```sh
line watch
line watch --timeout 30s
line watch --limit 10
line watch --from-now
```

The first run starts at LINE's current revision. Later runs resume from the saved
revision. `--from-now` discards that checkpoint. Status and reconnect messages go
to stderr.

Event types are:

- `message`: a sent or received message.
- `operation`: another LINE notification.
- `resync_required`: LINE reported a history gap; refresh chats and messages.

Consumers should deduplicate events by `revision`. A crash between writing an
event and saving its checkpoint can repeat the last event. Only one watcher can
run at a time.

## JSON and scripting

Add `--json` to commands that support structured output:

```sh
line whoami --json
line contacts --json
line chats --search "Family" --json
line messages CHAT_ID --json
line send CHAT_ID --stdin --json < message.txt
```

JSON goes to stdout and diagnostics go to stderr. Successful commands and help
return exit status 0; errors return 1. Empty result lists are `[]`.

Important JSON fields include:

| Command | Result |
| --- | --- |
| `whoami` | Account profile and ID |
| `contacts` | Contact names and IDs |
| `chats` | Chat ID, type, unread count, and optional activity time |
| `messages` | Message ID, sender, timestamp, content, encryption, and status |
| `send` | Message ID, chat ID, encryption, group-key registration, and sequence |
| `download` | Output path, byte count, and message ID |
| `react`, `unsend` | Action, chat ID, message ID, and sequence |
| `watch` | Newline-delimited event stream |

If some history entries cannot be decrypted, `messages --json` still writes the
complete array with per-message failure statuses, then exits with status 1. It
never prints encrypted chunks or raw server response bodies.

## Sessions and credential storage

LINE CLI stores one account per operating-system user:

| Platform | Storage |
| --- | --- |
| macOS | Keychain |
| Linux | AES-GCM encrypted session file with its key in Secret Service |
| Windows | Current-user DPAPI encrypted session file |

Session updates are protected by a process lock. Other commands can run while
`watch` is connected, although a brief session-busy error is possible during a
credential update; retry the command after the update finishes.

```sh
line logout
```

`logout` removes the local saved session. It does not revoke the session on
LINE's servers.

## Command help

Use the built-in help for the authoritative option list:

```sh
line help
line chats --help
line messages --help
line send --help
line watch --help
```

## Build from source

Source builds require Git and Go 1.26 or newer. macOS also requires Xcode Command
Line Tools and CGO for Keychain support.

On macOS or Linux:

```sh
git clone https://github.com/kongesque/line-cli.git
cd line-cli
./install.sh
```

On Windows PowerShell:

```powershell
git clone https://github.com/kongesque/line-cli.git
Set-Location line-cli
$env:CGO_ENABLED = "0"
go build -trimpath -o .\bin\line.exe ./cmd/line
.\bin\line.exe help
```

Contributor checks:

```sh
go test -race ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee
go vet ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
go build -trimpath -o bin/line ./cmd/line
```

Ordinary tests use fake APIs and credentials. Live tests require explicit
authorization and are disabled by default.

## Current limitations

- One saved LINE account per OS user.
- Recent history only, with at most 100 messages per read.
- Generic files only; no stickers or specialized media sending.
- Reading messages does not mark them as read.
- Release binaries are not signed or notarized.
- LINE protocol changes may affect compatibility.

LINE CLI is an independent project derived from `beeper/line`; it is not an
official LINE product and does not require Beeper or a Matrix homeserver.
