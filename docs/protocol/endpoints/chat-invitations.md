# Group invitations

## Evidence and scope

Inspected the public LINE Chrome extension **3.7.2** on 2026-11-07, matching
`pkg/line/client.go` `ExtensionVersion`. Same package and provenance as the
[read receipt investigation](read-receipts.md): downloaded from Google's Chrome
extension update service for `ophjlpahpchlmihnnnihgmmeilfjmjjc`, SHA-256
`3722ab02edb0aba49c229399ee8df5b4b5a38c60b76efadadfbae395015eb859`.

Static source evidence only. No login, capture, account read, or live
invitation change was performed. The extension remains LY Corporation's
copyrighted software; its source is not committed. Locators are minified
symbols in `static/js/main.js` and may change between releases.

## Listing pending invitations

| Locator | Behavior |
| --- | --- |
| `iU` | `talk/thrift/Talk/TalkService/getAllChatMids` |
| `CA` (`talkChat/fetchAllTalkChats`) | Calls `getAllChatMids` with request `{withMemberChats: true, withInvitedChats: true}` and a sync reason; reads `memberChatMids` and `invitedChatMids` from the response. `invitedChatMids` becomes the invited list. |
| `oU` | `talk/thrift/Talk/TalkService/getChats` |
| `_A` (`talkChat/fetchTalkChats`) | Calls `getChats` with `{chatMids, withMembers: true, withInvitees: true}` in batches of the `limit.sync.groups` configuration (default 100). A chat whose `extra.groupExtra.memberMids` contains your MID is joined; one whose `inviteeMids` contains your MID is invited. |

`getChats` returns the group name for invited chats. It does not name the
inviter: Chrome learns the inviter only from operation 124
`NOTIFIED_INVITE_INTO_CHAT` (`param1` chat, `param2` inviter, `param3`
invitees) while it is running, and keeps it in memory.

## Accept and reject

| Locator | Method | Arguments |
| --- | --- | --- |
| `aU` | `acceptChatInvitation` | One request struct: `{reqSeq: HO(), chatMid}` |
| `rU` | `rejectChatInvitation` | One request struct: `{reqSeq: HO(), chatMid}` |

`HO()` is the same nonzero request-sequence counter used for other mutations
(see [read receipts](read-receipts.md#request)). The struct form differs from
`sendChatChecked`, which uses positional scalars. Responses use the shared
gateway envelope; the CLI requires an explicit `code: 0`.

Accepting has no follow-up in Chrome's call sites: no `registerE2EEGroupKey`,
no group key fetch, and no read receipt. Operation 129
`ACCEPT_CHAT_INVITATION` (handler `YA`) moves the chat from invited to joined in
local state; operation 131 `REJECT_CHAT_INVITATION` (handler `NS`) removes it.

## CLI interpretation

- `line invites` calls `getAllChatMids` with `withInvitedChats: true` and looks
  up names with `getChats` (batches of 20, the CLI's validated group lookup
  size). A name lookup failure leaves names unavailable rather than hiding
  invitations. The inviter is not reported, because no request returns it.
- Accept and reject take a full group ID (or a guided choice) and confirm it is
  in the current pending list before reserving a request sequence. Each is
  attempted once with the persisted sequence and never retried.
- Accepting never registers or fetches a group key. A later explicit
  `line send` may register one, with complete membership, as before.
- The existing Go `getAllChatMids` sends sync reason `2`, as the CLI's other
  `getChats` reads do; Chrome's full sync uses reason `4`. The CLI keeps `2`.
