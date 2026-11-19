# Square Transport Foundation

The initial Square implementation is transport-only. It uses Apache Thrift
Compact Protocol and the existing `pkg/line.Client` HTTP/session lifecycle.

`CallRead` accepts only a `SquareReadOperation`; it does not accept a URL,
method, or arbitrary callback. The private operation registry is intentionally
empty until endpoint, exact path, method, and argument schema are verified.
Consequently, authenticated Square network calls are denied by default.
`Manager.DoSquareReadContext` uses the existing session credential and refresh
lifecycle while accepting only the registered operation identifier.

## Authentication handling

HTTP 401/403 responses remain compatible with the existing `ResponseError`,
`IsAuthError`, refresh, and invalidation behavior. Standard Thrift application
exceptions are decoded as typed errors but are not classified as authentication
or logout responses. Other result fields, including any possible service
exception struct, remain opaque until their schema is verified.

The repository does not contain verified Thrift service exception field IDs or
service codes for auth mappings. No field-ID interpretation or auth mapping is
made.

## Rust compatibility

No Rust-generated Square fixture or Rust implementation artifact was available
in this repository. `TestCompactProtocolGoldenBytes` now anchors generic
Compact Protocol v1 request/reply bytes using independently specified fixture
bytes; it does not claim `line-rs` compatibility. Compare these and operation
fixtures with Rust once source or generated output becomes available.

## Protocol uncertainties

- The verified Square endpoint and first read operation are unknown; the
  allowlist therefore denies all Square requests.
- The exact result struct field IDs and service-exception schema are unknown.
- Authentication headers beyond existing Chrome headers are not inferred.
- List, map, set, UUID, and binary coverage may require expansion after audit.
