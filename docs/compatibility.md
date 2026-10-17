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

The implemented slices own one explicitly selected local child process and expose
stdio framing, the `initialize` / `initialized` handshake, typed `thread/start`,
`thread/read`, `thread/resume`, `thread/unsubscribe`, `turn/start`, and
`turn/interrupt`, raw `Call`, bounded local subscriptions, one-shot reverse-request
answers, and bounded cleanup. Protocol models cover the complete selected
experimental-schema definition closure, with omitted/null/value handling and
unknown-field and unknown-union preservation.

Thread/turn methods expose their complete selected parameter/result shapes.
Interactive approvals and user input can now be answered explicitly. Other
capability-dependent workflows (attestation, elicitation, dynamic tools, accounts)
remain caller-managed through raw extensions and are not certified by typed
request support. Do not infer their support from reachability or generated fields.
`turn/interrupt` exposes the native request and acknowledgement; it does not prove
execution completion.

`ResumeThread` strictly checks the supplied native thread ID against the returned
ID, retaining correlation and responded-error evidence on mismatch. It never
falls back to `thread/start`, replays a failed request, or reconnects. Native
history/path selectors can deliberately return another identity; those native
workflows are outside this typed wrapper's strict-ID contract even though their
full parameter/result schemas are exposed. The raw escape hatch does not confer
support. `UnsubscribeThread` sends one native request and returns its status; it
is independent of local subscription cancellation and never sends an interrupt.
Two fake connections establish explicit reinitialization and unchanged native-ID
handling, not real-server durable history or persistence compatibility.

Subscriptions preserve all valid notifications as raw method and parameter JSON.
Seven methods also have typed payloads: `turn/started`, `turn/completed`,
`item/started`, `item/completed`, `item/agentMessage/delta`, `error`, and
`serverRequest/resolved`. Malformed known payloads retain their raw bytes with `DecodeError`. A subscriber's full queue
terminates only that stream with an inspectable loss cause outside its queue.
There is no filtering, replay, native-ID replacement, or event-driven turn state.
The remaining notification inventory rows are partial raw receive paths, with no
per-method typed/workflow evidence unless explicitly recorded. Envelope emission
timestamps are not exposed.

Reverse requests expose immutable method/parameters/exact tagged IDs and opaque
one-shot handles. Modern command/file/permissions approvals, user input, and two
legacy approvals have complete typed payload/results; every other method remains
raw. Legacy approval schemas are modeled, but the pinned modern production
handlers do not establish those legacy workflows as live-supported. Legacy
conversationId has the native ThreadId type and is used for resolution correlation
if a matching notification is received. Queue/active-capacity exhaustion explicitly
terminates the connection without
inventing an approval or denial. Local cancel/expiry does not cancel server work.
Native resolution matches both requestId and thread identity; an answer already
admitted wins over later local cancellation, expiry, or resolution.

There is no WebSocket, Unix-socket, remote-control, embedded-runtime, turn runner,
or login management in these slices. A generated type or a successful raw `Call`
does not change an endpoint's support status. Unknown extensions are caller-managed and uncertified.

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
`serverRequest/resolved` is a server notification sent after answers or pending
request resolution during interruption; it is not a success acknowledgment.
Errors after possible dispatch
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
