# Security changes (unreleased)

- Generic encrypted files keep attachment key material and filenames exclusively
  in the authenticated Letter Sealing payload. Historical downloads still accept
  compatible old envelopes with conflict checks.
- Encrypted native image sending is disabled before uploads or key registration:
  the existing image/preview representation reuses an AES-CTR encryption stream.
  Generic encrypted `--file` remains available for original bytes.
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
Validation uses synthetic data; no live LINE sends or interoperability checks
were performed for these changes.
