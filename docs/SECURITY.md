# Security model and validation

For supported versions and private vulnerability reporting, see the
[security policy](../SECURITY.md).

Contributor reference for encryption behavior and recorded security checks.
For sending, encryption policy, and user-visible restrictions, use the
[send guide](CLI.md#send-messages-and-files). Storage format and refresh details
are in [SESSION.md](SESSION.md); user-facing changes belong in the
[changelog](../CHANGELOG.md).

[Letter Sealing](#letter-sealing) · [Change record](#security-change-record) ·
[Transport limits](#transport-and-response-limits) ·
[Toolchain](#toolchain-and-vulnerability-scans) · [Live evidence](#authorized-live-follow-up)

## Letter Sealing

Login and message encryption reuse the LINE protocol and crypto runtime inherited
from the upstream project.

[LINE's Encryption Report 2025](https://www.lycorp.co.jp/en/privacy-security/security/transparency/encryption-report/2025/)
distinguishes TLS transport encryption from Letter Sealing (E2EE) and documents
media E2EE in modern official clients. This does not establish this CLI's media
interoperability. Non-E2EE sends still use HTTPS/TLS, but LINE can read their
content. Automatic non-E2EE delivery does not disable TLS or change LINE account
settings; the capability checks below still apply.

### Login and keys

`internal/session/manager.go` exports the login keychain and saves it with tokens
in OS-protected storage. `pkg/line/client.go` supports E2EE login and the explicit
non-E2EE login response. The account password is never persisted.

`pkg/e2ee/manager.go`, `pkg/runner.go`, and `pkg/ltsm` implement device/group key
operations and the signing runtime. Generated LTSM code and embedded memory data
are runtime dependencies, even though some functions are invoked indirectly.

### Sending and reading

`internal/messaging/client.go` and `keys.go` negotiate peer keys, restore historical
keys, and register group keys only during an explicit send with complete membership.
Missing keys, network errors, and ambiguous membership stop the send.
The [send policy](#send-policy-and-attachment-changes) below defines automatic
non-E2EE delivery, strict mode, and separate file/image capability checks.

History resolves the sender/receiver key IDs in each encrypted message. It does
not mark messages read or register group keys. Missing historical device keys can
leave an individual message unreadable.

Generic file attachments use the `emf` OBS service when encrypted and the `m`
service for plaintext. `internal/messaging/files.go` derives file keys with HKDF,
encrypts with AES-CTR, and verifies HMAC-SHA256 before saving a download. The CLI
treats media sent with `--file` as generic files. Attachment key material and
filenames stay inside the authenticated Letter Sealing payload. Current send
restrictions and live interoperability limits are listed in the
[security validation record](#security-change-record).

Mutations are never automatically replayed after an uncertain response. The
`group_key_registered` output field reports a new registration during that send;
`false` does not mean an encrypted group message lacks a key.

## Security change record

For user-facing upgrade actions, see the [changelog](../CHANGELOG.md).
This record separates implementation changes, synthetic verification, and
authorized live observations. The dated checks below apply to their recorded
revisions and environments; they do not replace the next release's CI gates.

| Path | Current restriction and validation |
| --- | --- |
| Text | E2EE preferred; explicit unavailability permits HTTPS/TLS delivery unless strict mode is requested. One authorized encrypted group send/readback succeeded. |
| Generic files | Explicit file capability required. Explicit non-E2EE capability permits automatic HTTPS/TLS delivery. Synthetic encrypted roundtrip passed; live encrypted delivery remains unverified. Plaintext roundtrips succeeded in the authorized group and direct chat. |
| Native images | Encrypted sending disabled before remote mutations. Non-E2EE delivery requires explicit capability; native-image live interoperability remains unverified. |

### Send policy and attachment changes

- Generic encrypted files keep attachment key material and filenames exclusively
  in the authenticated Letter Sealing payload. Historical downloads still accept
  compatible old envelopes with conflict checks.
- Encrypted native image sending is disabled before uploads or key registration:
  the existing image/preview representation reuses an AES-CTR encryption stream.
  Generic encrypted `--file` is available only with an explicit encrypted-file
  media capability. Explicit non-E2EE capability permits HTTPS/TLS delivery.
- CLI sends prefer Letter Sealing and automatically permit non-E2EE delivery
  only after an explicit account/server capability signal. `--require-e2ee`
  blocks that mode before remote actions. Missing keys, transport failures, and
  the encrypted-image restriction still stop sends. HTTPS/TLS protects non-E2EE transfers, but LINE
  can read their content. Human receipts identify the mode; JSON reports
  `encrypted: false` without extra output or prompts.
- The messaging API's zero-value `SendOptions` still requires E2EE. The CLI
  explicitly selects its automatic policy; library callers must choose theirs.

There is no encryption confirmation or consent-based restart. Capability and key
validation run under the normal session lock. Sends, uploads, and group-key
registrations are attempted once, with no automatic mutation retry.
The earlier confirmation flow passed synthetic checks on 2026-10-19 and was
subsequently replaced. The automatic send policy passed the full
[v0.5.0 CI suite](https://github.com/kongesque/line-cli/actions/runs/39240615793)
at release revision `aeefb35`, including the updated regression tests. All six
targets also passed the
[release builds and vulnerability scans](https://github.com/kongesque/line-cli/actions/runs/39240615899).
No live sends were performed as part of the automatic-policy change; earlier
authorized live observations are recorded below.

### Protocol evidence

Official LINE Chrome extension 3.7.2, package SHA-256
`3722ab02edb0aba49c229399ee8df5b4b5a38c60b76efadadfbae395015eb859`.
Shared helpers `gL`/`EL` move media key/name fields into encrypted payloads;
`yL` restores them after decryption. `_L`/`CL` derive and restart the same CTR
stream for each object using the same key material. No secure independent
preview representation was established. Source remains copyright LY Corporation.
Initial validation used synthetic data; the authorized live follow-up and its
limitations are recorded below.

### Transport and response limits

Authenticated LINE requests now reject redirects at dispatch, even when a caller
supplies an HTTP client with a permissive redirect callback. A redirect leaves a
mutation outcome unknown and never causes a replay.

Network response budgets apply after transport decompression: RPC/login/page
metadata 16 MiB (QR retains its stricter 1 MiB), OBS metadata/upload replies
64 KiB, HTTP error bodies 4 KiB, default media 20 MiB plus the authentication tag.
SSE allows 1 MiB per line, 4 MiB and 4096 lines per event, including comments and
event names. Oversized events never reach delivery/checkpoint callbacks.
Synthetic maximum-count history pages fit the RPC budget; live server maximum
keychain/history sizes have not been measured. Legitimate oversized responses
fail explicitly and may require a reviewed budget adjustment.

### Toolchain and vulnerability scans

Builds select Go 1.27.2, including CI/release jobs. `build.sh` checks the actual
compiler selected by `GO`/`GOTOOLCHAIN`; older compilers and unreviewed versions
fail before building. The minimum module directive is also Go 1.27.2. This
replaces the earlier Go 1.26+ requirement. To select the reviewed compiler:
`GOTOOLCHAIN=go1.27.2 ./build.sh`. Future toolchain updates should change the
module, CI pins, and compiler guard together after advisory review.

Run `./scripts/security-check.sh ./bin/line` for source and binary scans using
pinned govulncheck v1.9.0. Release jobs scan each target before packaging.
[GO-2026-6611](https://pkg.go.dev/vuln/GO-2026-6611) affects HTTP/2 clients;
[Go 1.27.2](https://go.dev/doc/devel/release#go1.27.0) contains its fix. Scanner
findings require applicability review; there is no standard-library exemption.

Audit triage on 2026-10-18 with govulncheck v1.9.0 and Go 1.27.2: source scan
reported no reachable or imported-package vulnerabilities. Module-only findings
were GO-2026-6629 (`x/text/secure/precis`), GO-2026-6355, GO-2026-6354 and
GO-2026-6303 (`x/crypto/ssh`), and GO-2026-5932 (`x/crypto/openpgp`). The CLI and
scanned public libraries do not import those packages. These are recorded as
inapplicable to the current dependency graph, not suppressed scanner failures.

Release executables retain their symbol tables (`-w`, without `-s`) so binary
scans include inlined-symbol analysis. govulncheck v1.9.0 conservatively treats
all vulnerable module symbols as present in stripped executables. Retaining
symbols made the rebuilt macOS binary scan pass without advisory exemptions;
DWARF debug data remains omitted.

### Recorded synthetic and build validation

Validation on 2026-10-18 at source revision `d8df371`: the complete CLI/protocol
race suite, runner/LTSM suites, go vet, and Staticcheck v0.8.2 passed. The real
synthetic recipient decrypted the sealed file key/name, authenticated the
attachment, and rejected a tampered payload. Bounded-response and SSE-line
fuzzing passed 15 seconds each (291,209 and 292,307 cases respectively).

All six release targets were rebuilt with Go 1.27.2: Linux and Windows
amd64/arm64 without CGO, and macOS amd64/arm64 with CGO. Compiler identity was
checked in each executable; target-specific source and binary scans using
govulncheck v1.9.0 reported zero reachable or imported-package vulnerabilities.
The module-only advisory triage above still applies. The native macOS arm64
CLI also passed its help smoke test. The selected-compiler guard passed
synthetic checks for accepted/rejected versions and a custom `GO` executable.

Cross-builds do not replace native CI. Linux Secret Service and Windows DPAPI
integration, and the complete native CI matrix, remain release gates. The
initial validation above did not include live LINE actions.

### Authorized live follow-up

Live follow-up (completed 2026-10-19): one explicitly authorized synthetic text
was sent encrypted and read back successfully in the designated test group.
The session was readable from macOS Keychain with native protection verified;
saved Letter Sealing keys loaded and the text send persisted its request sequence.
This checks the active session, not protection against a compromised OS account
or availability after reboot.

The file test failed, including one separately authorized file-only retry. On
that retry the encrypted upload returned HTTP 201 with an object ID; the file
message returned HTTP 400 / TalkException, and no new file message appeared in
recent history. The observed outgoing envelope contained neither outer key
material nor filename, and no plaintext fallback occurred. An uploaded synthetic
object may remain. No raw traffic, tokens, encrypted chunks, or private message
contents were saved as capture artifacts.

A read-only capability check showed file flow 1 (plaintext-only), despite working
text encryption. Static Chrome 3.7.2 evidence confirms type-specific media checks
also apply to files. The CLI now checks file type 14 before uploading. The live
refusal check passed with `encryption_required`, zero uploads/sends/registrations,
and an unchanged request sequence. No plaintext file was sent in that phase. Successful live
encrypted file delivery and authenticated download remain unverified on flow 2;
the earlier synthetic recipient roundtrip does not establish that live result.

Authorized plaintext follow-up (2026-10-19): after separate explicit consent,
one `line-security-test.txt` was sent with the then-supported `--allow-plaintext`
option. The receipt and history both reported `encrypted=false`, no group key was registered, the
filename matched, and the downloaded 28 bytes exactly matched the synthetic
fixture. This verifies the plaintext path only; it is not an encrypted-file
interoperability result.

The additional read-only negotiation probe followed Chrome's order: retrieve
the latest group-key capability, then determine media flow. The selected group
returned spec version 1, included text in `allowedTypes`, excluded file type 14,
and continued to report file flow 1. No missing client-side negotiation step
was established by this check. Do not force encrypted-file sending or change
group keys to bypass those reported capabilities.

Direct-chat follow-up (2026-10-19): a separately authorized test contact also
advertised file flow 1. The guarded default send refused with zero uploads,
sends, or registrations and an unchanged request sequence. After explicit
per-send plaintext consent, one synthetic `line-security-test.txt` was sent
successfully. Its receipt/history reported `encrypted=false`, the filename
matched, and the downloaded 28 bytes matched the fixture exactly. This verifies
generic text-file delivery in that direct chat; direct chats do not necessarily
advertise encrypted-file support. No additional text message was sent.
