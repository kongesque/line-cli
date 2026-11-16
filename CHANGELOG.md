# Changelog

User-visible changes are recorded here. Development-branch documentation may
describe options that are not yet in the latest release; use `line version`
and the documentation at your installed version's tag.

## [Unreleased]

## [0.7.0] - 2026-11-16

Sessions that keep working: token refresh no longer strands a session a few
days after login, a known device can repeat email login without a phone PIN,
and builds use Go 1.27.4.

### Upgrade notes

- Stop commands, watchers, and services that restart watchers before
  upgrading. Running old and new versions concurrently is unsupported.
- If your session already fails a few days after login with "check your
  connection" (HTTP 400, gateway code 10201), run `line login` once after
  upgrading. The fix prevents this but cannot recover a session already in
  that state.
- A rejected token refresh is now JSON `authentication_failed` instead of
  `remote_failed`. Scripts that retried on `remote_failed` should run
  `line login` instead; retrying does not help.
- `line login --email` now sends the device certificate saved by an earlier
  email login for the same address, so LINE may skip the phone PIN. Add
  `--require-pin` to always verify on your phone.
- Source builds require **Go 1.27.4**.

### Added

- `line login --email` sends the device certificate saved by an earlier email
  login for the same address, so LINE may sign you in without a phone PIN
  ([#31](https://github.com/kongesque/line-cli/issues/31)). When LINE skips
  verification, the saved Letter Sealing keys are reused only if they match the
  account's current key on LINE; otherwise nothing is saved. Certificates from
  QR login or logged-out sessions are not sent, and a failed login is not
  repeated. `--require-pin` always asks for phone verification. Checked with
  one live account on 2026-11-16: a repeat email login signed in without a PIN
  and messages decrypted afterwards.

### Fixed

- Token refresh now reads LINE's wrapped `{code, message, data}` reply as well
  as a bare token. A wrapped reply was previously discarded after LINE may have
  rotated the tokens, which could leave every later refresh rejected (HTTP 400,
  gateway code 10201) a few days after login until `line login`
  ([#29](https://github.com/kongesque/line-cli/issues/29)). Sessions already in
  that state still need `line login` once.
- When LINE rejects a token refresh, commands now say to run `line login`
  instead of "check your connection", JSON errors use `authentication_failed`
  instead of `remote_failed`, `line doctor` reports the rejected refresh, and
  `line watch` stops instead of retrying forever. The saved session is not
  marked invalid.
- The docs no longer promise a seven-day refresh interval; LINE decides when a
  refresh is due.

A live check with one account kept a QR session working for 168 hours (seven
days) with read-only checks about every 6 hours, past the 3–4 day point where
v0.6.0 failed. See the [session notes](docs/SESSION.md#refresh-follow-up-issue-29).

### Security

- Require the reviewed Go 1.27.4 compiler for source, CI, and release builds to
  address [GO-2026-6859](https://pkg.go.dev/vuln/GO-2026-6859) in `net/http`.
  The advisory affects HTTP/1 servers, which the CLI does not run, but the
  vulnerability scan reports the affected package as reachable.

## [0.6.0] - 2026-11-07

Inbox control and agent safety: mark chats read on purpose, filter unread
chats, handle group invitations, diagnose problems, and give scripts and
agents a read-only switch.

### Upgrade notes

- Reading never sends read receipts; that is unchanged. To mark a chat read,
  run the new `line read` explicitly. Others in the chat will see "Read".
- Use `--read-only` (before the command) or `LINE_CLI_READ_ONLY=1` when a
  script or agent should only read. Any `LINE_CLI_READ_ONLY` value other than
  `1`, `true`, `0`, or `false` now makes every command fail, so check that the
  variable is not already set to something else in your environment.
- Stop commands, watchers, and services that restart watchers before
  upgrading. Running old and new versions concurrently is unsupported.
- `line watch` attached to a terminal now prints readable lines instead of
  JSON. Piped or redirected output, and `--json`, keep the unchanged NDJSON
  stream. Scripts that run `watch` attached to a terminal must pass `--json`.
- `--help`, `-h`, and `line help COMMAND` now print to stdout (exit 0). Scripts
  that read help from stderr must read stdout instead. Error output is one line
  on stderr, without the full usage dump.

### Changed

- Command help lists `--flag` spellings grouped by purpose, with examples, and
  `line help COMMAND` shows one command's help. `logout` and `auth` help now
  explain what they do.
- Mistyped commands and options suggest the closest match, for example
  `line chat` (did you mean `line chats`?) and `--limt` (did you mean
  `--limit`?). Option values are never echoed in these errors.
- In a terminal, a chat name that matches no chat exactly, or several, opens a
  chooser of similar chats and contacts instead of failing. Without a terminal,
  the error lists up to five similar names with full IDs (JSON code unchanged).
  A partial or similar name is never used for a change unless you chose it.
- In a terminal, `line watch` prints one readable line per message (time, chat,
  sender, text or attachment type) with names cached. Other events are hidden
  unless you add `--verbose`.

### Added

- Read-only mode: `line --read-only COMMAND` or `LINE_CLI_READ_ONLY=1` refuses
  sends, read receipts, reactions, unsend, invitation changes, login, logout,
  storage migration, and update installs before any prompt, lock, credential access, request sequence, or
  network request. Reads, `watch`, and `update --check` keep working. Refusals
  use the new JSON error code `read_only`. Unrecognized `LINE_CLI_READ_ONLY`
  values fail every command instead of being ignored.
- `line read CHAT [--message ID]` marks a chat as read; others see "Read". It
  is the only command that sends read receipts, uses a persisted request
  sequence, and is attempted once. Without `--message` it marks up to the
  newest message from someone else. Guided mode lists unread chats first and
  asks for confirmation.
- `line chats --unread` lists only chats whose reported unread count is above
  zero. Chats with an unknown count are excluded. It works with `--search`,
  `--limit`, `--all`, `--show-ids`, `--json`, and `--details`.
- `line invites` lists pending group invitations; `line invites accept|reject
  GROUP_ID` changes one after confirming it is pending. Each change uses a
  persisted request sequence and is attempted once. Accepting never registers
  or fetches a group key. In a terminal, `line invites` lets you choose one to
  accept, reject, or skip.
- `line doctor [--offline] [--json]` checks the version and other copies on
  `PATH`, storage, the saved session, LINE sign-in (one profile read), Letter
  Sealing keys (count only), clock skew against LINE, and the watcher. Every
  non-passing check has a next step. It changes nothing, exits 1 only when a
  check fails, and prints no tokens, keys, IDs, names, or messages.
- `line completion bash|zsh|fish|powershell` prints a static completion script
  for commands, subcommands, options, and fixed values. It reads no session
  and makes no network requests.

### Fixed

- QR login now says when LINE approves a login but returns no usable Letter
  Sealing keys, with next steps and a diagnostic that contains no server data,
  instead of reporting a generic local setup failure. No session is saved.

### Security

- Require the reviewed Go 1.27.3 compiler for source, CI, and release builds to
  address [GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809) in certificate
  verification. Existing binaries need a rebuilt release to receive this fix.

## [0.5.0] - 2026-10-19

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

- Add `send --require-e2ee` to block non-E2EE sends. Non-E2EE human receipts say
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

[Unreleased]: https://github.com/kongesque/line-cli/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/kongesque/line-cli/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/kongesque/line-cli/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/kongesque/line-cli/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/kongesque/line-cli/releases/tag/v0.4.0
