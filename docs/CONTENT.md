# Detailed conversation JSON

Use explicit detailed output when a script needs decoded message content or
resolved chat information. Existing `messages --json`, `messages --page --json`,
`chats --json`, and watch NDJSON retain their JSON shapes and ordering.

```sh
line chats --details --json
line chats --details --json --search Family
line messages CHAT_ID --details --json
line messages CHAT_ID --details --json --resolve-names
line messages CHAT_ID --details --page --json --limit 50
line messages CHAT_ID --details --page --json --cursor 'NEXT_CURSOR'
```

`--details` requires `--json`. `--resolve-names` is available on `messages` and
requires `--details --json`; it batches unique sender IDs, at most 100 per read.
These validations run before session access. The command timeout includes name
resolution. Missing profiles and remote name-lookup failures produce explicit
unavailable names. Cancellation, local storage failures, and invalidated sessions
remain errors. Reads never mark messages read, register group keys, download
objects, or execute/follow links.

## Chat envelope

`chats --details --json` returns `{"schema_version":1,"chats":[...]}`. Rows have:

| Field | Meaning |
| --- | --- |
| `id` | Chat MID, always a string |
| `type` | LINE chat type |
| `name` | Effective display name, or `null` |
| `name_status` | `resolved` or `unavailable` |
| `updated_at` | Last known activity in epoch milliseconds, or `null` |
| `unread_count` | Reported unread count, or `null` when omitted by LINE |

Rows sort by activity, newest first. Like existing JSON mode, detailed JSON
includes all chats and has no default row limit. `--limit` and `--search` retain
their usual meanings. A name-search lookup failure remains fatal because a
partial name search cannot establish which chats match. Missing activity is not
proof a chat is inactive; a missing unread count is not zero.

## Message envelope

Detailed history returns `schema_version: 1`, `chat`, `order`, and `messages`.
`chat` contains `id`, nullable `name`, and `name_status`. Default detailed history
retains server ordering (`order: "server"`). With `--page`, order is
`newest_first`, and `next_cursor`, `exhausted`, and `stop_reason` follow the
[history pagination contract](CLI.md#read-messages). The cursor is interchangeable
with ordinary page mode. The detailed envelope uses `chat.id` instead of the
ordinary page envelope's `chat_id`.

Each message retains string `id`, `from`, `to`, `created_time`, numeric
`content_type`, `encrypted`, optional `reply_to`/`reactions`, and adds:

| Field | Meaning |
| --- | --- |
| `chat_id` | Selected conversation MID; incoming direct messages use the peer, not your own MID |
| `sender` | `id`, nullable `name`, `name_status` (`not_requested`, `resolved`, or `unavailable`) |
| `content` | Safe typed projection described below |
| `status` | Same value as `content.status` |
| `text` | Decoded text, empty for content without text |
| `file_name` | Only present when a filename was recovered; prefer `content.attachment.name` for explicit unknowns |

Content status is `ok`, `unsupported`, `malformed_metadata`, or
`decryption_failed`. Unsupported content is not an empty text message.
Malformed metadata may retain other independently valid fields. Rich traversal
limits use `rich.truncated`; oversized input is malformed. A decryption failure
never falls back to an outer filename, text, or location. Partial decryption
failures still write the envelope and return a nonzero exit, including for
attachments in detailed mode. Malformed metadata and unsupported types are
reported per message without making the command fail. Legacy mode keeps its
previous status/partial-error contract. Attachment encryption/storage markers
also set `encrypted: true` when chunks are missing, preventing a plaintext
detailed projection of damaged encrypted metadata.

## Allowed content fields

`content.kind` is `text`, `location`, `image`, `video`, `audio`, `file`, `contact`,
`sticker`, `flex`, `rich`, or `unsupported`. A location may arrive as LINE type
0 or 15. Applicable content fields are:

| Field | Contents |
| --- | --- |
| `text` | Decrypted or plaintext text |
| `mentions` | Entries with inclusive `start`, exclusive `end` in UTF-16 code units, and either `mid` or `all:true`; incoming metadata only |
| `location` | `title`, `address`, `latitude`, `longitude`; missing/invalid coordinates are malformed |
| `attachment` | Category `type`, nullable `name`, nullable `size_bytes`, nullable `duration_ms` (audio/video); MIME type is not guessed |
| `contact` | `mid`, `display_name` |
| `sticker` | String `package_id`, string `id`, optional `text` |
| `rich` | Optional `alt_text`, `text`/`links` arrays, `truncated` |

FLEX extraction visits known layout containers, text/span nodes, and URI action
labels/links. Only HTTP(S) links without embedded credentials are included.
It does not serialize image URLs, postback data, arbitrary JSON, HTML, or scripts.
RICH uses the same explicit metadata only when available; no general RICH
layout decoder is claimed. Input is capped at 64 KiB per decoded payload/rich
field, with rich traversal limited to 16 levels and 256 nodes and extracted
text/links limited to 8192 bytes and 100 entries each. Mentions are capped at 100
validated, nonoverlapping spans.

Encrypted text, location, and filenames come from authenticated Letter Sealing
payloads. Allowed outer metadata such as mention spans, file size, and duration
is not authenticated by that payload. No chunks, keys, unrestricted metadata,
object addressing, or raw server errors are exported.

Human history and message choosers use the same decoded model for location,
contact, sticker, rich summaries, and recovered filenames. Watch uses that
same decoder internally with its existing JSON projection; this adds no watch
flags or schema fields. Detailed watch output is outside this milestone.

See [protocol evidence](protocol/endpoints/message-content.md) for source
provenance and deliberate limits. Validation uses synthetic fixtures; live
LINE interoperability has not been tested.
