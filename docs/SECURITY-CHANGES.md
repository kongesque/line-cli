# Security changes (unreleased)

- Generic encrypted files keep attachment key material and filenames exclusively
  in the authenticated Letter Sealing payload. Historical downloads still accept
  compatible old envelopes with conflict checks.
- Encrypted native image sending is disabled before uploads or key registration:
  the existing image/preview representation reuses an AES-CTR encryption stream.
  Generic encrypted `--file` is available only with an explicit encrypted-file
  media capability. Plaintext-only file capability requires per-send consent.
- All sends require encryption by default. `--allow-plaintext` permits plaintext
  only after an explicit account/server capability signal. HTTPS still protects
  the connection, but LINE can read this content. The option does not suppress
  key validation, transport errors, or the encrypted-image restriction.

Protocol evidence: official LINE Chrome extension 3.7.2, package SHA-256
`3722ab02edb0aba49c229399ee8df5b4b5a38c60b76efadadfbae395015eb859`.
Shared helpers `gL`/`EL` move media key/name fields into encrypted payloads;
`yL` restores them after decryption. `_L`/`CL` derive and restart the same CTR
stream for each object using the same key material. No secure independent
preview representation was established. Source remains copyright LY Corporation.
Initial validation used synthetic data; the authorized live follow-up and its
limitations are recorded below.

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
and an unchanged request sequence. No plaintext file was sent. Successful live
encrypted file delivery and authenticated download remain unverified on flow 2;
the earlier synthetic recipient roundtrip does not establish that live result.
