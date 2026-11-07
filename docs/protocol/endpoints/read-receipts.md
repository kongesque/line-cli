# Read receipts (`sendChatChecked`)

## Evidence and scope

Inspected the public LINE Chrome extension **3.7.2** on 2026-11-07, matching
`pkg/line/client.go` `ExtensionVersion` and the repository application headers.
The package was downloaded from Google's Chrome extension update service for
extension `ophjlpahpchlmihnnnihgmmeilfjmjjc`
([official listing](https://chromewebstore.google.com/detail/line/ophjlpahpchlmihnnnihgmmeilfjmjjc)).
Package SHA-256:
`3722ab02edb0aba49c229399ee8df5b4b5a38c60b76efadadfbae395015eb859`, the same
package used for the history and media-download investigations.

This is static source evidence. No login, capture, account read, or live read
receipt was performed, and nothing here establishes live interoperability. The
extension remains LY Corporation's copyrighted software; its source is not
committed. Locators are minified symbols in this package's `static/js/main.js`
and may change between releases.

## Request

| Locator | Behavior |
| --- | --- |
| `SD` | Shared RPC wrapper: POST `/api/` plus the method path, with a JSON array of positional arguments (the leading HTTP options object is excluded). |
| `Ij` | `talk/thrift/Talk/TalkService/sendChatChecked`. |
| `HO` | Request-sequence counter: starts at a random 31-bit value and increments modulo 2^31 for each mutation. It is not a constant `0`. |
| `kF` | Mutation hook; spreads the array passed to `mutate` as positional arguments after the options object. |

Both call sites pass four positional arguments:

| Position | Value |
| --- | --- |
| 1 | Request sequence from `HO()` |
| 2 | Chat MID (user, group, or room) |
| 3 | ID of the newest message being marked read |
| 4 | Literal `0` (unnamed in the bundle) |

The response is the shared gateway envelope. `sendChatChecked` returns no data,
so the CLI requires an explicit `code: 0`, like other void mutations.

## When Chrome sends it

- **Open chat view.** When the window has focus, Chrome picks the newest loaded
  message that is newer than the box's `lastSeenMessageId`, is not from the
  signed-in account (`$O`: `from` equals own MID), and is not marked
  `SKIP_BADGE_COUNT: "true"` (`JO`). It sets the local unread count to `0` and
  calls `sendChatChecked` once for that message. If no message qualifies, it
  sends nothing.
- **Chat list menu "mark as read".** It sets the local unread count to `0`.
  Only if the box's last message exists and is not from the signed-in account
  does it call `sendChatChecked` with that message ID.

Chrome never sends a read receipt for the account's own message.

## Operations

| Operation | Handler | Effect |
| --- | --- | --- |
| 40 `SEND_CHAT_CHECKED` | `RS` | `param1` chat MID, `param2` message ID: set unread to `0` and `lastSeenMessageId` (your other sessions). |
| 55 `NOTIFIED_READ_MESSAGE` | `yS` | `param1` chat MID, `param2` reader MID, `param3` last read message ID: update read ranges (other members). |
| 91 `RECEIVE_READ_WATERMARK` | — | Defined; not used by the CLI. |

## CLI interpretation

- Only `line read` sends `sendChatChecked`. History, watch, download, members,
  chats, and pickers never do.
- The CLI reserves and persists a request sequence before the single attempt,
  matching Chrome's nonzero `HO()` sequence. It never retries; an uncertain
  outcome is reported as `unknown`.
- Without `--message`, the CLI reads the latest 50 messages (Chrome's usual
  page) and picks the message with the largest numeric ID that is not from
  you and not `SKIP_BADGE_COUNT`. Messages are not decrypted and no keys are
  fetched. If none qualifies, nothing is sent.
- With `--message`, the CLI verifies the ID in that chat using the bounded
  action lookup (10 pages, 1000 records, 30 seconds) and refuses your own
  messages, matching Chrome.
- The CLI does not know the box's `lastSeenMessageId`, so it may send a receipt
  for a message that is already read. It does not compare IDs against it.
