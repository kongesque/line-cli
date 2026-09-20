# QR login: protocol layer

## Completion Checklist

- [x] Context-aware session, QR, certificate, PIN, and final-login requests.
- [x] Exact signed request shapes and polling headers covered by synthetic tests.
- [x] Bounded same-session polling with cancellation and expiry classification.
- [x] Sanitized, bounded responses and no final-login replay or redirects.
- [x] Race tests, crypto regressions, formatting, vet, staticcheck, and build.
- [x] QR login-key lifecycle and independent synthetic key-agreement tests.
- [ ] Session orchestration and terminal UI.
- [ ] Authorized live validation of outstanding protocol unknowns.

Status: phase 1 implemented; no live QR login validation. CLI selection, curve
key ownership, session orchestration, and terminal rendering are later phases.

Evidence: LINE Chrome Extension manifest 3.7.2, local static bundle. `main.js`
SHA-256: `2912a06d868c2829636be1613c622f28807efe74a7868cfb143678a38b80cc2a`.
Source anchors are recorded in `pkg/line/qr_login.go`. Synthetic test responses
exercise parsing; they do not establish unknown server behavior.

## Requests

All methods use signed JSON POSTs under `/api/talk/thrift/LoginQrCode/` on the
Chrome gateway. The HMAC covers the exact path and serialized argument array.

| Service | Method | Single object inside the argument array |
| --- | --- | --- |
| SecondaryQrCodeLoginService | createSession | Empty object |
| SecondaryQrCodeLoginService | createQrCode | authSessionId |
| SecondaryQrCodeLoginPermitNoticeService | checkQrCodeVerified | authSessionId |
| SecondaryQrCodeLoginService | verifyCertificate | authSessionId, certificate |
| SecondaryQrCodeLoginService | createPinCode | authSessionId |
| SecondaryQrCodeLoginPermitNoticeService | checkPinCodeVerified | authSessionId |
| SecondaryQrCodeLoginService | qrCodeLoginV2 | systemName=CHROMEOS, modelName=CHROME, autoLoginIsRequired=false, authSessionId |

Scan polling sets `X-Line-Session-ID` and `X-LST` to the server's interval in
seconds multiplied by 1000. PIN polling uses the same session header and
`X-LST=110000`. A cloned HTTP client allows the requested wait plus 30 seconds,
without changing ordinary request timeouts. All requests accept a context.

## State transitions

Create session → create QR → append the login public key in orchestration →
poll scan → verify certificate → optionally create PIN and poll approval →
complete once → validate and save in orchestration.

A wrapped gateway error with `code=10052` and `data.statusCode=410` permits
another scan poll on the same session. There are at most `longPollingMaxCount`
polls, with delays of 1, 2, 4, 8, 16, 30, 30… seconds. Only exhaustion yields
`ErrQRCodeExpired`. PIN timeout yields `ErrQRPinTimeout`, which must not trigger
a fresh QR session. An outer HTTP 410 alone is not sufficient evidence.

Final login never automatically retries or follows redirects. Its error records
whether dispatch was possible and whether an access token was confirmed before
metadata validation failed. A lost response can mean remote session replacement.
No method saves credentials or changes the client's access token.

Response bodies are capped at 1 MiB. Errors retain numeric gateway/service/status
codes, never server messages, raw bodies, or transport error text. Context
cancellation and deadline identity remain available through `errors.Is`.

## Unverified behavior

- Exact invalid/missing-certificate rejection codes. All errors currently
  propagate; there is no guessed classification that permits PIN fallback.
- Actual outer HTTP statuses and wire envelopes for polling expiry.
- Explicit QR LSOFF capability metadata. Missing/malformed keys and unknown
  metadata errors fail closed; `NoE2EE` is never inferred.
- Exact refresh field representations and session displacement timing.

Complete results retain token V3 refresh fields and normalize the evidenced key
metadata into `LoginResult`. Profile validation, refresh validation, key unwrap,
and secure storage remain required before a session may be saved.

## QR login-key ownership

`Runner.BeginQRLoginKey` reserves the active login key for a single QR flow.
Call the returned lease's `Generate` once per fresh server session. It invokes
`Curve25519Key.generate(true)` and returns the 32-byte public key in standard
base64. Repeated scan polls reuse that value; regenerating destroys the previous
curve key through the runtime's registered destructor.

Keep the lease open through `LoginUnwrapKeyChain` and exported-key persistence.
Defer `Close` on every exit. Closing invalidates the active handle and releases
ownership; repeated or stale closes cannot affect a later flow. A second QR
flow, email secret generation, and unowned key cleanup are rejected while the
lease is active. An existing email key must first be explicitly released with
`ClearLoginKey` after its flow ends.

Synthetic tests check standard-base64 encoding, fresh keys on regeneration,
cleanup, missing-key failures, and concurrent ownership. An independent pure Go
X25519 peer encrypts a synthetic message that the native active login key must
decrypt. This proves key agreement and private-key retention. A full synthetic
keychain unwrap vector remains deferred: the repository has no established
keychain encoder/fixture, and message encryption is not claimed to verify the
keychain serialization format.
