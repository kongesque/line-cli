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

## Send failure and history diagnostics (issue #20)

### Evidence

Inspected the official public LINE Chrome extension **3.7.2** on 2026-10-17,
matching the CLI version constants. The package was downloaded from Google's
extension update service for `ophjlpahpchlmihnnnihgmmeilfjmjjc` and verified as
SHA-256 `3722ab02edb0aba49c229399ee8df5b4b5a38c60b76efadadfbae395015eb859`.
LY Corporation retains copyright in the extension. Its source is not committed.
No Chrome login, session replacement, or browser traffic capture was performed.

This investigation distinguishes transport/response failures from service
exceptions, checks whether recent-history records disappear during decoding,
and corrects group sends to honor the negotiated encryption version. Mutation
retries remain disabled.

Locators refer to `static/js/main.js` in that exact package:

| Locator | Observed behavior |
| --- | --- |
| `mU.E2EE_RETRY_ENCRYPT` | Talk error enum value **82**. This is distinct from sender-key update (83), receiver-key update (84), and group-key recreation (99). |
| Send-error switch using `mU.E2EE_RETRY_ENCRYPT` | When E2EE is enabled and content is encryptable, Chrome resets chat negotiation and retries its send flow. This CLI deliberately does not copy mutation retries. |
| `LA.negotiate`, `renewLatestGroupKey`, `unwrapGroupKey` | Group negotiation reads `getLastE2EEGroupSharedKey(1, chat)` and retains the returned `allowedTypes` and `specVersion`. |
| `encryptGroupMessage`, `encryptWithE2eeChannel`, `EL` | Chrome uses negotiated encryption version, constructs chunks, removes outer text/location, and sets string `contentMetadata.e2eeVersion`. |
| `MP`, `Rj` | `sendMessage` takes the request sequence and the prepared message. |

### Authorized live observations

Two single-attempt sends were performed in the user-approved two-member test
group: ordinary text and an explicit mention. Both returned HTTP **400**,
gateway code **10051**, `TalkException` code **82**. Their outcomes remain
`unknown`; neither was retried. The planned reply to the baseline was skipped
because the baseline had no confirmed receipt.

Recent-history diagnostics returned three records, with no null records or
invalid IDs. Detailed history contained the same three IDs. Neither test text
appeared, and none of the returned records carried a reply relationship. This
establishes no decode-stage omission in that sample, not exhaustive history or
non-delivery of the attempted sends.

A separate read-only check found saved E2EE enabled, saved keys present, and a
current group shared-key response available (no explicit disabled capability
or missing-key response). Those facts do not establish that the encryption
envelope, key freshness, or negotiated version used at send time was correct.
The original issue's visibly delivered reply remains unexplained.

### Root cause and correction

A read-only negotiation plus locally intercepted send showed `specVersion: 1`,
but the CLI generated V2 chunks and set `e2eeVersion: "2"`. The group key's
negotiated version was discarded by the messaging layer. This explains the
reproduced code-82 failures for both ordinary text and mentions.

Group sends now select V1 or V2 explicitly from the latest key response and
label the envelope with the same version. An absent version defaults to V2,
matching Chrome's `Nd` default; unsupported versions fail before sending.
The former V2-error fallback to V1 was removed because it returned V1 chunks
under a V2 label. Cipher failures remain failures, with no plaintext downgrade
or automatic mutation retry. The same selection applies to attachment payloads.

The corrected local interception produced the V1 five-chunk framing (8-byte
salt, 16-byte MAC, two 4-byte key IDs), labeled version 1 with no outer plaintext.
Chrome locators `mL` (V1) and `vL` (V2) corroborate these framing differences.

### Live verification after the fix

One baseline, one mention, and one reply were sent using the previously approved
content, only in the authorized two-member test group. All three returned
confirmed encrypted receipts without group-key registration. Subsequent recent
history returned seven records with no null or invalid IDs. Each new receipt
appeared in both pre-decryption diagnostics and detailed history, decrypted
successfully, and matched the approved text. The decoded mention targeted the
expected other member; the reply relationship matched the confirmed baseline.
Each verification send was attempted once, without automatic retries.

This confirms the corrected group text/mention/reply flow. It does not establish
what happened to the original issue's historical reply, or live attachment/V2
interoperability; those paths have synthetic regression coverage.

No private IDs, message bodies, keys, encrypted chunks, raw responses, or
captures are retained here. See [CLI diagnostics](../../CLI.md) and
[structured error details](../../ERRORS.md) for the new safe output contract.
