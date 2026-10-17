# Compatibility

This is the canonical compatibility record for this SDK. The complete per-method
matrix is [capabilities.json](protocol/capabilities.json); its records are part of
this document, not a separate supported-version claim.

## Protocol target

- Official repository: [openai/codex](https://github.com/openai/codex).
- Exact revision: [`d27764b82f7118f674371e6d6e76271d9d606edb`](https://github.com/openai/codex/tree/d27764b82f7118f674371e6d6e76271d9d606edb).
- Workspace version label: `0.160.1`, verified in the pinned `codex-rs/Cargo.toml`.
- This target is not a version range. Other revisions, including other releases
  carrying similar version labels, have unknown compatibility.
- Original source hashes, extraction details, and notices are recorded in
  [protocol provenance](protocol/README.md) and [sources.json](protocol/sources.json).

## Current implementation

The first slice owns one explicitly selected local child process and implements
stdio framing, the `initialize` / `initialized` handshake, typed `thread/start`
and `thread/read`, raw `Call`, and bounded cleanup. Protocol models cover the full
selected experimental-schema definition closure, with explicit omitted/null/value
handling and preservation of unknown object fields and union variants.

`thread/start` and `thread/read` are **partial workflows**: their request/result
bindings exist, but event subscription and reverse-request answers do not. Incoming
notifications are counted and discarded. An incoming server request fails the
connection with `ErrUnsupportedRequest`; the SDK never invents an approval or
silently accepts work. Do not enable capabilities that require attestation,
elicitation, or dynamic-tool callbacks in this slice.

There is no WebSocket, Unix-socket, remote-control, embedded-runtime, turn runner,
resume/unsubscribe wrapper, login management, or execution-interruption workflow
in this slice. A generated data type or a successful raw `Call` does not change
an endpoint's support status. Unknown extensions are caller-managed and uncertified.

The inventory enumerates **170 client requests, 11 server requests, 85 server
notifications, and 1 client notification**, including experimental, legacy, and
internal registry entries. It also records 23 framing, transport, and capability
controls. It covers the app-server surface, not every Codex CLI command, model API,
or MCP endpoint. The stable aggregate schema has 104/10/83/1 entries and is not a
complete inventory. Even the experimental aggregate omits three legacy requests
and two internal notifications; the exact Rust registry supplies those entries.

### Status and evidence are different

- **Supported**: the stated SDK behavior is implemented. This does not imply a
  real server was tested or every capability-dependent workflow is available.
- **Partial**: only the explicit subset in the record is implemented.
- **Unsupported**: no supported SDK workflow; raw transport reachability does not
  confer support.

Each inventory row keeps unit, fake-peer/mock, race, and live evidence separately.
Empty evidence arrays mean no result has been recorded for that row. Evidence
records and reproducible commands belong in [evidence.json](protocol/evidence.json).
Official tests inspected as design references are described in
[testing patterns](protocol/testing-patterns.md); their existence is not test
execution evidence for this SDK.

## Wire and lifecycle boundaries

The pinned protocol deliberately omits `"jsonrpc":"2.0"`. Request identifiers
are either strings or signed 64-bit integers; integer `1` and string `"1"` are
different identities. The SDK does not coerce IDs through floating point.

A context cancels local waiting. It does not send a fictional generic cancellation
notification and does not establish that the server stopped. `turn/interrupt`
and `userVerification/cancel` are separate native methods, listed individually.
`serverRequest/resolved` is a server notification. Errors after possible dispatch
carry outcome-unknown evidence and are never automatically replayed.

Closing this client closes only its owned child connection, then bounds waiting
and forceful termination. It does not send the managed daemon shutdown protocol
or take ownership of an existing service. Arbitrary descendant process cleanup
is not guaranteed. See the exported `Limits` and README for finite defaults.

## Live validation

**Not performed.** No real Codex binary, account, credentials, authenticated
session, or live model execution was used to validate this SDK. Source inspection,
synthetic child processes, loopback fixtures, and race-detector passes cannot be
promoted to live evidence. Record an exact SDK revision, official revision/binary,
scenario, command, and result before adding future live evidence.
