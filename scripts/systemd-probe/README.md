# Headless-storage Phase 0 probe

This experiment is separate from `cmd/line` and `internal/session`. It does not
read LINE sessions, access Secret Service, contact LINE, or implement a storage
backend. All keys are newly generated synthetic 32-byte values. No key, encrypted
credential, raw helper diagnostic, or credential fingerprint is printed.
Normal runs save nothing; explicit fixture enrollment persists only
the synthetic sealed blob, boot ID, and random-key digest for later verification.

Run the native probe only in a **disposable booted Linux test environment**. Even
unprivileged `systemd-creds encrypt --user` can cause the privileged broker to
initialize the VM's system host secret. The `--disposable` flag acknowledges this
side effect; it does not create isolation. The probe refuses root and non-Linux
execution before invoking systemd. VM provisioning and package installation are
separate operator actions. Do not run it on the UniPi or another personal host
without separately arranging an appropriate test environment.

## Current evidence

| Layer | Status |
| --- | --- |
| Upstream credential format and user broker source | Inspected v257 format and v259 client; source links below |
| Bounded header parser and helper subprocess | Implemented as experimental probe code |
| Linux native user-scope round trip | Passed on Debian 13 and Ubuntu 26.04; Ubuntu 24.04 rejected |
| Wrong-user and broker-isolation runtime checks | Passed on Debian 13 and Ubuntu 26.04, including readable wrong-UID fixtures |
| Reboot with the same enrolled blob | Passed on both candidates from a system unit and real cron; Debian VM cold start also passed |
| Complete disk copy | Separate Debian VM recovered the original host-only fixture from the copied disk |
| Physical TPM / virtual TPM | Software TPM policy/decryption checks passed; physical TPM unverified |
| Production headless support | Host-only CLI enrollment, persistent selection, local status, migration and logout implemented; migration runtime release checks pending |

Local validation on 2026-09-13, macOS arm64 / Go 1.27.1: race-enabled probe tests,
a 10-second parser fuzz run, and `go vet` passed. Linux arm64 and amd64 binaries
cross-built successfully. These are development checks, not native Linux evidence.

The child-process tests exposed an existing follow-up in
`internal/session/secret_tool_linux.go`: embedding `bytes.Buffer` promotes
`ReadFrom`, allowing `io.Copy` to bypass a custom `Write` limit. The probe uses
composition and tests both stdout and stderr limits. Phase 1 corrects the native
helper separately, with a real synthetic subprocess regression test.

## Local validation and cross-build

From the repository root:

```sh
go test -race ./scripts/systemd-probe
go test ./scripts/systemd-probe -run '^$' -fuzz FuzzCredential -fuzztime 10s
go vet ./scripts/systemd-probe
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -buildvcs=false -o /tmp/line-systemd-probe-arm64 ./scripts/systemd-probe
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -o /tmp/line-systemd-probe-amd64 ./scripts/systemd-probe
```

These tests use fabricated headers and a fake subprocess protocol. They check
bounds, truncation, malformed fields, mode reporting, output limits, timeouts,
environment isolation, and required round-trip/negative-check outcomes. They do
not establish real systemd decryption or TPM security.

## Native runtime procedure

Use disposable VMs for Debian 13/systemd 257 and Ubuntu 26.04/systemd 259, plus
an older unsupported image (Debian 12/systemd 252 or Ubuntu 24.04/systemd 255).
Record actual package version, architecture, kernel, TPM type, image version and
broker setup in an accompanying report. Package family is a candidate, not an
acceptance result; maintain patched distribution packages.

In a disposable VM, have the administrator install the matching binary at
`/usr/local/bin/line-systemd-probe` and create a stable unprivileged `lineprobe`
user. Ensure `/usr/bin/systemd-creds` and its system credential broker are present.
Then run, without a desktop session or credential-directory inheritance:

```sh
sudo -u lineprobe env -i PATH=/usr/bin:/bin /usr/local/bin/line-systemd-probe --disposable
```

This is a minimal cron-like execution context, not evidence that a cron daemon
has run the command. To exercise system-manager execution under that UID:

```sh
sudo systemd-run --quiet --wait --pipe --collect \
  -p User=lineprobe -p UMask=0077 \
  /usr/local/bin/line-systemd-probe --disposable
```

Do not use `LoadCredentialEncrypted=` or give the account root/TPM permissions:
the experiment is specifically testing direct user-scoped broker access. Run
broker-absent and inaccessible-socket cases in a separate disposable VM/snapshot;
do not disrupt a host broker that other applications use. Use unit sandboxing
to test denied access, then restore the test environment and verify success.

The probe generates a new key in memory and tries `auto`, `host`, and
`host+tpm2`. For each accepted envelope it asks systemd to decrypt those exact
bytes, compares the complete returned key, checks wrong-name and modified-tag
rejection, and rechecks the intact blob afterwards. Child environments are
explicitly limited; stdin/stdout pipes carry synthetic secrets, and subprocesses
have output limits and deadlines. Unexpected process errors are sanitized.

The report separates requested mode from verified envelope type. Exit 0 means
the auto baseline was observed with successful round-trip and negative checks.
It does **not** mean requested-mode enforcement or platform certification passed:
inspect `requested_mode_matches`, every outcome, and the policy fields. A helper
timeout is not accepted as proof of tamper rejection. Unavailable explicit modes
are recorded without invalidating an otherwise verified auto baseline.

The helper uses `--user`, an explicit credential name and `--newline=no`. For
259+ it also uses `--no-ask-password` and decrypts with `--refuse-null`. Older
builds require native confirmation of noninteractive behavior; output formats
are allowlisted before invoking decryption regardless of helper defaults.


### Persistent synthetic fixtures

Inside the disposable VM, enroll once and verify the same fixture after reboot:

```sh
sudo -u lineprobe env -i PATH=/usr/bin:/bin /usr/local/bin/line-systemd-probe \
  --disposable --enroll /home/lineprobe/phase0.fixture
# Reboot the disposable VM, then execute under the same UID:
sudo -u lineprobe env -i PATH=/usr/bin:/bin /usr/local/bin/line-systemd-probe \
  --disposable --verify /home/lineprobe/phase0.fixture --expect-reboot
```

Enrollment exclusively creates a 0600 file and syncs it and its directory. It
never overwrites an earlier fixture. The file contains the sealed blob, boot ID,
and SHA-256 digest of a random 32-byte key, with an explicit synthetic-format
marker. No plaintext key is saved. A changed boot ID plus successful verification
establishes same-key recovery across a Linux reboot; it alone does not distinguish
a warm reboot from a complete power cycle.

For a wrong-user test, the VM administrator must copy the fixture to a location
readable by that user and set appropriate ownership. Run `--verify` with
`--expect-denied` there, then verify again under the original user. File access
errors and helper timeouts do not count as the expected rejection. Fixture files
are trusted experiment inputs, not a production storage format or recovery export.

Exit 0 for fixture modes means `enrolled`, `fixture_verified`, or the explicitly
requested `expected_helper_rejection`. Read the outcome; these are different
assertions. Do not commit fixture files or VM disks.

## Format contract under investigation

The independent parser recognizes only these user-scoped identifiers:

| ID (16 bytes, hexadecimal) | Classification |
| --- | --- |
| `55b9ed1d38594d43a8319d2ebb332ac6` | Host key, user scope |
| `ef4ac13679a9480ea7db68897f9f165d` | Host key + TPM2, user scope |
| `adbc4ca3efb64201ba881b6f2e4095ea` | Host key + TPM2 with signed PCR policy, user scope |

System-scoped, TPM-only, null and unknown identifiers are rejected. The narrow
profile requires AES-256-GCM sizes 32/1/12/16 (key/block/IV/tag), eight-byte header
alignment, and user scope flags 7. TPM headers have bounded blob/policy lengths,
24-bit PCR masks, SHA-1/SHA-256 banks and RSA/ECC primary algorithms. Signed
policy headers have a separate PCR mask and bounded public key. Larger or future
valid systemd formats can be rejected: this is intentionally not a general parser.

Header inspection is **not authentication**. The metadata is only reported as
verified after helper decryption and exact key comparison. The parser does not
verify boot integrity, identify a physical versus virtual TPM, or interpret
sealed TPM internals. Future production integration must preserve this boundary.

Provenance: wire IDs and layout were inspected in systemd's
[v257 header](https://github.com/systemd/systemd/blob/v257/src/shared/creds-util.h)
and [v257 implementation](https://github.com/systemd/systemd/blob/v257/src/shared/creds-util.c),
whose source carries `LGPL-2.1-or-later`. No upstream implementation is vendored;
the Go parser and fake transport are independently written experiment code.

The [v259 IPC client](https://github.com/systemd/systemd/blob/v259/src/shared/creds-util.c)
does not forward `withKey` in `ipc_encrypt_credential`, despite server-side
support. Do not infer enforcement from accepted command-line flags. Runtime
observations must determine how each distribution build behaves.

The [v258 release notes](https://github.com/systemd/systemd/blob/v258/NEWS)
also change the default fixed PCR mask to empty. Display observed fixed and
signed-policy masks separately; hardware binding alone is not verified boot.

## Remaining Phase 0 evidence

See [RUNTIME.md](RUNTIME.md) for native observations, exact versions, and the
separation between unprivileged enrollment and administrator-sealed TPM fixtures.

- VM cold-start and host-only disk-copy checks passed. A physical-device power
  cycle and TPM-bound disk-copy resistance remain unverified.
- Physical TPM enrollment, detected-but-broken firmware TPM behavior and signed
  PCR policies remain unverified. Do not enable these profiles based on parser
  fixtures, software TPM results, or capability detection alone.
- Preserve explicit protection requirements: observed helper builds may ignore
  `--with-key=host+tpm2`. Refuse a mismatching result before enrollment.

The production engine is documented in
[internal/session/HEADLESS.md](../../internal/session/HEADLESS.md). The CLI now
supports explicit host-only enrollment, local status and native-to-headless
migration. This directory contains the separate evidence probe.
