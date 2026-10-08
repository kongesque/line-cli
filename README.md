# LINE CLI: command-line client for personal LINE accounts

English | [繁體中文（台灣）](README.zh-TW.md) | [日本語](README.ja.md) | [ภาษาไทย](README.th.md)

**LINE CLI** is an unofficial, open-source LINE command-line client written in Go.
Send LINE messages from your terminal, read personal and group chats, share files,
download attachments, and stream live events. Use interactive prompts for everyday
messaging or JSON output for shell scripts and AI agent workflows.

Works on **macOS, Linux, and Windows**, with Letter Sealing end-to-end encryption
when supported by the conversation. Sign in with your personal LINE account;
no bot account or LINE Messaging API setup is required.

[![CI](https://github.com/kongesque/line-cli/actions/workflows/cli.yml/badge.svg)](https://github.com/kongesque/line-cli/actions/workflows/cli.yml)
[![Release](https://img.shields.io/github/v/release/kongesque/line-cli?filter=v*&label=release)](https://github.com/kongesque/line-cli/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/kongesque/line-cli)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[Install](#install-line-cli) · [Quick start](#quick-start-send-your-first-line-message) · [Commands](#line-messaging-commands) · [Automation](#automate-line-with-json-and-shell-scripts) · [Full guide](docs/CLI.md)

![LINE CLI: personal LINE messages, files, and automation from the terminal](banner.png)

## Install LINE CLI

### macOS: Homebrew

```sh
brew install kongesque/tap/line-cli
```

### macOS and Linux: standalone installer

```sh
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.sh | sh
```

The installer detects your OS and architecture, verifies the release checksum,
and installs `line` into `~/.local/bin`. Reopen your terminal if prompted to
apply its `PATH` setup.

**Linux desktop:** before login, make sure a Secret Service keyring is running
and unlocked in the same D-Bus session as `line`. On Debian or Ubuntu, install
the required tools with:

```sh
sudo apt install libsecret-tools gnome-keyring
```

**Linux server or SSH:** packages alone are insufficient. On supported hosts,
`line login --headless` offers host-key storage without an unlocked keyring.
Enrollment remains interactive; this storage does not protect against a complete
disk copy or provide TPM protection.
See [headless setup, migration, and services](docs/CLI.md#linux-servers-and-headless-storage).

### Windows: PowerShell

```powershell
irm https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.ps1 | iex
```

The installer verifies the checksum and adds `line.exe` to your user `PATH`.
Reopen PowerShell if needed. Windows needs no additional keyring package.

You can also [download a release](https://github.com/kongesque/line-cli/releases/latest)
or [build from source](#build-and-contribute). Release binaries are unsigned and
not notarized.

Check your installation:

```sh
line version
line help
```

## Quick start: send your first LINE message

> [!IMPORTANT]
> Signing in may replace your existing LINE Chrome extension or another
> Chrome-style client session. LINE CLI saves one account per operating-system user.

### 1. Sign in with LINE on your phone

```sh
line login
```

Scan the terminal QR code with LINE's scanner on your phone, approve the login,
and enter the displayed PIN if asked. Wait for **Session saved securely**.
QR login is experimental and requires Letter Sealing to be enabled.

If your account has an email address and password configured, you can use:

```sh
line login --email you@example.com
```

Your password is entered privately and never saved. Login checks storage and asks
before replacing a usable saved session.
[Login options and QR troubleshooting](docs/CLI.md#login-options-and-qr-help).

### 2. Browse your chats and send a message

```sh
line whoami     # Confirm your account
line chats      # List conversations
line messages   # Choose a chat and read recent messages
line send       # Choose a recipient and enter your message
```

The CLI prompts for missing details in an interactive terminal. Use Ctrl-C to
cancel, or pass a chat name directly as shown below.

## LINE messaging commands

| What you want to do | Command |
| --- | --- |
| Find friends | `line contacts --search "Alice"` |
| Look up a person by user ID | `line contacts --mid USER_MID` |
| Find conversations and groups | `line chats --search "Family"` |
| Read recent messages | `line messages "Alice" --limit 20` |
| Send a text message | `line send "Alice" --text "Hello!"` |
| Send a file | `line send "Alice" --file ./report.pdf` |
| Download photos, video, audio, or files | `line download` |
| Add or remove a reaction | `line react` |
| Unsend your own message | `line unsend` |
| Stream live events | `line watch` |
| Check local session storage | `line auth status --check` |
| Remove your saved local session | `line logout` |

Replace example names with your own. Names must be unique exact matches
(case-insensitive); quote names with spaces. For duplicate names, use
`line chats --search "Alice" --show-ids` and pass a full chat ID.

Run `line COMMAND --help` for options. Downloads, reactions, and unsend also
support interactive selection. [Chat selection guide](docs/CLI.md#find-chats-and-people).

### Reply to LINE messages and download attachments

Find IDs with `--show-ids`, then replace `MESSAGE_ID` in the examples:

```sh
line messages "Alice" --show-ids
line send "Alice" --text "Sounds good" --reply-to MESSAGE_ID
line react "Alice" --message MESSAGE_ID --reaction love
line download "Alice" --message MESSAGE_ID --output ./received.pdf
```

For downloads, choose an attachment's ID and an appropriate filename. Existing
files are never overwritten.
[More reactions, unsend, and download options](docs/CLI.md#download-or-change-a-message).

## Automate LINE with JSON and shell scripts

Add `--json` for structured output on stdout; diagnostics go to stderr.
`--json` and `--stdin` disable prompts, so supply all required arguments.
Use full chat IDs in scripts, since names can change.

```sh
line chats --search "Family" --json
line messages CHAT_ID --limit 20 --json
line send CHAT_ID --stdin --json < message.txt
line watch --json > events.ndjson
```

Get `CHAT_ID` from `line chats --json`. `--stdin` accepts multiline text from a
file or pipe. `watch` emits newline-delimited JSON (NDJSON) and resumes from its
saved checkpoint; consumers should deduplicate by revision. Treat exported
messages and events as private data.

Remote changes are attempted once and never retried automatically. If a send,
upload, reaction, or unsend loses its response, check LINE before repeating it
to avoid duplicating an action.

See [JSON fields and exit codes](docs/CLI.md#json-output-and-exit-codes) and
[live event streaming](docs/CLI.md#watch-new-events) for integration details.

## Letter Sealing encryption and session security

- **Encryption:** Letter Sealing is used when supported. Missing or malformed keys
  and transport failures never silently downgrade an encrypted send to plaintext.
  JSON send results report encryption status.
- **Storage:** macOS Keychain, Linux AES-GCM session files with keys in Secret
  Service, or Windows current-user DPAPI. Headless Linux uses host-key storage.
- **Session recovery:** access tokens refresh automatically when saved refresh
  credentials work, even after a restart. Sign in again if refresh cannot recover
  access or LINE invalidates the session.

[Session storage](docs/CLI.md#where-your-session-is-stored) ·
[Token refresh and recovery](docs/CLI.md#token-refresh)

## Limitations

- Recent message history only, up to 100 messages per read. Reading does not mark
  messages as read; some older encrypted messages may be unavailable.
- File sending and media downloads are limited to 20 MiB. Images, video, and audio
  sent with `--file` appear as ordinary files; stickers and specialized media
  sending are not supported. External media URLs cannot be downloaded.
- LINE CLI uses LINE's Chrome-style protocol. Server changes can affect
  compatibility, and some protocol paths need broader live validation.

## Update LINE CLI

Stop running commands and watchers before upgrading; concurrent old and new
versions are unsupported.

```sh
line update --check   # Check the latest release without installing
line update           # Update where supported, or show upgrade instructions
```

Official standalone installations on macOS and Linux can update in place.
Homebrew uses `brew upgrade line-cli`; other installations receive upgrade
instructions. Checks require no LINE login and run only when requested.
[Update details](docs/CLI.md#updating-line-cli).

## Documentation and help

- [CLI guide](docs/CLI.md): complete usage, troubleshooting, and server setup.
- [Token and session audit](TOKEN_SESSION.md): refresh and recovery internals.
- [GitHub Issues](https://github.com/kongesque/line-cli/issues): bugs and feature requests.

For bug reports, include `line version`, your OS, the command, and a redacted
error. Omit passwords, tokens, QR login values, and private messages.

## Build and contribute

Source builds require Git and **Go 1.26+**. macOS also needs Xcode Command Line
Tools and CGO for Keychain support. On macOS or Linux:

```sh
git clone https://github.com/kongesque/line-cli.git
cd line-cli
./install.sh
```

Follow any PATH instructions printed by the installer. See the
[source-build guide](docs/CLI.md#build-from-source) for Windows PowerShell commands.
Linux storage requirements also apply to source builds.

For local development:

```sh
./build.sh
./bin/line help
go test -race ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee
go vet ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg
```

See [contributor guidance](AGENTS.md) for the full checks and protocol requirements.
CI covers all three platforms and native credential storage. Tests use synthetic
data and fake APIs; live tests require explicit authorization.

## License and provenance

LINE CLI is not affiliated with or endorsed by LINE. It is derived from
[beeper/line](https://github.com/beeper/line); shared protocol and crypto code
retain their upstream copyright and provenance. The Matrix connector and Beeper
deployment stack are not part of this project.

Distributed under the [MIT License](LICENSE).
