# Headless storage engine

Phases 2/3 implement the envelope, systemd adapter, persistent Linux resolver,
headless login consent, local status, and logout cleanup receipts. Fresh native
storage remains the default. `login --headless` explicitly enrolls host-only
storage; existing protection is preserved on reauthentication. Migration remains
a subsequent phase. Do not manually replace session files to change backends.

## Envelope version 1

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

## Systemd adapter

The adapter resolves `/usr/bin/systemd-creds` directly and checks that its target
and parent directories are root-owned and not writable by group/other users.
It rejects root execution and helper versions outside the inspected 256–259
range. Versions 257 and 259 have native disposable-VM evidence; 256 and 258 are
source-compatible candidates, not separately runtime-certified platforms.
Operational readiness still requires a successful actual roundtrip.

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
the future caller. TPM requests must actually return the matching TPM scheme;
accepted command-line options are insufficient. Auto-selection and automatic
fallback are not used. Signed-PCR metadata is recognized by the parser but the
adapter rejects its use pending evidence. The engine makes no claim that a TPM
is physical, that hardware binding proves verified boot, or that reboot access
has been tested for a newly enrolled user's machine.

The credential parser is independently written from the systemd v257 wire
layout (LGPL-2.1-or-later source provenance is retained in the code). Its IDs and
layout checks come from the reviewed [Phase 0 experiment](../../scripts/systemd-probe/README.md).
It performs no TPM sealing or systemd credential cryptography itself.

## Lifecycle and validation

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
will use a separate transaction. Stop older commands and watchers before changing
formats or lock conventions.

Linux logout uses an 80-byte private receipt: the 15-byte `LINECLI\0LOGOUT\0`
marker, one backend byte (1 native / 2 headless), the 32-byte SHA-256 digest of the
session file, and a 32-byte SHA-256 checksum of the preceding receipt bytes. The
checksum detects corruption; it is not an authentication claim against same-user
malware. Receipt creation, session removal, and receipt removal use durable file
operations. A receipt survives native-key cleanup failure or uncertain removal;
operations cannot create a new session while it remains. Logout verifies native
key removal using non-unlocking search, because clear's missing/unlocked-item
result alone cannot prove a locked key was deleted. An absent session without a
receipt cannot authorize deletion of an orphaned native item.

Native read-only status uses libsecret search without `--unlock`, or a per-query
macOS LAContext with interaction disabled. Linux/macOS native write status remains
unknown with an explicit interactive-check requirement. Windows DPAPI and headless
write status use separate probes. See [CLI.md](../../CLI.md) for user-facing
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
