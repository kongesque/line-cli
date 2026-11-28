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
| Post notification (Notes & Albums) | Content type 16 (`POSTNOTIFICATION`) and plaintext content type 0 with `ORGCONTP="POSTNOTIFICATION"` (`beeper/line` PRs #225/#226/#361/#367 and `evex-dev/linejs` enum): `serviceType` (`GB` Note, `AB` Album), `albumName`, `text`, `postEndUrl`, `chatId`, `previewMedias` | Allowlisted `post` object (`type`, `title`, `text`, `url`, `album_id`, and `preview_count` on Albums); private `mediaOid`/`chatId`/`svc`/`sid` stay internal |
| FLEX | Unique `FLEX_JSON` reader and `EU.FLEX` previews using `ALT_TEXT`; action handler identifies `type:"uri"`/`uri` | Bounded visible text, labels, and HTTP(S) action links; no resource fetches or action execution |
| RICH | `EU.RICH=17` is declared but there is no corresponding renderer in this package | Best-effort extraction only when the same explicit `ALT_TEXT`/`FLEX_JSON` fields exist; otherwise unsupported. No invented RICH payload format |

RICH fallback is a conservative CLI projection, also observed in the local
comparison implementation `enil` (`line.ts`), not proof of a Chrome RICH rendering
contract. Full rich layout interpretation and arbitrary HTML are unsupported.

### Post notifications (`POSTNOTIFICATION`, type 16)

Post-notification metadata provenance comes from upstream `beeper/line` (commits
`46b92d1`, `67e653a`, `207a4783`, `912dc6de`) and `evex-dev/linejs` type definitions,
verified locally with synthetic fixtures (live LINE Chrome interoperability has not
been verified).

- **Detection and E2EE boundaries:** Recognized only on native `contentType == 16`
  or plaintext `contentType == 0` with `location == nil` and exact
  `ORGCONTP == "POSTNOTIFICATION"`. `ORGCONTP` is ignored on all other content
  types. Encrypted type 0 messages (carrying `chunks` or `e2eeVersion`) always
  use the authenticated text/location decoder and never fall back to outer post
  metadata. Encrypted native type 16 messages report `kind: "post_notification"`,
  `status: "unsupported"` without exposing outer fields.
- **URL and `album_id` allowlist:** `postEndUrl` is treated as inert text and is
  never opened or fetched by history, watch, or download. Only `https://line.me/...`
  and `line://group/...` / `line://home/...` post/album navigation routes with
  recognized query parameters (`postId`, `homeId`, `groupId`, `albumId`,
  `albumIdV2`, `noteEntryType`, `source`, `type`) are emitted. Well-formed LINE URLs with an
  unrecognized route or unknown query parameter omit `url` with `status: "ok"`.
  Malformed, relative, plain `http://`, port/userinfo, control/whitespace, or
  unsafe-scheme URLs set `status: "malformed_metadata"` and omit `url`/`album_id`.
  `album_id` (`albumIdV2` preferred over `albumId`, max 256 bytes, header-safe) is
  extracted only for Albums (`serviceType == "AB"`) on accepted album routes.
- **Bounds and `previewMedias`:** Every consumed field is checked against the
  64 KiB and valid-UTF-8 limits before trimming or parsing; album-only fields
  (`albumName`, `chatId`, `previewMedias`) are ignored on Notes. `previewMedias`
  is capped at 64 entries and keeps only deduplicated `svc: "album"`, `sid: "a"`,
  `mediaType: "I"` items with safe OIDs (<= 256 bytes, no path separators, `..`,
  whitespace, or controls); top-level `mediaOid` fallback is deliberately not
  used. `preview_count` is emitted on Albums only and counts eligible previews
  bound to the selected chat: either exact `chatId == selectedChat` (with no
  conflicting `postEndUrl` `homeId`), or on modern V2 opaque chats
  (`[uUcCrR]<43 base64url>`) a same-type legacy V1 hex `chatId` (`[ucr]<32 hex>`)
  paired with an accepted album `postEndUrl` whose `homeId` equals `selectedChat`.
  Full Note body retrieval is out of scope.

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
