# LINE CLI guide

English | [繁體中文（台灣）](CLI.zh-TW.md) | [日本語](CLI.ja.md) | [ภาษาไทย](CLI.th.md)

Use your personal LINE account from a terminal. You can read conversations,
send messages and files, download attachments, and follow new events. Commands
can guide you through choices in a terminal or return JSON for scripts.

> [!IMPORTANT]
> LINE allows one Chrome-style session at a time. Signing in with this CLI may
> replace an existing LINE Chrome extension or another Chrome-style client
> session.

## Get started

### Install

On macOS or Linux:

```sh
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.sh | sh
```

The installer chooses the right release, checks its checksum, installs `line`
in `~/.local/bin`, and configures `PATH` for common shells. Reopen your
terminal if prompted.

On macOS, you can also use Homebrew:

```sh
brew install kongesque/tap/line-cli
```

On Debian or Ubuntu, install credential storage before you log in:

```sh
sudo apt install libsecret-tools gnome-keyring
```

The Linux keyring must be running and unlocked in the same D-Bus session as
`line`. See
[Linux servers and headless storage](#linux-servers-and-headless-storage) if
you use SSH or a server without an unlocked keyring.

On Windows, run this in PowerShell:

```powershell
irm https://raw.githubusercontent.com/kongesque/line-cli/main/scripts/install-release.ps1 | iex
```

The installer puts `line.exe` in `%LOCALAPPDATA%\line-cli\bin` and adds that
directory to your user `PATH`. Reopen PowerShell if needed.

Release binaries are currently unsigned. You can inspect or download them from
[GitHub Releases](https://github.com/kongesque/line-cli/releases/latest).

Check your installation:

```sh
line version
line help
```

### Sign in and send your first message

```sh
line login
line whoami
line chats
line messages "Family group"
line send "Alice" --text "Hello!"
```

`line login` shows a QR code. Scan it with LINE on your phone, approve the
login, and enter the displayed PIN if asked. Wait for
**Session saved securely** before running other commands.

QR login is experimental. If you have an email address and password configured
on your LINE account, you can use:

```sh
line login --email you@example.com
```

The CLI asks for your password privately and never saves it. There is no
password flag or password environment variable.

### Login options and QR help

| Command or option | Use it when |
| --- | --- |
| `line login` or `line login --qr` | You want to sign in with a terminal QR code. |
| `line login --email ADDRESS` | You want the email/password fallback and phone verification. |
| `line login --qr-url` | You need the one-time QR value for a trusted local QR tool. |
| `line login --force` | You want to skip the question about replacing a saved session. |
| `line login --headless` | You are enrolling a supported Linux host without an unlocked keyring. |

You cannot combine `--email` with `--qr` or `--qr-url`. Both QR methods require
an interactive input terminal; redirecting stdout is fine. Progress, the QR
code, PINs, and warnings go to stderr. The final success message goes to
stdout.

Scan the QR with LINE's scanner. Enter a PIN exactly as displayed, including
leading zeros. A phone approval alone does not mean the CLI finished checking
your profile, exporting keys, and saving your session.

If the QR does not fit in your terminal, the CLI tells you the required width
and suggests `--qr-url`. It will not print a clipped code. `NO_COLOR` disables
explicit colors. With `--qr-url`, treat the value as a secret: use only a
trusted local tool, and do not share it, send it to an online QR generator, or
save it. It remains in terminal scrollback. QR images are not saved. Unset
`QRCODE_DEBUG` before normal QR login because QR encoder debug mode can write
data.

A displayed countdown is approximate. Once LINE confirms expiry, the CLI can
show a new QR code, up to three per login. Network errors and PIN timeouts do
not trigger a new attempt. Plain terminals and redirected stderr show state
changes without a live countdown.

QR login cannot currently handle accounts with Letter Sealing disabled. A
rejected saved QR certificate does not fall back to PIN verification, and an
unknown certificate error stops login. If you see a certificate verification
error, try the email fallback. The `Diagnostic: verifyCertificate` suffix, when
present, contains numeric codes only and no QR, PIN, certificate, token, or
server response text.

### Saved sessions and cancellation

Before contacting LINE, login checks local storage and asks before replacing a
usable saved session. Answer **no** to keep it. `--force` skips that question
only: it does not skip storage checks, headless storage consent,
concurrent-session checks, or protocol errors.

Ctrl-C cancels prompts and QR polling. SIGINT exits 130 and SIGTERM exits 143.
Password echo is restored before exit. Legacy email authentication requests
still use their existing HTTP timeouts.

If you cancel before the new session is saved, the old local session stays in
place. LINE may already have replaced the old Chrome-style session if the final
login request reached its server. The CLI does not retry that request
automatically. If saving reports an uncertain outcome, check
`line auth status --check`; do not restore an older session copy or blindly
repeat login. A successful save is still reported if a signal arrives
afterward.

For SSH, use an interactive terminal:

```sh
ssh -t user@host
line login
# Or, for the email fallback:
line login --email you@example.com
```

SSH does not remove the credential-storage requirement. A supported Linux host
without an unlocked keyring can use
[headless storage](#linux-servers-and-headless-storage).

## Daily commands

### Let the CLI guide you

In an interactive terminal, you can omit some details:

```sh
line messages
line send
line download
line react
line unsend
```

Choose a chat or message by number or search for a name. Use `n` and `p` to
move between pages, `q` to cancel, or Ctrl-C to stop. Prompts are disabled with
`--json` and `--stdin`, so scripts must supply every required argument.

### Find chats and people

```sh
line chats
line chats --search "Family"
line chats --all --limit 50
line chats --show-ids
line contacts
line contacts --search "Alice"
```

`chats` shows conversations, while `contacts` lists friends. Both show 20
results by default in human output. JSON output shows all matches unless you
set `--limit`. Use `--limit 0` for every result in human output. `chats --all`
includes inactive conversations.

Commands that take a `CHAT` accept a full contact, room, or group ID, or a
unique exact name matched without case sensitivity. Quote names with spaces. If
a name is missing or shared by several chats, use
`line chats --search NAME --show-ids` and choose the full ID. IDs are more
reliable in scripts because names can change.

To look up someone who is not in your friend list, use their full user MID
(LINE's user ID). For example, a group message's `from` field in
`line messages CHAT_ID --json` contains its sender's MID.

```sh
line contacts --mid USER_MID
line contacts --mid FIRST_USER_MID --mid SECOND_USER_MID --json
```

Repeat `--mid` for each person. The command looks up only those IDs, keeps
their input order, removes exact duplicates, and shows every requested ID. It
does not add anyone as a friend. Use a full user MID, not a group/room ID,
display name, or public LINE username. The CLI accepts legacy `u` plus 32
lowercase hex digits and current `u`/`U` plus 43 base64url characters.

Human output pairs every MID with its name. Your custom contact name takes
precedence over the profile name. `(unavailable)` means LINE did not return
that profile; `(name unavailable)` means LINE returned a profile without a
usable name. Neither result tells you whether an account was deleted.
`--search` and `--limit` cannot be combined with `--mid`. See
[JSON lookup results](#json-lookup-results) for script behavior.

### Read messages

```sh
line messages "Alice"
line messages "Alice" --limit 50
line messages "Alice" --show-ids
line messages CHAT_ID --json
```

`messages` fetches 1–100 recent messages and displays them oldest to newest.
`--show-ids` includes the IDs needed for replies, reactions, downloads, and
unsend.

Reading history does not mark messages as read or save their text locally. Some
older encrypted messages may be unavailable if LINE no longer provides their
original device keys.

### Send messages and files

```sh
line send "Alice" --text "Hello!"
line send "Alice" --stdin < message.txt
line send "Alice" --text "Sounds good" --reply-to MESSAGE_ID
line send "Family group" --file ./report.pdf
line send "Family group" --file ./report.pdf --reply-to MESSAGE_ID
```

Choose exactly one of `--text`, `--stdin`, or `--file`. Text must be nonempty
and at most 10,000 UTF-16 units. Generic files can be up to 20 MiB. Images,
video, and audio sent with `--file` appear as ordinary files.

The CLI uses Letter Sealing when the conversation supports it. Missing keys,
incomplete group membership, or a transport failure stop an encrypted send
instead of silently sending plaintext. An explicit group send can register a
new group key only when every current member is known.

Add `--json` to see the message ID, whether it was encrypted, whether a group
key was registered, and the request sequence.

> [!CAUTION]
> The CLI attempts each send and other remote change once. If the response is
> lost, check LINE before you retry: the action may have succeeded.

### Download or change a message

Find a message ID with `line messages CHAT --show-ids` or `--json`:

```sh
line download "Alice" --message MESSAGE_ID --output ./received.pdf
line download "Alice" --message IMAGE_MESSAGE_ID --output ./photo.jpg
line react "Alice" --message MESSAGE_ID --reaction love
line react "Alice" --message MESSAGE_ID --remove
line unsend "Alice" --message MY_MESSAGE_ID
```

Reactions are `like`, `love`, `laugh`, `surprise`, `sad`, and `angry`. You can
unsend only your own messages, subject to LINE's server rules.

These commands search the latest 100 messages in the selected chat. Downloads
never overwrite an existing destination. They verify encrypted data before
saving, and remote metadata cannot choose the output path.

`download` supports image, video, audio, and generic file messages up to 20
MiB, with a two-minute transfer timeout. It saves the full media bytes
unchanged, so changing the output extension does not convert the format.
Messages with an external `DOWNLOAD_URL` are not supported. Reading or
downloading never registers a group encryption key.

To send attachment bytes to another command, use an explicit chat and message
ID:

```sh
# In Bash or another shell that supports pipefail:
set -o pipefail
line download CHAT_ID --message MESSAGE_ID --output - | consumer
```

Replace `consumer` with your program. In this mode, stdout contains only
attachment bytes; diagnostics go to stderr. Prompts and `--json` are
unavailable, and the CLI refuses to write binary data to a terminal. Use
`--output ./-` if you actually want a file named `-`.

The CLI authenticates encrypted media before writing the first byte. A download
or authentication failure produces no attachment bytes. A broken pipe or
cancellation after output begins can leave partial output, so check the exit
status and use `pipefail`. For files, prefer `--output PATH`: shell redirection
with `>` can truncate an existing file before the CLI runs.

### Watch new events

```sh
line watch
line watch --timeout 30s
line watch --limit 10
line watch --from-now
```

`watch` writes one JSON event per line to stdout and status messages to stderr.
The first run starts at LINE's current revision. Later runs continue from a
saved checkpoint. `--from-now` discards that checkpoint and starts with new
events.

| Event | Meaning |
| --- | --- |
| `message` | A message sent or received. |
| `operation` | Another LINE notification. |
| `resync_required` | LINE reported a gap; refresh chats and messages. |

Use `revision` to remove duplicates in consumers. If the process stops between
writing an event and saving its checkpoint, the last event may appear again.
Only one watcher can run at a time.

## Updating LINE CLI

```sh
line update --check
line update
line update --check --json
```

`update` shows the installed version, the latest stable GitHub release, the
installation method, and the executable it found. It works without signing in
to LINE. `--check` only checks and shows the next step; it never installs or
acquires session locks. Messaging commands do not check for updates.

| Installation | `line update` behavior |
| --- | --- |
| Official standalone installer on macOS/Linux | Downloads, verifies, and replaces the executable when a newer stable release exists. |
| Homebrew | Prints `brew upgrade line-cli`; Homebrew retains ownership of the executable. |
| Official standalone installer on Windows | Prints a PowerShell installer command to run after this command exits. |
| Source build | Points to the release tag and source-build instructions. |
| Unrecognized installation | Shows the release page and asks you to use your original installation method. |

Self-update requires both an official release build and the `.line-cli-install`
receipt beside the executable, written by the official installers. A manually
copied binary, an older installation without this receipt, or a source build is
not automatically replaced. Use your original installation method to upgrade;
rerunning the official installer also creates the receipt. Custom standalone
installation directories are supported. The Windows upgrade command sets
`LINE_CLI_INSTALL_DIR` to the detected directory, preserving custom locations.

Stop existing commands and watchers, including any service that restarts a
watcher, before updating. Automatic replacement holds the watcher and session
locks without reading credentials. It refuses to proceed when either lock is
unavailable. Do not run an external installer concurrently.

The updater downloads assets from the checked release tag, verifies the archive
against that release's SHA-256 checksums, and stages the executable in the same
directory before replacing it with an atomic rename. Download, checksum, and
extraction failures leave the installed executable intact. Downloads and archive
extraction have size limits. It does not use sudo, retry installation, downgrade
newer versions, or guess the ordering of development/prerelease builds.

`--json` writes one result to stdout, with diagnostics on stderr, and follows
the same installation behavior as human-readable output. Use **both `--check`
and `--json`** for a read-only check. Fields include `current_version`,
`latest_version`, `status`, `installation`, `executable`, `can_self_update`,
`release_url`, and optional `instructions` and `upgrade_command`.

Check statuses are `update_available`, `up_to_date`, `ahead`, and
`unknown_version`. Successful installation returns `updated`; a failed attempt
returns `failed`. `updated_unconfirmed` means replacement happened but the
directory could not be synced: check `line version` before retrying. The
`current_version` field always records the version that started the command;
after `updated`, `latest_version` is the installed version. `can_self_update`
describes support, not whether a newer version exists.

Successful checks and manual instructions exit 0, including when an update is
available. Check or installation failures exit nonzero. If the initial check
fails, no JSON result is written; after an installation failure, the result is
written before the error exit. Cancellation uses the usual signal exit codes.

## Use from scripts

### JSON output and exit codes

Add `--json` to commands that support it:

```sh
line whoami --json
line contacts --json
line contacts --mid USER_MID --json
line chats --search "Family" --json
line messages CHAT_ID --json
line send CHAT_ID --stdin --json < message.txt
```

JSON goes to stdout; diagnostics go to stderr. Success and help exit 0.
Ordinary CLI and network errors exit 1. Storage errors have
[their own exit codes](#storage-exit-codes). SIGINT exits 130 and SIGTERM exits
143. Empty result lists are `[]`.

| Command | Useful JSON fields |
| --- | --- |
| `whoami` | Profile and account ID. |
| `contacts` | Friend names and IDs. |
| `contacts --mid MID` | One record per unique requested MID, with a lookup status. |
| `chats` | ID, type, unread count, and optional activity time. |
| `messages` | ID, sender, time, content, encryption, and status. |
| `send` | Message ID, chat ID, encryption, group-key registration, and sequence. |
| `download` | Output path, byte count, and message ID. |
| `react`, `unsend` | Action, chat ID, message ID, and sequence. |
| `watch` | One JSON event per line. |
| `update` | Installed/latest versions, status, installation method, and next step. Use `--check --json` to only check. |

If some messages cannot be decrypted, `messages --json` still writes the full
array with a status for each message, then exits 1. It never prints encrypted
chunks or raw server response bodies.

### JSON lookup results

`contacts --mid MID --json` returns an array, even for one ID. Every record has
the existing contact fields `mid`, `displayName`, `displayNameOverridden`,
`statusMessage`, and `picturePath`, plus:

| Field | Meaning |
| --- | --- |
| `effectiveDisplayName` | Your custom name, or the profile name if no custom name is set. Empty when no name is available. |
| `status` | `resolved` if LINE returned a profile; `unavailable` if it omitted that MID. |

A missing profile still gets a row with its requested `mid` and empty
name/profile fields. A returned profile can also have an empty name; use the
MID as a fallback when displaying it. Match rows by `mid`, because duplicate
input IDs appear only once.

If LINE returns every profile, the command exits 0. If any are unavailable, it
writes the complete JSON array, reports the count on stderr, and exits 1.
Scripts can still use that JSON as a partial result. A network, authentication,
or malformed-response error produces no result array, even if an earlier batch
succeeded.

## Account and session

### Where your session is stored

The CLI stores one LINE account per operating-system user.

| System | Storage |
| --- | --- |
| macOS | Keychain. |
| Linux | Encrypted session file; its key is in Secret Service. |
| Windows | Current-user DPAPI encrypted session file. |

Your password is used only for login and is never saved. Commands lock the
session during updates. If another command reports that the session is busy,
retry after the current update finishes.

Check local storage without contacting LINE:

```sh
line auth status
line auth status --check --json
```

`auth status` checks whether local storage is accessible. It does not contact
LINE, refresh a token, or prove that the saved login still works. Inaccessible
storage is reported as unreadable rather than logged out. `--check` tests write
readiness where it can do so without an unlock prompt. macOS Keychain and Linux
Secret Service may report `interactive_check_required`; their full write checks
run during interactive login. Headless reboot access remains
`expected_not_verified` until you test it on your host.

To remove the local session:

```sh
line logout
```

Logout does not revoke the session on LINE's servers.

### Token refresh

A normal access token reaches its refresh boundary after about seven days; this
is not a fixed lifetime for your saved session. If the saved refresh token
works, the CLI renews access automatically, even after a restart. If refresh
keeps failing or the refresh token is missing, you may need `line login`.

Read-only requests may refresh credentials and retry once. Sends, reactions,
unsend, and uploads are never replayed automatically: inspect LINE before
repeating an action whose result is uncertain. The watcher reconnects when
needed using saved credentials.

Only an explicit server logout signal invalidates the saved session. Token
expiry, a generic HTTP 401/403, a rejected refresh, or a network failure alone
does not mark it invalid. A server logout signal does not necessarily tell you
whether another client replaced the session. A storage failure after token
rotation stops further requests, because the CLI cannot guarantee that it saved
the new token.

For refresh timing, retry rules, and call-path evidence, see the
[token and session audit](../TOKEN_SESSION.md).

## Linux servers and headless storage

### Enroll without an unlocked keyring

On a supported Linux host without an unlocked Secret Service keyring, enroll
interactively with headless storage:

```sh
line login --headless --email you@example.com
line auth status
line auth status --check --json
```

The example uses email login. `line login --headless` selects experimental QR
login with the same storage protection. You still need an interactive terminal
for enrollment; `--headless` does not make login unattended.

Before contacting LINE, the CLI asks you to accept **Host key; no TPM**
protection. `--force` does not skip this. Headless storage protects against
other unprivileged users, but not against root, malware running as your
account, or someone with a complete copy of the disk. It does not claim TPM
protection. Cancelling before the new session is saved leaves no new local
session.

After enrollment, use ordinary commands without a storage flag. Logging in
again keeps the selected backend. `login --headless` does not convert or
overwrite an existing native session.

Headless storage needs a trusted `/usr/bin/systemd-creds` helper and its user
credential broker. Use the same Unix account and config directory for
interactive commands, SSH, cron, and services. Systemd versions 256–259 are
accepted; Debian 13/systemd 257 and Ubuntu 26.04/systemd 259 have disposable-VM
validation. Other accepted versions still need a successful local check. Older
and unreviewed newer versions are rejected. UniPi and physical TPM behavior
have not been verified.

### Move an existing Linux session

To move an accessible native Linux session to headless storage:

```sh
line auth migrate --storage=headless
```

Migration requires a terminal and the same explicit host-only protection
acceptance. Stop watchers, automation, and older CLI versions first. It
preserves tokens, encryption keys, request sequence, and the watch checkpoint
without contacting LINE or asking for your password.

If migration stops partway through, run the same command again. Do not delete
`migration.pending` or manually replace `session.enc`. `line auth status`
reports `migration_pending` until cleanup succeeds. The
[headless storage internals](../internal/session/HEADLESS.md) explain the recovery
process.

### Run a watcher without a login session

Enroll interactively as a dedicated unprivileged account. After a reboot, run
`line auth status --check` under the same user and environment you will use for
the service. Keep `HOME` and `XDG_CONFIG_HOME` consistent. You are responsible
for installing and managing the service or cron job; the CLI does not create
one.

For a systemd user service, save this example as
`~/.config/systemd/user/line-watch.service` for the enrolled account. Update
`ExecStart` if your binary is elsewhere. `%h` means the account's home
directory. Do not add `User=` to a user service.

```ini
[Unit]
Description=LINE event watcher
StartLimitIntervalSec=300
StartLimitBurst=5

[Service]
Environment=HOME=%h
Environment=XDG_CONFIG_HOME=%h/.config
UMask=0077
NoNewPrivileges=true
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
LockPersonality=true
MemoryDenyWriteExecute=true
ExecStart=/usr/local/bin/line watch
Restart=on-failure
RestartSec=15s
RestartPreventExitStatus=65 69 74 78

[Install]
WantedBy=default.target
```

This setting worked in the Debian 13/systemd 257.13 ARM64 environment reported
in [issue #3](https://github.com/kongesque/line-cli/issues/3). Check it on your
own host before enabling it. `AF_UNIX` lets the credential broker run;
`AF_INET` and `AF_INET6` allow LINE connections and DNS.

```sh
systemctl --user daemon-reload
systemctl --user start line-watch.service
systemctl --user status line-watch.service
# Enable it after you confirm that storage and the watcher work:
systemctl --user enable line-watch.service
```

An administrator may need to enable lingering to start the user service without
an interactive login. If you use a system service instead, add `User=linebot`,
use explicit `/home/linebot` paths for both environment variables, and set
`WantedBy=multi-user.target`.

Watch output contains private message data. Restrict access to its journal or
output files. Cron should use the same account and paths, with `umask 077`.

### Diagnose a headless service

The following systemd settings failed in the
[issue #3](https://github.com/kongesque/line-cli/issues/3) user-service
environment:

| Setting | Observed result |
| --- | --- |
| `PrivateTmp=true` | Headless helper unavailable; exit 69. |
| `ProtectSystem=strict` | Headless helper unavailable; exit 69. |
| `ProtectHome=read-only` | Nonzero exit, including a file write failure. |

Leave these settings and `PrivateUsers=` out of the baseline unit. Adding
`ReadWritePaths=%h/.config/line-cli` alone did not fix the reported failures.
These are observations from one user-service environment, not universal rules
for all system services.

A user namespace created by systemd can hide the host's root UID from a
per-user service. With `PrivateUsers=true`, the root-owned helper may appear to
have an unmapped owner
([systemd 257 documentation](https://github.com/systemd/systemd/blob/v257/man/systemd.exec.xml)).
The CLI rejects a helper whose ownership and permissions it cannot verify.
Read-only mounts can also block locks, path records, refreshed tokens, request
sequences, and watch checkpoints. Both `$XDG_CONFIG_HOME/line-cli` and
`$HOME/.config/line-cli` need write access, even if they are different
directories.

Use `line auth status --check --json` as the service user. Compare it
interactively and inside a transient unit with the same environment and
hardening settings. For example, change the executable path if needed:

```sh
systemd-run --user --wait --collect \
  --property=Environment=HOME="$HOME" \
  --property=Environment=XDG_CONFIG_HOME="${XDG_CONFIG_HOME:-$HOME/.config}" \
  --property=UMask=0077 \
  --property=NoNewPrivileges=true \
  --property='RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6' \
  --property=LockPersonality=true \
  --property=MemoryDenyWriteExecute=true \
  /usr/local/bin/line auth status --check --json
```

This check is local and does not contact LINE. Change one sandbox setting at a
time:

- `headless_helper_untrusted` (exit 69) means the helper or a parent directory
  failed an ownership/permission check. Inspect the sandbox settings named in
  the human error, but also check real installation permissions.
- `headless_unavailable` means discovery, version support, or root execution
  failed. The human error names the stage.
- `storage_unavailable` can mean the credential broker or unsealing failed.
  Helper stderr is not shown.

Restore a working service environment before considering re-enrollment or
deletion. If the service stopped, repair storage, then run
`systemctl --user reset-failed line-watch.service` and start it again. Exit 69
suppresses automatic watcher restarts. That restart policy never applies to
sends or other remote changes.

### Linux session files and upgrades

On Linux, the encrypted session file and both process locks are in
`$XDG_CONFIG_HOME/line-cli` (normally `~/.config/line-cli`). Changing
`XDG_CACHE_HOME` does not change the lock location. Keep the application
directory private (`0700`) and its files private (`0600`). The CLI rejects
unsafe ownership, file types, symlinks at the application directory or files,
and hard-linked files. It does not automatically change permissions on existing
parent directories.

Login checks storage before asking for local input, then checks session and
storage identity again before contacting LINE. It uses a separate temporary
credential item or encrypted file, leaving the active session in place. If
storage is unreadable or corrupt, restore access first or explicitly log out to
remove the local session. If another login or logout changes the session while
you are entering information, start the command again.

Linux also records observed session paths in `native-paths.json` under
`$HOME/.config/line-cli`. If older releases used other `XDG_CONFIG_HOME`
locations, run `line auth status` once with each old location before migrating
or logging out. This lets the CLI avoid deleting a key needed by another known
directory. Do not delete the path record or lock files as a shortcut.

Stop old commands and watchers before upgrading to this lock layout; concurrent
old and new binaries are unsupported. Keep lock files after logout. Multiple
config directories are not a supported way to use several accounts because
native Linux storage uses one key identity per Secret Service keyring.

If you see an uncertain-durability error, the file may already have changed.
Repeat the same local operation; do not restore an older copy. Logout has a
private recovery receipt and can be repeated to finish interrupted cleanup.

### Storage exit codes

| Code | Meaning |
| --- | --- |
| 65 | Invalid format, missing key, failed authentication, or protection mismatch. |
| 69 | Storage or helper unavailable or timed out. |
| 74 | Uncertain durability or failed probe cleanup. |
| 75 | Local contention, storage changed during login, or helper cancellation. |
| 78 | Configuration, consent, migration, repair, or interactive-check requirement. |

Signals take precedence: SIGINT exits 130 and SIGTERM exits 143. Other CLI or
network errors exit 1. `auth status --json` can still write its status object
when storage is unavailable, then return the matching nonzero code.

## Reference

### Command help

The built-in help has the current option list:

```sh
line help
line login --help
line contacts --help
line chats --help
line messages --help
line send --help
line watch --help
line update --help
```

### Build from source

You need Git and Go 1.26 or newer. macOS also needs Xcode Command Line Tools
and CGO for Keychain support.

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

Ordinary tests use fake APIs and credentials. Live tests are disabled by
default and need explicit authorization. Cancellation tests use synthetic
subprocesses. On macOS/Linux, the password terminal test uses Python 3's
standard-library PTY support when available. Native Linux Secret Service
integration needs an explicitly enabled disposable D-Bus session; Windows DPAPI
tests use temporary files.

### Current limitations

- One saved LINE account per operating-system user.
- QR login is experimental and cannot handle accounts with Letter Sealing
  disabled.
- History is recent only, with at most 100 messages per read.
- Sending supports generic files, but not stickers or specialized media sends.
- Downloads support images, video, audio, and files up to 20 MiB; external
  media URLs are unsupported.
- Reading messages does not mark them as read.
- Release binaries are not signed or notarized.
- LINE protocol changes may affect compatibility.

LINE CLI is an independent project derived from `beeper/line`. It is not an
official LINE product and does not need Beeper or a Matrix homeserver.
