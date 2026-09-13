# Phase 0 native runtime evidence — 2026-09-13

These are synthetic credential experiments in disposable ARM64 VMware Fusion
VMs. No LINE session, personal keyring, or UniPi was used. Production code is
unchanged. Results certify only the tested behavior and configuration.

## Debian 13

- Official `debian-13-generic-arm64.tar.xz` cloud image, image listing dated
  2026-08-31; SHA-512 checked against the publisher's HTTPS checksum file.
- Debian 13.6, kernel `6.12.107+deb13-arm64`, systemd
  `257.13-1~deb13u1`, Go probe built with Go 1.27.1 for Linux arm64.
- EFI VM, two CPUs, 2 GiB memory; no firmware TPM. Administrator installed
  `open-vm-tools` and `cron`, and enabled `systemd-creds.socket`.
- Test account `lineprobe` UID 1001; comparison account `otherprobe` UID 1002.
  Neither account had a desktop, login session, or lingering user manager.
  `user@1001.service` remained inactive, including after reboot.
- Helper environment contained only the allowlisted values in `helper.go`.
  The account had no direct access to `/dev/tpmrm0` and no sudo permission.

| Check | Observed result |
| --- | --- |
| Minimal environment, `auto` | Verified `host-user`; exact 32-byte round trip |
| Explicit `host` | Verified `host-user` |
| Explicit `host+tpm2` with no TPM | **Returned `host-user` successfully; requested mode did not match** |
| Wrong name and modified GCM tag | Helper rejected each; intact blob then still decrypted |
| Another UID with its own readable fixture copy | Helper rejected; original UID still decrypted |
| System unit with `User=lineprobe` | Same enrolled fixture decrypted |
| Real cron daemon | Same enrolled fixture decrypted |
| Unit with socket in `InaccessiblePaths=` | Helper rejected |
| Stopped credential broker socket | Helper rejected; resumed socket restored access |
| Temporarily removed VM host secret | Existing fixture rejected; restoring the original secret restored access |
| Reboot, boot-time system unit | Same pre-reboot fixture verified; different Linux boot ID |
| Reboot, real cron daemon | Same pre-reboot fixture verified; different Linux boot ID |

The broker uses `Accept=yes` and per-connection service instances. There is no
singleton `systemd-creds.service` to stop on this distribution. Socket denial,
absence and host-secret experiments were confined to this disposable VM, with
success controls before and after the failure checks. A timeout or unreadable
fixture is not accepted as helper rejection.

### Software TPM experiment

Installed Debian packages `swtpm` 0.7.1-1.5, libtpms 0.9.2-3.2 and tpm2-tools
5.7-1+b1. The kernel's `tpm_vtpm_proxy` exposed a software TPM via `/dev/tpmrm0`.
This VM still reported no firmware TPM. The ordinary broker's encryption path
continued returning `host-user`, including for an explicit `host+tpm2` request.

An administrator therefore created a separate **synthetic test fixture**, using
`systemd-creds --uid=1001 --with-key=host+tpm2 --tpm2-device=/dev/tpmrm0
--tpm2-pcrs=7`. Its user-scoped scheme ID was
`ef4ac13679a9480ea7db68897f9f165d`, with fixed PCR mask 128. The unprivileged Go
probe parsed the actual header and verified the full decrypted random key.

- Unprivileged broker decryption succeeded without direct TPM-device access.
- Extending software PCR 7 caused rejection of the already enrolled fixture.
- Removing the software TPM caused rejection; the host-only fixture still worked.
- Restarting the same software TPM state with cleared PCRs restored access.
- After VM reboot, the original TPM fixture verified under UID 1001 with a
  different boot ID, after the software TPM service had started.

This establishes parser compatibility and functional TPM-policy failure behavior
for this software TPM. It **does not** establish unprivileged TPM enrollment,
physical TPM protection, measured/verified boot, or resistance to copying software
TPM state. Administrator sealing is a test technique, not the planned CLI UX.

## Ubuntu version matrix

Ubuntu Minimal ARM64 cloud images were verified against the publisher's SHA-256
files and booted in separate disposable VMware guests after generating an
initramfs for VMware NVMe compatibility.

| Image | Kernel | systemd | Result |
| --- | --- | --- | --- |
| Ubuntu 26.04.1 LTS (Resolute) | `7.0.0-31-generic` | `259.5-0ubuntu3.4` | Native baseline, exact round trips and negative checks passed; `host+tpm2` still returned verified `host-user` |
| Ubuntu 24.04.4 LTS (Noble) | `6.8.0-139-generic` | `255.4-1ubuntu8.17` | Rejected before encryption: user-scoped broker feature floor was unavailable and the broker socket was absent |

The Ubuntu 26.04 guest was not used to claim reboot or TPM evidence; those
checks are represented by the Debian 13 results above. Ubuntu 24.04 confirms
that the version floor is a runtime gate rather than a package-family guess.

## Reproduction boundaries

Build and run the probe as described in `README.md`. A normal probe run never
persists a key or blob. `--enroll PATH` exclusively creates a 0600 synthetic fixture
containing the sealed blob, enrollment boot ID, and SHA-256 digest of a freshly
random 32-byte key. The plaintext key is never persisted. `--verify PATH` decrypts
and checks that same key; `--expect-reboot` additionally requires a changed boot ID.
Keep fixture files and test VM disks out of the repository.

For wrong-UID testing, an administrator copies the synthetic fixture into the
other account's home with that account's ownership and 0600 permissions, then
runs `--verify PATH --expect-denied`. The original account verifies the original
fixture again. This distinguishes broker identity rejection from file denial.

For reboot testing, run a system-manager oneshot unit with `User=lineprobe` and
`--verify PATH --expect-reboot`, enabled for `multi-user.target`, and a real cron
entry with the same command. Collect only the JSON reports after reboot. A new
baseline run alone is not reboot evidence. No `LoadCredentialEncrypted=`, desktop
session, user lingering, or interactive Polkit agent was used.

## Enrollment decision

An accepted `--with-key` argument is insufficient evidence of protection. Always
inspect the exact returned envelope, decrypt it and compare the entire key before
enrollment. An explicit TPM requirement must reject the host-only results observed
here. No automatic resealing, weaker provider, or new key is permitted when an
existing fixture becomes inaccessible.

Physical TPMs, signed PCR policies, and Raspberry Pi/UniPi are unverified. Keep
those claims gated independently of the host-only results. In particular, this
software TPM configuration is not an approved unprivileged TPM enrollment target.

Image provenance: [Debian cloud images](https://cloud.debian.org/images/cloud/trixie/latest/).
