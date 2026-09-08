# LINE CLI

A standalone command-line client for your personal LINE account. Supports secure
login, chat discovery, Letter Sealing text and files, replies, reactions, unsend,
and resumable live events.

## Quick start

Install Go 1.26 or newer. macOS also needs Xcode Command Line Tools and CGO for
Keychain access. Linux needs `secret-tool` and an unlocked Secret Service keyring.
Windows uses the current user's DPAPI credential protection.

```sh
git clone https://github.com/kongesque/line-cli.git
cd line-cli
./install.sh
line login
line chats
line messages
line send
```

In Windows PowerShell, build with `go build -trimpath -o bin/line.exe ./cmd/line`
and run `.\bin\line.exe`.

The installer uses `~/.local/bin`; add it to PATH if the installer reports it is
missing. Use `./install.sh --force` for updates. You can also keep using
`./build.sh` and `./bin/line`. Guided prompts require a terminal; explicit flags
and `--json` remain available for scripts.

Use a unique exact chat name or a full chat ID. Run `line help` or see
[CLI.md](CLI.md) for all commands, storage setup, JSON output, limits, and validation.
LINE allows one Chrome-style session; signing in here can replace an existing
LINE Chrome extension session.

## Commands

- Account: `login`, `whoami`, `logout`.
- Discovery: `contacts`, `chats --search NAME`.
- Messages: `messages`, `send --text`, `send --stdin`, `send --reply-to`.
- Files: `send --file`, `download --message ID --output PATH` (generic files up to 20 MiB).
- Actions: `react --reaction NAME`, `react --remove`, `unsend --message ID`.
- Events: `watch --json` with saved revisions and reconnect support.

## Development

```sh
go test -race ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee
go vet ./internal/... ./cmd/line ./pkg/line ./pkg ./pkg/e2ee
./build.sh
```

The protocol and crypto regression suites live in `pkg/`; generated LTSM code
and its embedded runtime data are required for signing and Letter Sealing.
See [AGENTS.md](AGENTS.md) for package layout and development conventions.
CI tests Linux, macOS, and Windows, including native credential-store checks.
The release workflow produces archives for amd64 and arm64 on each platform.

## Origin and license

Derived from [beeper/line](https://github.com/beeper/line). The Matrix connector
and deployment stack have been removed; shared LINE protocol and crypto code
retain their upstream provenance. Distributed under the [MIT license](LICENSE).
