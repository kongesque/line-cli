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
