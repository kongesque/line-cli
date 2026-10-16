# Group membership and outgoing mentions

Evidence inspected 2026-10-16: official public LINE Chrome extension **3.7.2**,
matching the CLI headers and signing runtime. The package SHA-256 was verified as
`3722ab02edb0aba49c229399ee8df5b4b5a38c60b76efadadfbae395015eb859`.
The package and extracted source were reused from the history investigation in
`/tmp`; neither is committed. LY Corporation retains copyright in that source.
No installed capture profile was found. No login, account capture, live read,
or mutation was performed. Static source and synthetic tests do not establish
live interoperability.

## Evidence contract

Resolve the request flags and membership completeness for the group member
list, and the metadata construction/placement for text mentions (including
Letter Sealing and replies). Existing public source answers these questions;
no new traffic capture is necessary. Locators refer to `static/js/main.js` in
the exact package above.

| Behavior | Source locator | Implementation |
| --- | --- | --- |
| Group lookup | `oU=SD("talk/thrift/Talk/TalkService/getChats")`; `_A`, `talkChat/fetchTalkChats` | Existing TalkService `getChats`, request with `chatMids`, `withMembers:true`, `withInvitees:true`, existing sync reason 2; wrapper `data.chats` |
| Member identity | `kT`, `OT`, `_A` | Keys of `extra.groupExtra.memberMids` are current members, including self when returned; keys of `inviteeMids` are separate invitees. Values are join/invitation timestamps, not booleans determining inclusion |
| Completeness | `_A` uses requested member maps as the full snapshot; no per-chat member cursor | Present maps, including `{}`, are complete for this response. Missing/null maps are explicitly incomplete. This does not assert freshness beyond the response or available profiles |
| Legacy rooms | `vA` / `room/fetchRooms`, `kT` | Separate room lookup and self handling. The new group membership command rejects legacy room IDs instead of guessing that contract |
| Mention construction | `II`, `aP`, editor `onChoose` mention block | `MENTION` is a JSON string containing `MENTIONEES`; string `S`/`E` are JavaScript UTF-16 indices, exclusive end. `M` is a user MID; explicit mention-all uses `A:"1"` |
| Encrypted mention | `gL`, `EL`, `yL` | Encrypt the ordinary text body; retain `MENTION` in outer `contentMetadata`. Mention metadata is not authenticated as part of the encrypted text |
| Incoming mention | `tP`, `nP`, `Gk` | Shared incoming decoder validates metadata against the decoded text |

The CLI uses sorted MID lists and does not invent a self entry if the server
omits it. It does not treat invitees as current members or use this read command
to register keys. An omitted list is rendered as incomplete, never a complete
empty group. Missing/mismatched/duplicate chat responses and malformed lists
fail safely. Existing flexible object/array map decoding is retained; malformed
shapes now return an error rather than silently disappearing.

Outgoing mention input uses the safe decoded field names `start`, `end`, `mid`,
and `all`. The CLI requires explicit bounds, full supported user MIDs, at most
100 spans, no overlap, and boundaries that do not split a surrogate pair. These
are conservative CLI validation limits, not claimed server limits. Text is
never rewritten or scanned for implicit mentions. Mentions need explicit text
or stdin; files are unsupported. Mention-all is rejected for direct chats.
Ordinary reply validation, capability checks, persisted sequences, and the
single-attempt mutation rule apply unchanged. The CLI does not adopt Chrome's
transport/mutation retry behavior.
