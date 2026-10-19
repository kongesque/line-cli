# LINE CLI: command-line client for personal LINE accounts

English | [繁體中文（台灣）](README.zh-TW.md) | [日本語](README.ja.md) | [ภาษาไทย](README.th.md)

**LINE CLI** is an unofficial Go CLI for your personal LINE account. Read chats,
send messages and files, download attachments, and stream events from your terminal.
Use interactive prompts or JSON output on **macOS, Linux, and Windows**.
No bot account or LINE Messaging API setup is required.

[![CI](https://github.com/kongesque/line-cli/actions/workflows/cli.yml/badge.svg)](https://github.com/kongesque/line-cli/actions/workflows/cli.yml)
[![Release](https://img.shields.io/github/v/release/kongesque/line-cli?filter=v*&label=release)](https://github.com/kongesque/line-cli/releases/latest)
[![Go](https://img.shields.io/github/go-mod/go-version/kongesque/line-cli)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

[Install](#install-line-cli) · [Quick start](#quick-start-send-your-first-line-message) · [Commands](#line-messaging-commands) · [Automation](#automate-line-with-json-and-shell-scripts) · [Full guide](docs/CLI.md)

![LINE CLI: personal LINE messages, files, and automation from the terminal](banner.png)

The README on `main` follows development. For a released version, use the
README at its tag and `line COMMAND --help`. See the [changelog](CHANGELOG.md).

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
To use it in the current shell, run `export PATH="$HOME/.local/bin:$PATH"`.

**Linux desktop:** on Debian or Ubuntu, install credential storage before login:

```sh
sudo apt install libsecret-tools gnome-keyring
```

Run `line` from a desktop session with the Secret Service keyring running and
unlocked in the same D-Bus session. On a server or over SSH without a desktop
keyring, use the headless login below.

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
QR login requires Letter Sealing to be enabled.

If your LINE account has an email address and password configured, you can use
this instead, including when QR login fails:

```sh
line login --email you@example.com
```

Enter your password at the private prompt and complete phone verification if
asked. Your password is never saved.

<details>
<summary>Linux server or SSH: log in without a desktop keyring</summary>

Use your regular Unix account and an interactive terminal (`ssh -t user@host`
when connecting remotely). Headless storage requires `/usr/bin/systemd-creds`
from systemd 256–259 and a working user credential broker.

```sh
line login --headless
```

For email/password login, use `line login --headless --email you@example.com`
instead. Accept **Host key; no TPM** when prompted: this storage does not protect
against a complete disk copy or provide TPM protection. Initial enrollment still
requires terminal interaction and phone approval.

Wait for **Session saved securely**, then run `line auth status --check`.
Continue with the commands below as the same Unix user, without `--headless`.

</details>

### 2. Browse your chats and send a message

```sh
line whoami     # Confirm your account
line chats      # List conversations
line messages   # Choose a chat and read recent messages
line send       # Choose a recipient and enter your message
```

The CLI prompts for missing details in an interactive terminal. Use Ctrl-C to
cancel, or pass a chat name directly as shown below.

A successful send prints `Sent to … · end-to-end encrypted` or
`Sent to … · transport encrypted (no E2EE)`.

## LINE messaging commands

| What you want to do | Command |
| --- | --- |
| Find friends | `line contacts --search "Alice"` |
| Read recent messages | `line messages "Alice" --limit 20` |
| Send a text message | `line send "Alice" --text "Hello!"` |
| Send a file | `line send "Alice" --file ./report.pdf` |
| Download photos, video, audio, or files | `line download` |
| Stream live events | `line watch` |

Replace `Alice` with a full chat ID or a complete chat name that matches only
one chat (case-insensitive). If names are duplicated, run
`line chats --search "Alice" --show-ids` and use the full ID shown.
Run `line COMMAND --help` for options; the
[CLI guide](docs/CLI.md#daily-commands) covers replies, reactions, and pagination.

## Automate LINE with JSON and shell scripts

Add `--json` for structured output on stdout; diagnostics go to stderr.
`--json` and `--stdin` disable prompts, so supply all required arguments.
Use full chat IDs in scripts because names can change.

```sh
line chats --search "Family" --json
line messages CHAT_ID --limit 20 --json
line send CHAT_ID --stdin --json < message.txt
line watch --json > events.ndjson
```

Get `CHAT_ID` from `line chats --json`. Watch output is NDJSON; consumers should
deduplicate by revision. See the [JSON reference](docs/JSON.md) for fields and errors.

Remote changes are attempted once. If a send or other action loses its response,
check LINE before repeating it.

## Letter Sealing encryption and session security

Sends use Letter Sealing (end-to-end encryption, E2EE) whenever available. When
LINE explicitly reports no E2EE support, the CLI sends over HTTPS/TLS without
asking for confirmation. The transfer is encrypted, but LINE can read the content.
Use `--require-e2ee` to block non-E2EE sends. File encryption support is checked
separately from text; missing keys and transport failures never trigger a downgrade.

See [sending and encryption](docs/CLI.md#send-messages-and-files) and
[session storage](docs/CLI.md#where-your-session-is-stored).

## Limitations

- When LINE reports no E2EE support, sends use HTTPS/TLS; LINE can read the
  content. `--require-e2ee` blocks these sends. See
  [LINE's Encryption Report 2025](https://www.lycorp.co.jp/en/privacy-security/security/transparency/encryption-report/2025/).
- History contains up to 100 messages per page. Use `--page --json` and its cursor
  for older available history; full history is not guaranteed. Reading does not
  mark messages as read; some older encrypted messages may be unavailable.
- File sending and media downloads are limited to 20 MiB. `--file` preserves
  original bytes and appears as an ordinary file. Encrypted native image sending
  is disabled; non-E2EE images require explicit LINE capability. Native
  audio/video and sticker sends, and external media URL downloads, are unsupported.
- LINE CLI uses LINE's Chrome-style protocol. Server changes can affect
  compatibility, and some protocol paths need broader live validation.

## Update LINE CLI

Stop running commands and watchers before upgrading; concurrent old and new
versions are unsupported.

```sh
line update --check   # Check the latest release without installing
line update           # Update where supported, or show upgrade instructions
```

Homebrew users should run `brew upgrade line-cli`. See the
[update guide](docs/CLI.md#updating-line-cli) for supported installation methods.

## Documentation and help

Read the [CLI guide](docs/CLI.md) or browse the [documentation index](docs/README.md).
Report bugs and request features through [GitHub Issues](https://github.com/kongesque/line-cli/issues).
Include `line version`, your OS, the command, and a redacted error.
Omit passwords, tokens, QR login values, and private messages.

## Build and contribute

Source builds require **Go 1.27.2**; macOS also needs Xcode Command Line Tools
and CGO. See [build from source](docs/CLI.md#build-from-source) and
[contributing](CONTRIBUTING.md) for setup and development checks.

## License and provenance

LINE CLI is not affiliated with or endorsed by LINE. It is derived from
[beeper/line](https://github.com/beeper/line); shared protocol and crypto code
retain their upstream copyright and provenance. The Matrix connector and Beeper
deployment stack are not part of this project.

Distributed under the [MIT License](LICENSE).
