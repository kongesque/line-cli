# Media download contract

## Evidence and scope

Inspected the public LINE Chrome extension **3.7.2**, matching
`pkg/line/client.go` and `pkg/runner.go`, on 2026-09-27. The package was obtained
from Google's Chrome extension update service for extension
`ophjlpahpchlmihnnnihgmmeilfjmjjc` ([official listing](https://chromewebstore.google.com/detail/line/ophjlpahpchlmihnnnihgmmeilfjmjjc)).
Package SHA-256:
`3722ab02edb0aba49c229399ee8df5b4b5a38c60b76efadadfbae395015eb859`.

This is static source evidence, not a live account test. No capture, credentials,
message contents, or third-party source bundle is included in the repository.
The source locators below refer to minified symbols in `static/js/main.js` of
this exact package; they may change between releases. The implementation and
this document describe protocol behavior; the extension remains LY Corporation's
copyrighted software.

## Addressing and full-content selection

The extension's `ZP` maps content types to encrypted storage IDs:

| Content type | Meaning | Encrypted SID |
| --- | --- | --- |
| 1 | Image | `emi` |
| 2 | Video | `emv` |
| 3 | Audio | `ema` |
| 14 | Generic file | `emf` |

`FP` recognizes these four downloadable types. `QO`/`WO` construct the OBS path:

- Plain: `/r/talk/m/{messageID}`. When `MEDIA_CONTENT_INFO.category` is
  `original`, append `/original` for the download, but not for object-info.
- Encrypted: `/r/talk/{SID}/{OID}` using message metadata.
- Preserve `OBS_POP` as the encoded `p` query parameter on both requests.
- Preview paths are distinct (`/preview` or `__ud-preview`); a download does
  not request these or fall back to one after failure.

The save flow `eB` calls `$P`/`GD` for `object_info.obs` before `zD` retrieves
the full object. `notexist` means unavailable; `encodeStatus=ing` means still
processing. Existing CLI transport tests cover preflight, bounded processing
polling, original selection, and transport/authentication failures.

Private OBS requests use the encrypted OBS access token and Chrome application
header. Encrypted requests also carry `X-Talk-Meta` derived from the message ID.
`gL`/`yL` move `keyMaterial` between the encrypted message payload and the
extension's in-memory `ENC_KM` metadata after Letter Sealing decryption.
The CLI obtains it only from its authenticated message-decryption path; raw
metadata is not a substitute for successful decryption.

## Object encryption and authentication

`_L`, `CL`, `SL`, `TL`, and `zD` establish the following:

1. Base64-decode the 32-byte key material.
2. HKDF-SHA256 with empty salt and info `FileEncryption` yields 76 bytes:
   AES key `[0:32]`, HMAC key `[32:64]`, and nonce `[64:76]`.
3. AES-256-CTR uses the 12-byte nonce followed by four zero bytes. Chrome uses
   a 32-bit counter; the CLI's 20 MiB bound stays far below counter wrap.
4. The final 32 object bytes are the HMAC-SHA256 tag. Authenticate before
   decrypting or exposing plaintext.
5. **Images, audio, and files:** HMAC over the ciphertext body.
6. **Videos:** SHA-256 each consecutive 131072-byte ciphertext chunk (including
   the final partial chunk), then HMAC the concatenated 32-byte hashes. An
   empty body contributes no hashes. `eB` selects this mode by video content
   type. Video previews use the ordinary MAC, which is another reason never
   to substitute a preview for a full video.

DM/group differences belong to Letter Sealing message/key lookup, before this
shared object processing. The CLI retains its historical sender/receiver key
lookups and group-key unwrap; download never registers or replaces group keys.
Synthetic tests cover incoming DMs, own-device echoes, and group reads.

## Deliberate CLI restrictions

The extension has broader fallback behavior. The CLI requires an OID for an
encrypted object, validates an advertised SID against the content type (using
the type mapping when SID is absent), and rejects inconsistent metadata.
Any encryption indication prevents plaintext fallback. Keys, URLs, encrypted
chunks, and raw server bodies are not printed.

Messages with an explicit `DOWNLOAD_URL` use a separate public-resource path
in Chrome. Arbitrary remote URL downloads are outside this implementation;
the CLI reports that variant as unsupported instead of silently fetching the
message-ID object or forwarding credentials to another host.

Downloads use shared selected-chat lookup bounded to 10 pages, at most 1000
returned records, and 30 seconds; see [history](history.md). Plaintext downloads
remain limited to 20 MiB. Full verified bytes are buffered before publication to a
file or stdout. No live capture was needed to resolve these code paths; live
interoperability has not been claimed.

## Outgoing generic-file confidentiality

On 2026-10-18, rechecked the same official Chrome 3.7.2 package documented in
[image send](image-send.md) (SHA-256
`3722ab02edb0aba49c229399ee8df5b4b5a38c60b76efadadfbae395015eb859`).
Its shared `gL` packages `ENC_KM` and `FILE_NAME` as `keyMaterial` and `fileName`;
`EL` removes both outer fields when constructing the encrypted message. `yL`
restores them only after decryption. These helpers apply to encrypted media,
not just images. The CLI now follows this envelope for generic files while
retaining historical incoming metadata conflict checks. No live send was used.


## File capability validation after live inspection

The same Chrome 3.7.2 source uses `sP` and `checkAndGetMediaMessageFlow` for
all media types, including file type 14. `encryptMessage` encrypts media only
when its type-specific flow is 2. The CLI had applied this check only to images;
it now checks `flowMap["14"]` before generic-file uploads too. An explicit 1
requires per-send plaintext consent; missing/unknown/error responses stop sends.

In an authorized test group, LINE advertised file flow 1 even though encrypted
text worked. An encrypted object upload succeeded (HTTP 201), but the subsequent
file message returned HTTP 400 / TalkException. No file message appeared in the
inspected recent history. This exposed the missing capability check; the exact
server exception code was not retained, so the HTTP rejection alone does not
prove which field LINE rejected. The corrected default path is verified to
refuse before upload, registration, message send, or sequence reservation.
Encrypted-file delivery/download remains unverified on a live flow-2 chat.

A subsequent read-only probe fetched the latest group-key capability before the
media-flow query, matching Chrome's negotiation order. The tested group returned
spec version 1, allowed text, excluded file type 14, and still returned file flow
1. With separate explicit plaintext consent, one synthetic file then completed
message creation, upload, history lookup, and byte-identical download. Its
receipt/history reported `encrypted=false`; no group key was registered. This
establishes plaintext interoperability for that group, not encrypted-file support.
