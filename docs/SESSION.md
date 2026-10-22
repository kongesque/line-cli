# Session lifecycle and storage

Contributor reference for token refresh, credential persistence, and the Linux
headless engine. For setup, host-only storage consent, migration, services,
and recovery steps, use the [account](CLI.md#account-and-session) and
[headless](CLI.md#linux-servers-and-headless-storage) chapters of the user guide.
For message encryption, see the [security model](SECURITY.md#letter-sealing).

[Token refresh](#token-refresh) · [Call paths](#call-path-audit) ·
[Headless engine](#headless-storage-engine) · [Envelope](#envelope-version-1) ·
[Migration](#logout-and-migration-recovery) · [Recorded verification](#release-verification)

## Current boundaries

- The normal seven-day boundary is an access-token refresh boundary, not a fixed
  local-session expiry. Persist rotated tokens before using the new client.
- Safe reads can recover authentication; message mutations are never replayed.
- Native OS-protected storage is the default. Headless enrollment uses explicit
  host-only consent, and changing backend requires the migration transaction.
- Keep one account, user identity, and config environment consistent. Stop old
  processes before upgrading lock layouts or migrating storage.
- Dated audit/VM results below apply to their recorded revisions and environments;
  rerun the [development and release checks](DEVELOPMENT.md) for a new candidate.

## Token refresh

Historical refresh audit, 2026-09-27, for the Go CLI and Chrome gateway protocol.
No live login, messages, uploads, or captures were performed. The local capture
profile was absent; retry-policy behavior below is a bounded CLI implementation
of the supplied policy fields, not a claim of a newly observed Chrome trace.

### Upstream evidence

Public source snapshots inspected:

- LINEJS [`ef6c3d9`](https://github.com/evex-dev/linejs/tree/ef6c3d9f70dd41fa51053615d47f071f58cf8db3):
  [auth refresh](https://github.com/evex-dev/linejs/blob/ef6c3d9f70dd41fa51053615d47f071f58cf8db3/packages/linejs/base/service/auth/mod.ts)
  persists a rotated refresh token, retaining the old one when absent, and uses
  issue time plus duration. [Login](https://github.com/evex-dev/linejs/blob/ef6c3d9f70dd41fa51053615d47f071f58cf8db3/packages/linejs/base/login/mod.ts)
  also saves the refresh token and server-derived deadline.
  [Request recovery](https://github.com/evex-dev/linejs/blob/ef6c3d9f70dd41fa51053615d47f071f58cf8db3/packages/linejs/base/request/mod.ts)
  recognizes MUST_REFRESH_V3_TOKEN and bounds replay. It uses a different Thrift
  endpoint, `/EXT/auth/tokenrefresh/v1`; that endpoint is not substituted for Chrome.
- Beeper [`c40908e`](https://github.com/beeper/line/tree/c40908ee46242e45e696fd116a52b97f2553f9c7):
  [Chrome refresh](https://github.com/beeper/line/blob/c40908ee46242e45e696fd116a52b97f2553f9c7/pkg/line/client.go)
  POSTs JSON `{refreshToken, retryCount}` to `/api/auth/tokenRefresh`, with Chrome
  headers/HMAC, and expects a direct TokenV3IssueResult.
  [Recovery](https://github.com/beeper/line/blob/c40908ee46242e45e696fd116a52b97f2553f9c7/pkg/connector/auth_recovery.go)
  serializes recovery and compares the failing client's token to current tokens
  before interpreting logout. [Client lifecycle](https://github.com/beeper/line/blob/c40908ee46242e45e696fd116a52b97f2553f9c7/pkg/connector/client.go)
  retains rotated tokens and distinguishes forced logout from recovery.
- Beeper's V3 struct contains `refreshApiRetryPolicy`, but its recovery does not
  consume that policy or persist issue time/duration. LINEJS also does not consume
  the policy. Neither is evidence of Chrome's exact retry schedule. The CLI's
  policy implementation is a bounded CLI policy, not copied
  behavior. Beeper's password-based relogin fallback is deliberately not adopted:
  the CLI never stores passwords.

### What the CLI already did correctly

- QR and email/PIN login preserve TokenV3IssueResult in the API layer and choose
  its access token over the legacy auth token. `pkg/line/structs.go` already has
  all requested V3 fields and the Chrome decimal-string timing representation.
- Both login methods use `finishLogin`, preserving account identity, certificate,
  exported Letter Sealing keys, access/refresh tokens, and a refresh deadline.
- `/api/auth/tokenRefresh` already uses the current refresh token and Chrome
  headers/signature. Empty access-token results are rejected. A response omitting
  the refresh token retains the previous one.
- Manager proactively refreshes when its saved deadline is due, persists before
  retrying a read, recreates the client, and limits authentication recovery.
- Sends, reactions, unsends, group key registration, and uploads are single-shot
  mutations. Request sequences are persisted before applicable mutations. Only
  OBS readiness/download reads have their own encoding-pending retry loop.
- Cross-process command locks prevent concurrent refresh/save races. The watcher
  uses short command locks and a separate singleton watch lock.
- Watch checkpoints reload current session state after decoding. Existing stale
  SSE logout handling already compared access tokens before invalidating.
- No local session-age cutoff exists. Explicit server logout invalidates saved
  credentials; ordinary refresh-required errors and network failures do not.
- Storage already commits the complete serialized state: Keychain SecItemUpdate
  on macOS; encrypted file sync/rename/directory sync on Linux; DPAPI temporary
  file sync and write-through replacement on Windows. Storage errors stop work.

### Gaps found and fixed

| Previous behavior | Updated behavior |
| --- | --- |
| Deadline was receipt time + duration minus margin; ignored server issue time | Issue time + duration minus 30 seconds, or half the lifetime for tokens shorter than 60 seconds; legacy receipt-time deadline remains a fallback |
| Session discarded duration, issue time, and retry policy | Both login paths and refresh persist all requested V3 fields in the existing single commit |
| Policy existed only in the wire struct; refresh was attempted once | Transient network/408/429/5xx refresh failures get up to four attempts; provided initial/max delay, multiplier, jitter are used with validation and a 30-second delay cap; retryCount increments |
| Refresh request was not cancellable and published its new client token before persistence | Context-aware refresh returns token state without changing the original client; Manager saves before creating a client with the new token |
| Code matching could miss whitespace or confuse 119 with 1190 | JSON normalization and numeric boundaries; namespace-aware unauthorized-device and TokenAuthException handling |
| HTTP-200 auth envelopes could lose codes inside method-specific parsing | RPC boundary preserves a typed, body-redacted auth error before method parsing; non-auth capability envelopes keep existing handling |
| Runtime mutation auth failure left credentials stale | Recover credentials once, but return the mutation failure without replaying it |
| SSE auth recovery depended on a Talk probe also failing | Explicit refresh-required SSE errors refresh directly; generic 401/403 probes for logout, then refreshes even when the probe succeeds |
| SSE lifetime was only the fixed probe interval | Stream timeout is the earlier of the next probe or refresh deadline; reconnect uses the persisted access token; consecutive auth rejection after recovery stops without another rotation or invalidation |
| Stale-response protection was confined to the watcher | Manager compares login generation/current tokens before recovery or invalidation; shared-manager calls are serialized too |
| Diagnostics/tests/docs suggested Chrome sessions expire at 168 hours | Treat 168 hours as the normal access-token refresh boundary; describe refresh rejection separately from forced logout |

Gateway code 10051 alone means RESPONSE_ERROR, not necessarily authentication.
TalkException 119 / refresh-required text trigger recovery. TalkException 8 and
TokenAuthException 3 identify unauthorized-device failures; existing explicit
V3_TOKEN_CLIENT_LOGGED_OUT, REQUEST_NEED_LOGIN/10004, and invalid-sender-key
logout classifications are retained. TokenAuthException 1/2 indicate an auth
rejection without proving a forced logout. Generic 401/403 can trigger recovery
but never alone persist invalidation. A logout code cannot reliably prove which
other client, if any, displaced the session; diagnostics do not invent that cause.

### Call-path audit

| Path | Session handling |
| --- | --- |
| whoami, contacts, chat listing/selection, sender display lookup | Manager.Do |
| history, message lookup for replies/actions, blocked contacts | Manager.Do |
| peer negotiation, peer/group key reads, membership reads | Manager.Do |
| send/reply, react/cancel reaction, unsend | Manager.Mutate; persisted sequence; no replay |
| encrypted/plain file uploads, group-key registration | Manager.Mutate; no replay |
| file/media downloads, including channel/encrypted OBS token acquisition | Entire safe read flow runs inside Manager.Do |
| watch revision/profile probes | Manager.DoContext |
| SSE transport | Direct API client only for the stream; Manager.RecoverStream under command lock for auth, generation/token comparison before invalidation |
| QR/email authentication and immediate setup | Direct clients inside Manager login lifecycle; complete new state saved by finishLogin |
| auth status/check/migrate and local logout | Local storage only; do not contact LINE |

No ordinary CLI command constructs a pkg/line.Client to bypass Manager. External
users of pkg/line.Client remain responsible for their own session persistence;
that package does not automatically retry mutations or own credential storage.
Different CLI processes still fail fast with ErrBusy when a command holds the
session lock; they cannot independently refresh the same stored token. Concurrent
calls sharing one Manager wait for its mutex and then load the committed state.

### Regression coverage and limits

Synthetic tests cover complete email and QR metadata, proactive margin, stale
receipt-time deadlines overridden by issue metadata, restart before/at/after
168 hours, access/refresh-token rotation, omitted refresh tokens, code 119 read
recovery, HTTP-200 errors, refresh rejection/malformed results, transient retries,
jitter bounds, cancellation, forced logout, stale clients/streams, concurrent
refresh, persistence failure, SSE-only auth errors, and scheduled SSE rotation.
Existing messaging tests retain no-replay and encryption capability guards.

Normal token rotation should survive the seven-day access-token boundary without
manual login when refresh credentials remain valid and storage is writable.
There is no seven-day live soak test or promise against server revocation.
A rejected/missing refresh credential or genuine logout may require manual login.
If a server rotates credentials but the response is lost or local persistence
fails, the client cannot guarantee recovering that unknown/uncommitted token.

### Historical token-audit validation

Recorded on macOS with Go 1.27.1; this is historical evidence, not the current
build requirement. Current builds require Go 1.27.3.

Checks recorded for that audit:

- `go test -race ./internal/... ./cmd/line ./pkg/line/... ./pkg/e2ee` passed.
- The final SSE recovery bound also passed `go test -race ./internal/events ./internal/cli`.
- `go vet ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg` passed.
- `staticcheck ./internal/... ./cmd/line ./pkg/line ./pkg/e2ee ./pkg` passed.
- `goimports -local github.com/kongesque/line-cli` reports no formatting changes.
- `./build.sh`, `./bin/line help`, and `git diff --check` passed.

Go/staticcheck caches and cached-module builds of the lint tools used `/tmp`
locations because the sandbox does not allow writing the normal user caches.
Linux native keyring and Windows DPAPI integration tests were not executed on
this macOS host; their storage implementation is unchanged.

## Headless storage engine

Native storage remains the default. `login --headless` enrolls host-only storage;
reauthentication keeps the existing backend. Use
`auth migrate --storage=headless` for an explicit native-to-headless migration.
Never replace session files manually to change backends. Setup and service
commands are in the [CLI guide](CLI.md#linux-servers-and-headless-storage).

### Envelope version 1

The authoritative path remains `$XDG_CONFIG_HOME/line-cli/session.enc`. Integers
are unsigned little-endian. The complete header, including the sealed credential
and nonce, is AES-256-GCM associated data. All account identity, tokens, E2EE keys,
generation, request sequence, and watch checkpoint remain inside encrypted State.

| Offset | Bytes | Meaning |
| --- | --- | --- |
| 0 | 16 | Magic: `LINECLI\0SESSION\0` |
| 16 | 2 | Envelope version, currently 1 |
| 18 | 2 | Backend ID, currently 1 for systemd user credentials |
| 20 | 4 | Sealed credential byte length |
| 24 | 1 | Protection: 1 host-user, 2 host-tpm2-user, 3 host-tpm2-signed-user |
| 25 | 3 | Reserved, must be zero |
| 28 | 4 | Fixed PCR mask |
| 32 | 2 | PCR bank |
| 34 | 2 | Reserved, must be zero |
| 36 | 4 | Signed PCR mask |
| 40 | 12 | Random GCM nonce |
| 52 | variable | Base64 systemd sealed credential, including original whitespace |
| following | variable | Encrypted State JSON and 16-byte GCM tag |

The aggregate limit is 4 MiB, the sealed credential limit is 192 KiB encoded /
128 KiB decoded, and individual TPM fields are bounded at 16 KiB. State must fit
within the aggregate limit after envelope overhead. There is no compression.
Unknown versions, backends, reserved fields, schemes, or mismatched policy fail.
Policy is unverified until both systemd decryption and session authentication
succeed. Recognizing a header does not prove protection or hardware availability.

Existing native files have no magic: they remain the legacy nonce + ciphertext
format with `line-cli-session-v1` associated data. The resolver chooses one format
from the stored bytes and does not try another provider after failure. Corrupting
the new magic can make a file look legacy, but it cannot pass legacy AEAD; Save
authenticates existing legacy data before replacing it. The native provider also
refuses a recognized headless marker to prevent accidental native overwrites.

### Systemd adapter

The adapter resolves `/usr/bin/systemd-creds` directly and checks that its target
and parent directories are root-owned and not writable by group/other users.
It rejects root execution and helper versions outside the inspected 256–259
range. Versions 257 and 259 have native disposable-VM evidence; 256 and 258 are
source-compatible candidates, not separately runtime-certified platforms.
Operational readiness still requires a successful actual roundtrip.

Ownership/permission rejection has the separate `headless_helper_untrusted`
reason and remains exit 69 and `errors.Is(err, ErrHeadlessUnavailable)` compatible.
A user-service namespace can leave host root unmapped, causing this trust check
to fail. The diagnostic points operators to sandbox settings without guessing
that every trust failure is caused by a sandbox. Discovery, file-type, root-user,
and version failures retain `headless_unavailable` with fixed stage-specific
text. Helper subprocess errors include broker/identity/sandbox guidance but never
raw stderr. See [Run unattended](CLI.md#run-a-watcher-without-a-login-session) for the Debian 13
issue report, service example, and validation limits.

Each operation uses `--user`, the fixed name `line-cli-session-key`, and
`--newline=no`. Version 259 additionally uses `--no-ask-password` and, for reads,
`--refuse-null`. Earlier builds rely on the inspected noninteractive broker path
and the runtime evidence from Phase 0. No desktop D-Bus session or service-unit
credential directory is needed. Broker absence, denial, host-secret loss, wrong
user, or incompatible policy fails closed.

The helper receives a fixed minimal environment, key bytes on stdin, and private
stdout/stderr pipes. No shell, key arguments, key environment values, plaintext
temporary files, or raw diagnostics are used. Calls have a ten-second deadline,
parent cancellation, bounded output (32 bytes for keys, 4 KiB for diagnostics),
and a bounded inherited-pipe wait. Buffers are cleared where practical; this is
not a claim of complete memory erasure.

Encryption uses an explicit requested mode. Host-only mode requires consent in
the CLI. TPM requests must actually return the matching TPM scheme;
accepted command-line options are insufficient. Auto-selection and automatic
fallback are not used. Signed-PCR metadata is recognized by the parser but the
adapter rejects its use pending evidence. The engine makes no claim that a TPM
is physical, that hardware binding proves verified boot, or that reboot access
has been tested for a newly enrolled user's machine.

The credential parser is independently written from the systemd v257 wire
layout (LGPL-2.1-or-later source provenance is retained in the code). Its IDs and
layout checks come from the reviewed [Phase 0 experiment](https://github.com/kongesque/line-cli/blob/main/scripts/systemd-probe/README.md).
It performs no TPM sealing or systemd credential cryptography itself.

### Lifecycle and validation

#### Reads, writes, and login

`linuxStorage` reads the authoritative bytes for every locked Load/Save/Prepare.
It retains no provider selection or wrapping key across operations. Headless
updates authenticate the current envelope, reuse its exact sealed credential and
policy, generate a fresh nonce, and use the Phase 1 durable replacement routine.
Preflight uses a separate synthetic file and reports cleanup failures. A failed
helper cannot trigger resealing, native fallback, or file replacement. An error
after rename/directory sync is reported as uncertain durability without rollback.

The candidate builder returns verified bytes without committing them. The login
controller prepares a candidate, obtains explicit host-only consent, checks for
changed storage around unlocked prompts, and writes only after authentication.
An unaccepted candidate cannot pass Manager.Login preflight or Save. Migration
uses a separate transaction. Stop older commands and watchers before changing
formats or lock conventions.

#### Logout and migration recovery

Linux logout uses an 80-byte private receipt: the 15-byte `LINECLI\0LOGOUT\0`
marker, one backend byte (1 native / 2 headless / 3 migration cleanup), the 32-byte SHA-256 digest of the
session file, and a 32-byte SHA-256 checksum of the preceding receipt bytes. The
checksum detects corruption; it is not an authentication claim against same-user
malware. Receipt creation, session removal, and receipt removal use durable file
operations. A receipt survives native-key cleanup failure or uncertain removal;
operations cannot create a new session while it remains. Logout verifies native
key removal using non-unlocking search, because clear's missing/unlocked-item
result alone cannot prove a locked key was deleted. An absent session without a
receipt cannot authorize deletion of an orphaned native item.

Migration stages `.migration.candidate`, reopens and authenticates its full state,
then durably creates the 144-byte `migration.pending` receipt before replacement.
The receipt is a 16-byte `LINECLI\0MIGRATE\0` marker, followed by SHA-256 digests
of the original ciphertext, the candidate's sealed credential, and the old native
key, then a SHA-256 checksum of those 112 bytes. It contains no recoverable key.
After the candidate rename and directory sync, cleanup verifies native ownership
and the key fingerprint before clearing the native item. A cleanup retry never
reseals or replaces committed state. The sealed-key digest allows normal token,
sequence and checkpoint updates while cleanup is pending. Pre-commit receipts
block ordinary operations until resumed; unknown/mismatching receipts fail closed.
Logout backend 3 records that native cleanup belongs to the migration receipt,
so a crash after removing that receipt cannot delete a later replacement key.

#### Locks and native-key ownership

Linux session operations hold both the config-directory session lock and an
account-wide credential lock. The latter lives with a bounded observed-path
record under `$HOME/.config/line-cli`. Native deletion checks every known path
and the default path, authenticating other headless files and syncing their
directories. Native/unreadable paths retain the shared key. Historical custom
paths must be registered before cleanup; changing HOME or running older versions
concurrently is unsupported. These files survive logout.

#### Status and test coverage

Native read-only status uses libsecret search without `--unlock`, or a per-query
macOS LAContext with interaction disabled. Linux/macOS native write status remains
unknown with an explicit interactive-check requirement. Windows DPAPI and headless
write status use separate probes. See [CLI.md](CLI.md) for user-facing
status fields, limits, and exit codes. Libsecret search/clear behavior was checked
against [upstream source](https://github.com/GNOME/libsecret/blob/0.21.7/tool/secret-tool.c).

Synthetic tests cover every-byte envelope tampering, every truncation, size and
integer bounds, unknown fields, legacy compatibility, changed backend between
operations, unchanged sealed key across updates, failure preservation, temporary
cleanup, real child-process limits, deadlines, cancellation, and environment
isolation. Fuzz targets are `FuzzEnvelope` and `FuzzCredential`.

Native testing on 2026-09-13 used disposable ARM64 VMs and new synthetic state:

| Runtime | Result | Twenty uncached full loads |
| --- | --- | --- |
| Ubuntu 26.04 / systemd 259 | Enrollment, load, update, preflight, wrong name, tamper checks passed | 86 ms total / 4.3 ms mean |
| Debian 13 / systemd 257 | Same checks passed | 170 ms total / 8.5 ms mean |

These timings include helper discovery/version detection and unsealing. They are
VM observations, not performance guarantees. No persistent key cache was added.
Physical TPM, signed-PCR use, and UniPi behavior remain unverified. Phase 0 holds
the separate wrong-UID, broker-denial, reboot, and copied-disk observations.

Run the native engine test only in a disposable booted Linux VM: sealing can
initialize its global system host secret. Set `LINE_CLI_TEST_SYSTEMD_CREDS=1` and
run `go test -v ./internal/session -run '^TestLinuxSystemdStorageIntegration$'`.
It never contacts LINE or reads the developer's session or keyring. Ordinary
tests skip this integration unless it is explicitly enabled.

Phase 4 adds `TestLinuxSystemdMigrationIntegration` under the same opt-in gate
and native Secret Service migration cleanup to the private D-Bus CI fixture.
Its transaction failure tests use synthetic providers. A fresh native systemd
migration run completed during the release verification below.
The actual watcher and migration resolver passed the mid-stream migration and
logout regression with synthetic providers on macOS through a temporary Go
source overlay, including the race detector. Linux builds include that regression
in the ordinary session test suite.

### Release verification

On 2026-09-19 a fresh disposable Debian 13.7 ARM64 VM ran systemd
257.13-1~deb13u1 and kernel 6.12.107+deb13-arm64 without a TPM. The official
Debian generic ARM64 image (2026-09-14 listing) was verified against the published
SHA-512 checksum before conversion to a VMware disk. Tests ran as an unprivileged
UID 1501 with the credential socket enabled, without desktop login or a user
manager. All account state and LINE responses were synthetic.

| Check | Result |
| --- | --- |
| Complete Linux session unit suite | Passed |
| Native systemd engine and migration | Passed; 20 full loads took 158 ms total |
| CLI headless login, reauthentication, status and logout with fake LINE | Passed |
| Private D-Bus Secret Service integration, including migration cleanup | Passed |
| CLI migration using both native providers and fake LINE | Passed; complete state preserved and no LINE call during migration/status/logout |
| Same production envelope after reboot, system service and real cron | Passed; full state loaded and next request sequence persisted by each |
| Same UID after reboot, with user manager inactive | Passed |
| Readable fixture copied to a different UID | Broker rejection; no filesystem error counted as success |
| Broker stopped, then restored | Failed closed, then recovered |
| VM host secret removed, then restored | Failed closed, then recovered the same state |

The first reboot attempt could not execute a test binary stored in `/tmp`, which
Debian cleared at boot. Installing it at `/usr/local/libexec/line-session.test`
and repeating the reboot passed both scheduled checks. This was a fixture issue.
The additional CLI migration test initially used an email the fake API did not
accept; correcting the synthetic fixture made the complete flow pass.

Release review also found that retrying an observed-path registration after a
failed directory sync could trust a visible but not confirmed-durable registry.
Known-path registration now confirms directory durability before the session
lock is returned, with a regression test for repeated failure and recovery.

The maintained macOS race suite, native/Linux vet and staticcheck, goimports,
and CLI build/help passed. Existing [CI run 35438746247](https://github.com/kongesque/line-cli/actions/runs/35438746247)
at `266e185` passed Linux private Secret Service, Windows native DPAPI, macOS,
and their build/test gates. That CI run precedes the Linux-only durability fix
and additional tests in this phase; the fresh VM suite covers that fix. Parser
fuzzing and exhaustive corruption/fault-injection tests from Phases 2–4 remain
part of the evidence; no parser change was made in this phase.

Ubuntu 26.04/systemd 259 retains the earlier engine/CLI and Phase 0 reboot/cron
evidence; the new full-state reboot and real-provider CLI migration checks above
were run on Debian only. Source acceptance of 256/258 is not a runtime claim.
Phase 0 documents copied-disk recovery and software-TPM policy failure separately.
Physical TPM, signed-PCR enrollment and UniPi deployment remain outside the
verified release scope. Host-only storage does not resist a complete disk copy.

#### Repeating the persistent fixture

Compile `go test -c ./internal/session` for the disposable VM's OS/architecture
and install the test binary on persistent storage. The fixture is deliberately
opt-in and never calls LINE. In a fresh directory owned by the test UID:

```sh
sudo -u lineprobe env LINE_CLI_TEST_SYSTEMD_CREDS=1 \
  LINE_CLI_TEST_HEADLESS_FIXTURE=/home/lineprobe/headless-fixture \
  LINE_CLI_TEST_HEADLESS_MODE=enroll \
  /usr/local/libexec/line-session.test -test.v -test.run '^TestLinuxHeadlessPersistentFixture$'
```

After reboot, execute the same command with mode `verify` and
`LINE_CLI_TEST_EXPECT_REBOOT=1`, first from a system service with `User=lineprobe`
and `UMask=0077`, then from a real cron job. Use a readable working directory
and stagger the jobs to avoid intentional session-lock contention. Verify that
the user manager is inactive. The fixture checks saved tokens, E2EE material,
generation, checkpoint and sequence, then persists and reloads the next sequence.
It overrides HOME/XDG inside its explicitly supplied disposable directory.

For negative checks, mode `denied` requires a helper/broker rejection; timeout,
filesystem denial and successful decryption do not count. In the disposable VM
only, test a readable copy owned by another UID, stop/restore the credential
socket, and move/restore the VM host secret with guaranteed cleanup. Follow each
original-UID negative check with `verify`. Do not run disruptive broker/host-key
tests on a personal or shared machine. Enrollment may initialize the VM's global
host secret. Fixtures and VM images must remain outside version control.
