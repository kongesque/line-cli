# Changelog

User-visible changes are recorded here. Development-branch documentation may
describe options that are not yet in the latest release; use `line version`
and the documentation at your installed version's tag.

## [Unreleased]

### Upgrade notes

- Stop commands, watchers, and services that restart watchers before upgrading.
  Running old and new versions concurrently is unsupported.
- CLI sends prefer Letter Sealing. When LINE explicitly reports no E2EE support,
  sends proceed over HTTPS/TLS without confirmation; LINE can read this content.
  Use `--require-e2ee` to block non-E2EE sends, including in scripts and JSON mode.
- File media capability is checked independently of text encryption. A chat
  with encrypted text may support only non-E2EE files. Unsupported or unknown
  capabilities never justify bypassing the check.
- Encrypted native image sending is disabled because image and preview reuse
  an encryption stream. Automatic non-E2EE delivery cannot override this restriction.
  Generic `--file` preserves the original bytes, subject to file capability
  checks. Non-E2EE native images require explicit capability.
- Source builds require the reviewed **Go 1.27.2** compiler; module, build guard,
  and CI/release pins agree on this version.

### Added

- Add `send --require-e2ee` for strict encryption policy. Keep `--allow-non-e2ee`
  and `--allow-plaintext` as compatibility options (default `true`; `false`
  requires E2EE). Strict mode takes precedence. Non-E2EE human receipts say
  `transport encrypted (no E2EE)`; JSON continues to report `encrypted: false`.
- Command deadlines and cancellation across core commands with `--timeout`.
- Opt-in structured errors using global `--error-format=json`, with individual
  mutation-step outcomes and sanitized request diagnostics.
- Available-history pagination with `messages --page --json` and opaque cursors;
  bounded lookup of older reply, download, reaction, and unsend targets.
- Detailed message/chat JSON using `--details --json`, with optional sender-name
  resolution, decoded content, and explicit unavailable values.
- Group member/invitee listing and explicit UTF-16 mention spans for text sends.
- JPEG/PNG image preparation and non-E2EE sending with capability checks. Encrypted
  native images remain disabled and native-image live interoperability is unverified.

### Security

- Generic encrypted files retain attachment key material and filenames only in
  the authenticated Letter Sealing payload. Compatible historical envelopes
  remain readable with conflict checks.
- Authenticated requests reject redirects, and network responses and SSE events
  have explicit size budgets. Mutations are never automatically replayed.
- Source and release binaries are scanned with a pinned version of govulncheck.
  Release executables retain symbols needed for binary analysis.
- Live encrypted-file interoperability remains unverified. The authorized live
  file roundtrips verified plaintext delivery only. See the
  [security changes and validation record](docs/SECURITY.md#security-change-record).

### Documentation and packaging

- Align English, Thai, Japanese, and Traditional Chinese guides on encryption
  policy, image restrictions, pagination, build requirements, and navigation.
- Add troubleshooting tables and a maintainer release checklist.
- Consolidate scattered references into `docs/JSON.md`, `docs/SESSION.md`,
  `docs/SECURITY.md`, and `docs/DEVELOPMENT.md`; remove their superseded copies.
- Bundle documentation at its current paths, including translations and linked
  reference pages, instead of referencing the removed root `CLI.md`.

## [0.4.0] - 2026-09-28

- Add `line update` with release checks, installation detection, SHA-256
  verification, and standalone upgrades.
- Add direct contact lookup by MID, including users outside the friend list,
  with validation and partial-result handling.
- Update multilingual CLI, Homebrew, and upgrade documentation.

For earlier versions, see [GitHub Releases](https://github.com/kongesque/line-cli/releases).

[Unreleased]: https://github.com/kongesque/line-cli/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/kongesque/line-cli/releases/tag/v0.4.0
