# Structured errors and mutation outcomes

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

## Schema 1

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

## Interpret each remote step separately

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

A plaintext file send creates the message **before** uploading bytes. If that
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

Native image sends record full encrypted upload (`attachment_upload`), encrypted
preview (`preview_upload`), and `send` separately. An upload may leave an object
without creating a message. Plain image upload follows message creation, so its
failure retains the confirmed message receipt just like a generic-file upload.
Never infer that an upload or send can safely be retried from a transport error.

Send policy errors: `encryption_required` means Letter Sealing is unavailable
and the command did not permit plaintext. `encrypted_image_unavailable` means
native encrypted image sending is disabled until a secure compatible preview
format is established. Neither error authorizes an automatic resend or downgrade.
