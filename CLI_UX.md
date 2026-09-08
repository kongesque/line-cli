# Friendly command-line design

Status: implemented with a line-based chooser. CLI.md is the usage reference.
Existing command flags and JSON contracts remain the compatibility baseline.
Human history uses absolute local dates and native terminal wrapping. Chat tables
retain their established column order and absolute timestamps. Choosers load the
chat/contact directory once, then filter/page locally without holding a session lock.

## Core experience

The seven everyday commands work without memorizing flags or copying IDs.
Missing information is prompted for when using an interactive terminal. Names
lead the output; IDs and protocol details are available through explicit flags.

| Command | Default experience |
| --- | --- |
| `line login` | Ask for email, masked password, and phone verification. |
| `line whoami` | Show the signed-in account name and session status. |
| `line contacts` | Show contact names, with search and optional IDs. |
| `line chats` | Show recent conversations, unread counts, and activity time. |
| `line messages` | Choose a chat, then read a conversation with sender names. |
| `line send` | Choose a recipient, enter a message, and show the result. |
| `line logout` | Remove the local session and show a clear completion message. |

## Login and account

```text
$ line login
Email: you@example.com
Password: ••••••••

Open LINE on your phone and enter: 123456
Waiting for approval…

Signed in as Alex.
Next: line chats

$ line whoami
Signed in as Alex
Session saved securely on this device.

$ line logout
Signed out on this device.
Your LINE account remains active on your phone.
```

Passwords remain masked and never saved. Existing `login --email ADDRESS` skips
the email question. If already signed in, identify the saved account and offer
to keep it or deliberately sign in again before starting another login.
The Chrome-style session replacement notice remains visible during login.

`whoami --show-ids` reveals the account ID; `whoami --json` keeps the existing
profile schema. If credentials cannot be refreshed, say `Your session expired.
Run line login to sign in again.` Do not claim logout revokes a remote session.

## Contacts and chats

```text
$ line contacts
CONTACT
Alice
Ben
คุณแฟน

Showing 20 of 84 contacts.
Find someone: line contacts --search "Alice"

$ line chats
CHAT                  UNREAD   LAST ACTIVE
Family group               3   10:42
Alice                      —   Yesterday
คุณแฟน                     1   Mon 18:30

Read: line messages "Alice"
Write: line send "Alice"
```

Use the existing default of 20 recent chats. Contacts also show 20 rows by
default, with `--limit 0` for all rows. Add `contacts --search TEXT`, `--limit N`,
and `--show-ids`; retain the current chat search, limit, and ID flags.

Human output uses local time, clear date labels, and terminal-width-aware wrapping
for Thai, Chinese, emoji, and long names. Names must never be truncated into an
ambiguous selectable value. Provide absolute timestamps in detailed output.
Unread counts stay unchanged by browsing; reading does not mark messages read.
Empty results explain what happened: `No contacts match "Alixe".`

## Shared chat chooser

Both `messages` and `send` use the same chooser when no chat is supplied:

```text
$ line messages
Choose a chat

  1  Family group     3 unread
  2  Alice
  3  คุณแฟน           1 unread

Chat (number or search text): 2
```

Start with recent chats. A name search can include inactive conversations and
contacts, so sending does not require an existing conversation. Typed searches
show a new numbered result list; the user selects a result. Support another
search, more results, and cancellation without restarting the command.

Numbers refer only to the list currently displayed inside that prompt. They
are never persistent chat aliases or accepted as implicit IDs in another command.
Store the selected full ID in memory. Duplicate names show chat type and an ID
suffix; expand to the full ID when needed to distinguish them. Never guess a
recipient from an ambiguous or partial command-line argument.

## Reading messages

```text
$ line messages "Alice"
Alice · 20 recent messages

Today
10:40  Alice
       Are you free later?

10:42  You
       Yes, after six.

Write: line send "Alice"
```

Show the fetched window oldest to newest for human reading. Resolve sender names,
label the current account `You`, preserve line breaks, and show date separators.
Group chats show each sender. If a name cannot be resolved, show a recognizable
ID fallback. Name resolution failure must not hide otherwise readable messages.

Files show `[File: report.pdf]`; unsupported content gets a readable type label.
Unavailable encrypted content shows `[Unable to decrypt this message]` with a
short diagnostic. It must not appear as blank text or raw encrypted chunks.
Add `messages --show-ids` for replies, reactions, downloads, and unsend targets.
Keep JSON ordering and fields unchanged, including per-message failure status.

## Sending

```text
$ line send
Choose a chat

  1  Family group
  2  Alice
  3  GF

Chat (number or search text): 2
To: Alice
Message (Enter to send; Ctrl-C to cancel): Hello!

Sent to Alice · encrypted
```

`line send "Alice"` skips the chooser and prompts for text. A completed, nonempty
message line sends once when Enter is pressed, as the prompt states. Empty input
does not send. Ctrl-C or end-of-input before a completed line cancels without a
send. Use `--stdin` for multiline input; never reinterpret piped text as answers
to interactive questions. Resolve the recipient before accepting message text.

Existing explicit commands stay direct:

```sh
line send "Alice" --text "Hello!"
line send "Alice" --stdin < message.txt
line send "Alice" --file report.pdf
line send "Alice" --text "Yes" --reply-to MESSAGE_ID
```

Success output names the recipient and reports encryption. Plaintext sends say
`Sent to Alice · not encrypted (Letter Sealing unavailable)`. IDs, request
sequences, and group-key registration details remain available through `--json`.
An uncertain send says `LINE did not confirm this send. Check line messages
"Alice" before trying again.` Preserve existing errors about a partially uploaded
file. Never automatically resend a failed or uncertain mutation.

## Help, installation, and scripting

`line` and `line help` lead with the seven core commands and short examples.
List `watch`, `download`, `react`, `unsend`, and `version` under additional commands.
Every command still supports `--help` without accessing credentials or LINE.

Provide a documented installation path for the built executable so `line` works
from any directory. On macOS/Linux, use an existing user-owned PATH directory
such as `~/.local/bin`; on Windows, use a user-owned directory on PATH. Do not
silently edit shell startup files. If PATH does not include the chosen directory,
print the exact setup instructions. Keep the installed binary at a stable path
for macOS Keychain access.

Interactive prompts require terminal stdin and stdout and are disabled with
`--json` or `--stdin`. Redirected commands missing required arguments fail promptly
with an actionable example. All progress and prompts go to stderr; machine output
stays on stdout. Existing exit codes and JSON contracts remain stable.

Use a simple line-based chooser first. It needs no full-screen terminal framework
or added runtime dependency. Avoid holding the credential lock while waiting for
chat selection or message input; reacquire it and refresh the session before an
operation, detecting an account change instead of sending from the wrong account.

## Implementation order and acceptance

1. Done: terminal-aware prompts, email entry, cancellation, and the shared chooser.
2. Done: bare `messages`/`send` and `send CHAT` use those prompts.
3. Done: account, contacts, and history output with opt-in IDs and sender names.
4. Done: core/additional help, install.sh, and PATH documentation.
5. Added: guided message selection for reactions, downloads, and own-message unsend.

Verify each bare command in a terminal, duplicate names, no matches, Unicode names,
EOF/Ctrl-C, expired/replaced sessions, and redirected input. Keep fake-API regression
tests proving JSON output is unchanged and each explicit send happens at most once.
Design review and implementation tests require no live messages.
