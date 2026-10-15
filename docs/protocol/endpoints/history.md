# Older message history contract

## Evidence

Inspected LINE Chrome extension **3.7.2** on 2026-10-15, matching the repository
application headers and signing runtime. Downloaded the public package from
Google's extension update service for extension
`ophjlpahpchlmihnnnihgmmeilfjmjjc` ([official listing](https://chromewebstore.google.com/detail/line/ophjlpahpchlmihnnnihgmmeilfjmjjc)).
SHA-256: `3722ab02edb0aba49c229399ee8df5b4b5a38c60b76efadadfbae395015eb859`.
This matches the package used for the media-download investigation. No installed
capture profile was available; no login, account reads, captures, or live
mutations were performed. This is static implementation evidence, not a live
interoperability or server-retention guarantee. The extension remains
LY Corporation's copyrighted software; its source bundle is not committed.

Locators below are minified symbols in this package's `static/js/main.js`:

| Locator | Behavior |
| --- | --- |
| `SD` | POST `/api/` plus method path; JSON array of positional arguments, excluding the initial HTTP options object |
| `Bj`, `jC` | `talk/thrift/Talk/TalkService/getPreviousMessagesV2WithRequest`; request object then sync reason |
| `DU.UNKNOWN` | Numeric sync reason `1`, used by the older-history scroll handler |
| `DC`, `Dj` | Initial history uses `getRecentMessagesV2` with chat ID and count |
| `jC` | Request fields: `messageBoxId`, `endMessageId: {deliveredTime, messageId}`, `messagesCount`; removes any response message matching the anchor ID |
| `PC` reducers | Appends older results to the existing newest-first list; `hasPrev` becomes false only when the filtered result is empty |
| Scroll handler calling `XC.fetchPrevMessageList` | Uses the last item in the current list as the next anchor |

The decoded gateway payload is an array of messages. Shared RPC handling unwraps
the gateway's `code`/`data` response. Pagination does not call read-receipt or
key-registration methods. Chrome caps its page size at its configured message
limit (default 100) and normally requests 50. CLI requests are capped at 100.
Chrome can substitute created time when delivered time is absent; this CLI
intentionally refuses such a cursor because those fields have different meanings.

## CLI interpretation

The request is `[request, 1]`. Message IDs are JSON strings; delivered time is
retained as an exact integer, never converted through floating point. A cursor
contains version 1, selected chat ID, message ID, and delivered time as strings,
encoded as unpadded URL-safe base64 JSON. It is a navigation token, not a secret
or a proof of message ownership. Each response must independently match the chat.

Inclusive anchors are filtered. An empty page, or one containing only the anchor,
means end of *available* history, following Chrome. A short nonempty page is not
proof of exhaustion. Older data may have expired or otherwise be unavailable;
no fixed server-retention period or exhaustive account history is promised.
Malformed and nonadvancing pages fail instead of silently claiming exhaustion.

The extension registers a `getMessagesByIds` service name but the inspected
bundle discards that callable and provides no request construction or supported
lookup flow. That is insufficient evidence for a direct lookup API. Shared CLI
lookup therefore traverses bounded history pages.

Supplementary evidence: the pinned
[linejs fetcher](https://github.com/evex-dev/linejs/blob/4fc512ab8c19a2490ca73bb71777beb775ca3ae3/packages/linejs/client/features/chat/fetcher.ts)
uses each page's last delivered time and lossless message ID as its next anchor.
Its binary Thrift transport is not evidence for the Chrome JSON argument shape.
