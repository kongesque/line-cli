# JSON output and errors

Use this reference when writing scripts. For commands and setup, see the
[CLI guide](CLI.md#use-from-scripts). Success and partial results go to stdout;
diagnostics go to stderr. Use full chat IDs and keep message IDs as strings.

[Output modes](#output-modes) · [Detailed content](#detailed-content) ·
[Errors](#errors) · [Mutation outcomes](#interpret-each-remote-step-separately)

## Output modes

| Invocation | stdout format |
| --- | --- |
| `line messages CHAT_ID --json` | Legacy message array in server order. |
| `line messages CHAT_ID --page --json` | Schema-1 page object with `chat_id`, newest-first messages, continuation cursor, and exhaustion status. |
| `line messages CHAT_ID --details --json` | Schema-1 envelope with `chat`, safe decoded content, and server order. Add `--page` for a newest-first detailed page. |
| `line chats --json` | Legacy chat array. |
| `line chats --details --json` | Schema-1 `chats` envelope with explicit unknown values. |
| `line watch --json` | Existing NDJSON stream; no detailed-content envelope. |
| `line --error-format=json COMMAND ... --json` | Usual success/partial output on stdout; one structured error on stderr when the supported command fails. |

`--error-format=json` does not imply `--json`. It disables prompts, as do `--json`
and `--stdin`. Sends prefer Letter Sealing and automatically use HTTPS/TLS only
when LINE explicitly reports no E2EE support. Add `--require-e2ee` to block that
mode before remote actions. A nonzero exit can accompany valid
partial JSON or a confirmed remote action; inspect statuses and mutation outcomes
before deciding what to do next.

In send results, `encrypted` reports Letter Sealing: `false` means no E2EE, while
HTTPS/TLS still encrypts the transfer. LINE can read non-E2EE content.

Pagination and cursors follow the [history guide](CLI.md#history-pagination-json).
Other command fields and exit codes are listed in the
[CLI reference](CLI.md#json-output-and-exit-codes).

## History diagnostics

`messages CHAT_ID --diagnostics --json` inspects recent-history IDs before
decryption, without message content or key lookups. It requires `--json` and
cannot combine with `--details`, `--resolve-names`, `--page`, or `--cursor`.
The result includes `schema_version: 1`, `chat_id`, `method: "getRecentMessagesV2"`,
`requested_count`, `returned_count`, `null_count`, `invalid_id_count`, and
`message_ids` in server order. IDs stay strings; malformed IDs are counted
instead of printed. This is one logical read; authentication recovery may retry it.
A missing ID does not prove failed delivery or a search of all available history.

## Detailed content

Existing legacy message/chat JSON and watch NDJSON retain their shapes and order.
Use the explicit detailed mode for normalized content and unavailable values.

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

### Chat envelope

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

### Message envelope

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

### Allowed content fields

`content.kind` is `text`, `location`, `image`, `video`, `audio`, `file`, `contact`,
`sticker`, `flex`, `rich`, or `unsupported`. A location may arrive as LINE type
0 or 15. Applicable content fields are:

| Field | Contents |
| --- | --- |
| `text` | Decrypted or plaintext text |
| `mentions` | Entries with inclusive `start`, exclusive `end` in UTF-16 code units, and either `mid` or `all:true`; incoming metadata; the same field names are accepted by `send --mentions` |
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

## Errors

Use the global option **before the command**:

```sh
line --error-format=json send CHAT_ID --stdin --json < message.txt
line --error-format=json messages CHAT_ID --json > history.json 2> error.json
line --error-format=json auth status --json
```

`--error-format json` is equivalent. The default is `text`. This option supports
`whoami`, `contacts`, `chats`, `members`, `messages`, `send`, `download`, `react`, `unsend`,
`auth status`, help, and version. Login, logout, migration, watch, and update keep
their existing diagnostic contracts; requesting JSON errors for them fails
locally before doing work. An invalid error-format option itself uses text.

JSON error mode disables guided prompts and requires explicit command arguments.
It does not imply `--json`: use both options for JSON success output. A failed
command writes one JSON object followed by a newline to stderr, without the
flag parser's human diagnostics. Success and help retain their usual output.
JSON error mode adds no wrapper to stdout. Partial legacy history and contact
results remain arrays and still exit nonzero. Explicit `messages --page --json`
uses its page object on stdout, including for partial decryption failures.

### Error schema 1

An uncertain send can produce this synthetic example:

```json
{
  "schema": 1,
  "error": {
    "code": "remote_failed",
    "stage": "send",
    "message": "LINE request failed; check connectivity and account settings. Delivery is unknown; inspect LINE before any retry.",
    "mutation": {
      "action": "send",
      "chat_id": "u-example",
      "request_sequence": 42,
      "attempted": true,
      "outcome": "unknown",
      "steps": [{"stage": "send", "attempted": true, "outcome": "unknown"}]
    }
  },
  "exit_code": 1
}
```

Branch on `schema`, `code`, and outcome fields, not the human `message`.
Unknown codes/fields should be handled conservatively. `chat_id` and `message_id`
are strings, including IDs above JavaScript's safe integer range. Optional IDs
and sequence are omitted when unavailable. The persisted request sequence is
within LINE's signed 32-bit range and remains a JSON number. This report is not
an operation ledger and does not supply a deduplication or replay API.

Request failures may additionally include `error.diagnostic`. Its `kind`
distinguishes `transport` (HTTP exchange failed), `response_read` (body could
not be read completely), `gateway` (HTTP/application error),
`response_envelope` (invalid JSON or missing/invalid success code),
`message_decode` (message data could not be decoded), and `missing_receipt`
(send returned no message or no ID). Gateway diagnostics include `http_status`
and, when available, numeric `gateway_code`. Only the known exception names
`TalkException` and `TokenAuthException` and their numeric `exception_code` are
exposed. Server reason strings, body contents, and transport error text are
never included. Other failures may omit diagnostics.

These details do not change outcome classification or authorize retries. A
gateway failure or a parsing failure can still follow a delivered message.
For missing-history investigations, `messages CHAT --diagnostics --json`
reports returned IDs before decryption without message text or encrypted data.

| Code | Meaning |
| --- | --- |
| `invalid_arguments` | Invalid or missing options/input; validation stopped the command. |
| `no_session` | No saved session. |
| `invalid_session` | Invalidated session or unusable saved Letter Sealing keys. |
| `session_changed` | Saved account changed during the command. |
| `busy` | Another command holds the session lock. |
| `authentication_failed` | Authentication failed; inspect the mutation outcome independently. |
| `invalid_cursor` | Malformed/unsupported history cursor or cursor bound to another chat. |
| `history_invalid` | History has invalid identity/order/data or no valid delivered-time continuation anchor. |
| `message_not_found` | Target absent from available history in the selected chat; older history may be unavailable. |
| `lookup_incomplete` | The 10-page lookup budget was reached before finding the target. |
| `unsupported_content` | Requested download content is unsupported. |
| `decryption_failed` | History contains failed decryptions, or an attachment could not be decrypted/authenticated. |
| `encryption_failed` | Message encryption/key preparation failed. |
| `encryption_required` | Explicit non-E2EE capability blocked by strict mode; no message action attempted. |
| `encrypted_image_unavailable` | Encrypted native images disabled; no upload, group-key registration, or send attempted. |
| `remote_failed` | Remote request failed or a send receipt was missing. |
| `cancelled` | Cancellation, including a signal. |
| `deadline_exceeded` | Command timeout or the 30-second lookup budget expired; stage `lookup` identifies lookup. |
| `input_failed` | A local input could not be read. |
| `output_failed` | Command output could not be written; a mutation may already be confirmed. |
| `storage_<reason>` | Classified storage failure; suffix matches `auth status`'s storage reason. |
| `operation_failed` | Other local/preparation failure; do not infer a retry policy. |

Stages identify where work stopped: `validation`, `lock`, `storage`,
`preparation`, `lookup`, `decode`, `persist_sequence`, `group_key_registration`,
`attachment_upload`, `preview_upload`, `send`, `react`, `remove_reaction`, `unsend`, `output`, or
`completion` (a signal after the command completed).

Exit codes are unchanged: ordinary errors are 1, storage errors retain
[their dedicated codes](CLI.md#storage-exit-codes), SIGINT is 130, and SIGTERM is
143. The same code appears in `exit_code`. Cancellation after confirmed delivery
can still exit 130/143. Errors retain their wrapped causes internally so storage
recovery errors keep their exit status even after a remote mutation attempt.
Tokens, encrypted chunks, keys, raw protocol bodies, arbitrary wrapped error
strings, and input message text are excluded from machine errors.

### Interpret each remote step separately

The top-level `mutation` describes the requested message action (`send`, `react`,
`remove_reaction`, or `unsend`). `steps` describes the ordered mutation stages,
including preparation side effects. Reads and credential recovery are not listed
as message mutation steps. `attempted` means the command handed the operation to
the mutation API; it does not prove a packet reached LINE.

| Outcome | Meaning and handling |
| --- | --- |
| `not_attempted` | The message action/stage was not handed to its mutation API. Inspect earlier steps before deciding what to do next. |
| `confirmed` | A success response was received; sends also require a returned message ID. Do not repeat this action. |
| `rejected` | An authoritative authentication rejection was received. Inspect earlier steps and the error code; the CLI never replays the action. |
| `unknown` | No authoritative result: a lost response, malformed/missing receipt, or an unclassified service failure. Inspect LINE before deciding whether to retry. |

Rejection classification is deliberately narrow: typed HTTP 401/403, or the
existing recognized authentication envelope codes (119/10004, TokenAuthException
3/4, TalkException 8, and TalkException 83 with `invalid sender key`). Generic
5xx responses, timeout-like errors, other service exceptions, and text that
merely resembles an authentication error remain unknown. Void mutation responses
must contain an explicit `code: 0`; HTTP 200 alone is insufficient.

A send can fail **before message dispatch** after registering a group key or
uploading an encrypted file. Its message outcome is `not_attempted`, while the
preparation steps may be `confirmed` or `unknown`. Do not equate this with zero
remote effects.

A non-E2EE file send creates the message **before** uploading bytes. If that
upload fails, the report retains `mutation.outcome: "confirmed"`, the created
`message_id`, chat ID, and request sequence. Its `attachment_upload` step records
whether the upload was not attempted, rejected, or uncertain. A confirmed message
here does not mean the attachment is usable.

If a message action succeeds but stdout fails, `code` is `output_failed`, `stage`
is `output`, and the confirmed action, message ID, chat ID, and sequence remain
available. Do not send again just because stdout was empty or truncated. If
stderr itself cannot be written, no JSON diagnostic can be guaranteed; the
nonzero exit still applies. Broken stdout pipes return a write error instead of
terminating immediately via SIGPIPE, allowing stderr to retain the receipt.
No mutation is retried for reporting failures.

These contracts are covered with synthetic APIs and transports. They do not
constitute a new live LINE compatibility validation.

Encrypted native images currently stop at `preparation` with
`encrypted_image_unavailable`, before upload, group-key registration, or send.
The outcome schema retains `preview_upload` for compatibility; its presence in
the schema does not mean encrypted image sending is enabled. Non-E2EE image upload
follows message creation, so its failure retains the confirmed message receipt
just like a generic-file upload. Never infer safe retries from a transport error.

Send policy errors: `encryption_required` means Letter Sealing is unavailable
and strict mode (`--require-e2ee`) blocked
the send before remote actions. There is no encryption confirmation prompt in any
output mode. `encrypted_image_unavailable` is the image restriction described
above. Neither error permits an automatic resend or downgrade.
