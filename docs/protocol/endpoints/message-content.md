# Safe decoded message content

Evidence inspected 2026-10-16: the public official LINE Chrome extension **3.7.2**
package already used for [history](history.md) and [media downloads](media-download.md).
Its manifest and CLI headers/runtime agree. SHA-256:
`3722ab02edb0aba49c229399ee8df5b4b5a38c60b76efadadfbae395015eb859`.
No installed capture profile was present. No live account, login, capture, or
mutation was needed. This is static-source evidence plus synthetic tests, not
live compatibility verification. The extension is LY Corporation's copyrighted
software; its source/package is not committed.

Locators below refer to `static/js/main.js` in that exact package.

| Content | Source evidence | CLI projection |
| --- | --- | --- |
| Encrypted body | `gL` packages `text`, `location`, `REPLACE`, and, for media, `keyMaterial`/`fileName`; `yL` restores them after decryption | Only text, location, and filename are read from authenticated plaintext; key material and replacement internals are not exported |
| Mentions | `II`, `tP`, `nP`, `Gk`: outer `MENTION` JSON with `MENTIONEES`, string `S`/`E`, `M` or `A:"1"`; `EL` leaves MENTION outside the encrypted payload | UTF-16 offsets and MID/all flag; validate bounds, surrogate boundaries, overlap, and bounded count |
| Location | `gL`/`yL`, `EU.LOCATION=15`, message `location` | Title, address, latitude, longitude; also handle a location on content type 0 |
| Files/media | `sP`: `FILE_SIZE`, `FILE_NAME`, `DURATION`; `Ok` derives duration from media seconds times 1000 | Nullable filename, size in bytes, duration in milliseconds, and content category; never invent a MIME type or derive a filename from the storage object |
| Shared contact | `oP`: outer `mid`, `displayName` | MID and display name only |
| Sticker | `iP`: `STKPKGID`, `STKID`, `STKTXT` | String package/item IDs and optional text |
| FLEX | Unique `FLEX_JSON` reader and `EU.FLEX` previews using `ALT_TEXT`; action handler identifies `type:"uri"`/`uri` | Bounded visible text, labels, and HTTP(S) action links; no resource fetches or action execution |
| RICH | `EU.RICH=17` is declared but there is no corresponding renderer in this package | Best-effort extraction only when the same explicit `ALT_TEXT`/`FLEX_JSON` fields exist; otherwise unsupported. No invented RICH payload format |

RICH fallback is a conservative CLI projection, also observed in the local
comparison implementation `enil` (`line.ts`), not proof of a Chrome RICH rendering
contract. Full rich layout interpretation and arbitrary HTML are unsupported.

Detailed output uses an allowlist, never generic metadata serialization. Input
JSON fields are capped at 64 KiB. Rich traversal visits only known layout children
and actions, at most 16 nested levels and 256 nodes, retaining at most 100 text
entries, 100 links, and 8192 bytes of extracted text/links. `truncated` reports
traversal/output limits. Oversized or malformed input reports `malformed_metadata`.
Names and ordinary content strings have a 64 KiB decoding bound. Mention lists
are limited to 100 entries. These are local resource limits, not server limits.

Encrypted filename, text, and location never fall back to unauthenticated outer
values in the detailed projection. Other allowed outer metadata (mentions,
size/duration, contact/sticker/rich fields) is not claimed to be authenticated by
Letter Sealing. Missing optional fields stay unknown. Reads use existing key
lookups and never register group keys, download objects, or send read receipts.
Legacy history/watch JSON is kept as a separate projection of the same decoder.
