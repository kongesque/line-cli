# Letter Sealing in the CLI

This is a contributor-oriented overview of the CLI's end-to-end encryption
boundaries. For commands and troubleshooting, start with the
[CLI guide](../docs/CLI.md).

Login and message encryption reuse the LINE protocol and crypto runtime inherited
from the upstream project.

## Login and keys

`internal/session/manager.go` exports the login keychain and saves it with tokens
in OS-protected storage. `pkg/line/client.go` supports E2EE login and the explicit
non-E2EE login response. The account password is never persisted.

`pkg/e2ee/manager.go`, `pkg/runner.go`, and `pkg/ltsm` implement device/group key
operations and the signing runtime. Generated LTSM code and embedded memory data
are runtime dependencies, even though some functions are invoked indirectly.

## Sending and reading

`internal/messaging/client.go` and `keys.go` negotiate peer keys, restore historical
keys, and register group keys only during an explicit send with complete membership.
Sends require encryption by default. A non-E2EE login or an explicit
unsupported-capability response permits plaintext only with affirmative terminal
confirmation or per-command `--allow-plaintext`. Scripts, JSON output/errors, and
`--stdin` never prompt. Missing keys, network errors, and ambiguous membership
stop the send. File and image media capabilities are checked separately from text.

History resolves the sender/receiver key IDs in each encrypted message. It does
not mark messages read or register group keys. Missing historical device keys can
leave an individual message unreadable.

Generic file attachments use the `emf` OBS service when encrypted and the `m`
service for plaintext. `internal/messaging/files.go` derives file keys with HKDF,
encrypts with AES-CTR, and verifies HMAC-SHA256 before saving a download. The CLI
treats media sent with `--file` as generic files. Attachment key material and
filenames stay inside the authenticated Letter Sealing payload. Live encrypted-file
interoperability remains unverified; plaintext file send/download was verified in
one authorized group and one direct chat. Encrypted native `--image` sends are
disabled before remote mutations because full image and preview reuse a CTR stream.
Plaintext native images require explicit capability and per-send consent, and
native-image live interoperability remains unverified. See the
[security validation record](../docs/SECURITY-CHANGES.md).

Mutations are never automatically replayed after an uncertain response. The
`group_key_registered` output field reports a new registration during that send;
`false` does not mean an encrypted group message lacks a key.
