# Ticket 05: use authenticated remote app-servers

Verification date: 2026-10-17.

Tested SDK implementation commit: `86e4b30d5e6b8f72f08317cc903fd7e9b4a98b1f`.

The final source and test tree passed full normal/race, five-repeat normal, and
verification gates, then was committed unchanged at that identifier. This
following documentation-only change records the exact tested revision and adds
README, CI, evidence, and inventory documentation. SDK source and tests are
unchanged; no hosted CI pass is claimed.

The official protocol target remains
`d27764b82f7118f674371e6d6e76271d9d606edb` (workspace version `0.160.1`). The runtime
transport dependency is `github.com/coder/websocket` v1.8.15, source revision
`9c8faadccd1b679e811a79ce506f8a10237251ad`, licensed under ISC. Its exact module
checksums and preserved license are in [dependency provenance](../protocol/dependencies.json).
The [canonical compatibility record](../compatibility.md) remains the complete
170/11/85/1 method inventory and 23 transport/capability controls, with distinct
implementation, unit, mock, race, and live evidence.

No real Codex binary, account, real credential, paid model, production TLS
terminator, or official upstream test suite was used. The new CI workflow is
configured but has not run on a hosted runner during this work.

## Implemented public boundary

- `NewRemote(ctx, RemoteOptions)` accepts the URL, explicit nonempty bearer token,
  initialization parameters, limits, optional TLS configuration, and plaintext
  opt-in before any dial. One explicit construction opens one connection and
  completes `initialize` followed by `initialized` before returning. The earlier
  caller deadline or `CallTimeout` bounds dialing plus initialization. Canceling
  the constructor context after success does not close the client.
- The same typed P0 methods, raw `Call`, subscriptions, `Requests`, opaque answer
  handles, native identities, dispatch evidence, and loss/cleanup semantics work
  over WS/WSS. `RemoteOptions` introduces no process ownership, durable session
  store, high-level turn runner, reconnect, replay, login, or token refresh.
- A complete text message contains exactly one JSON envelope without a JSONL
  delimiter. Fragmentation and Ping/Pong are transport controls, not extra JSON
  boundaries. Multiple objects, split JSON across messages, invalid frames,
  unsupported binary, and excessive complete-message size terminate explicitly.
  The official server drops unsupported inbound binary; the SDK's received-binary
  failure is deliberate and agrees with the pinned official test client's policy.
- Every new explicit client initializes again. Resume sends the saved native
  thread ID unchanged and never falls back to starting a replacement thread.
  Disconnect after possible dispatch is outcome-unknown and sends no retry.
  Local context cancellation or a successful interrupt RPC is not proof that
  server execution stopped.
- Remote `Close` closes only its socket, settles calls/subscriptions/handles, and
  releases the reader and writer without waiting for application consumers. It
  sends no daemon shutdown and never kills the remote service. A listener remains
  available to another explicitly initialized client.

## Authentication and security choices

The bearer is sent exactly as `Authorization: Bearer <token>`. URL user
information, query strings, fragments, and tokens embedded in the URL are
rejected before transmission. Empty or invalid bearer syntax is rejected. No
Origin header, environment credential lookup, default HTTP client mutation, or
ambient HTTP proxy is used. All HTTP redirects are refused, including same-origin
redirects, so the credential cannot be forwarded to a second request.

WSS verifies the certificate and hostname. A caller-supplied trust pool allows a
private CA without disabling verification. TLS configuration and roots are
cloned; TLS versions below 1.2 are not enabled and `InsecureSkipVerify` is rejected.
Without `AllowInsecureWS`, plain WS requires both a `localhost`/numeric-loopback
URL and an actual connected loopback TCP peer, checked before sending the header.
The explicit opt-in permits plaintext traffic to other hosts and can expose the
credential and protocol data. It does not make that network safe.

`ErrRemoteTransport` exposes safe stage/status information without peer response
bodies, URL, bearer, or close reason. SDK error surfaces also scrub reflected
bearer credentials, including supported URL/JSON-encoded echoes and JSON string,
key, or number occurrences. This is a deliberate exception to raw RPC error
fidelity: changed diagnostic lexical values are replaced while unaffected bytes,
duplicate members, numeric lexemes, native error code, and RPC correlation remain.
Unsafe reflected arbitrary error causes are not left reachable through `Unwrap`;
SDK sentinel classification is retained. Successful results, notification data,
and request data are still caller-sensitive payloads, not universally redacted
logs. The SDK does not control server logging or token issuance policy.

## Public-contract scenarios

`TestRemotePublicContractParity` reuses **52 transport-neutral test bodies** from
tickets 01–04 over both real loopback WS and synthetic-trust WSS sockets. The
fixture bridges WebSocket messages to the independently authored fake child's
JSONL messages; the fixture alone owns that helper process. The SDK never sees
fixture internals. Process ownership, inherited pipes, and stderr checks remain
stdio-specific rather than being relabeled as remote evidence.

The shared cases cover exact tagged IDs and correlation; startup/native thread
methods; pre-write cancellation and post-dispatch unknown outcomes; no replay;
early/stale/interleaved events; typed/raw payload preservation; explicit loss and
slow-subscriber isolation; strict resume identity and unsubscribe independence;
bounded reverse requests; duplicate, canceled, expired, resolved, reused-ID, and
stale-connection handles; one-shot concurrent answers; and reader/Close progress
when application work is stalled.

Additional transport-specific scenarios are separate:

1. `TestRemoteNativeWorkflow` performs initialize → thread start/read → subscribe
   and turn start/interrupt → answer a native approval → unsubscribe → close on
   one authenticated connection, then explicitly initializes another client and
   resumes the same native thread ID. It runs for WS and WSS.
2. Three remote backpressure/ownership tests confirm a received message byte
   before cancellation, timeout, or Close. Calls and answers remain
   outcome-unknown after partial dispatch, never complete or replay later, and
   cannot keep the client alive. Queue rejection leaves a handle reusable;
   admitted-but-unwritten cancellation consumes it without sending a late reply.
   Concurrent closes leave the listener available.
3. Thirteen security tests cover exact synthetic bearer/no-Origin headers,
   trusted/untrusted certificates and caller configuration isolation, invalid
   options before dial, same/cross-origin redirects, reflected credentials in
   handshake/close/native errors, decoding errors, constructor cancellation and
   deadlines, stalled TLS, malformed held-open upgrades, raw RFC 6455 violations,
   and complete-message framing/limits. Direct byte fixtures cover malformed
   opcodes, continuation/control frames, masking, UTF-8, and close codes rather
   than relying only on well-formed library writes. Masked and nonminimal-length
   cases carry valid native JSON, proving rejection at the framing boundary.
4. `TestRemoteValidPrefixSurvivesMalformedFrame` sends a valid response and event
   followed by each of four malformed header forms in one socket write, for WS
   and WSS. It preserves the definitive reply and queued event before exposing
   terminal `ErrProtocol`, including a high-bit extended length.
5. `ExampleNewRemote` is an external-package compiled example. It has no output
   directive, so tests do not execute its placeholder endpoint or credential.

The exact test files, names, and shared case list are in
[evidence.json](../protocol/evidence.json). The final protocol unit and race package results are recorded separately;
historical model evidence remains attributed to its original SDK revisions. No protocol schema
or generated model is changed by this ticket. The bindings remain loss-preserving
models, not strict enforcement of every native JSON Schema constraint.

## Review findings and regressions

Review-driven regressions cover reflected credentials in native RPC diagnostics,
duplicate-key diagnostic JSON, invalid raw WebSocket frame classification, and a
malformed HTTP 101 upgrade whose body is held open by the peer. The fixes preserve
unrelated diagnostic bytes, return `ErrProtocol` for invalid framing, and keep
socket cleanup bounded by the constructor context even while the library drains
a rejected upgrade body. A separate streaming guard rejects masked server frames
and nonminimal/high-bit extended lengths tolerated by the dependency, following
[RFC 6455 sections 5.1–5.2](https://www.rfc-editor.org/rfc/rfc6455#section-5.1)
without copying implementation code. A later read-only review also found that an
invalid header coalesced after a valid response in one socket read could discard
the valid prefix. The guard now returns that prefix and retains a sticky protocol
error for the invalid suffix, so definitive response evidence is not lost. A
direct public regression covers that ordering. These checks use independent fake
peers, not live Codex.

## Verification

All Go commands use Go 1.26.9 with `GOTOOLCHAIN=local`:

```sh
mise exec go@1.26.9 -- go test -count=1 ./...
mise exec go@1.26.9 -- go test -race -count=1 ./...
mise exec go@1.26.9 -- go test -count=5 ./...
mise exec go@1.26.9 -- go vet ./...
mise exec go@1.26.9 -- go mod verify
python3 docs/protocol/verify_inventory.py path/to/exact-official-source-bundle
git diff --check
```

All final working-tree gates passed:

- Uncached normal: exported-client package 17.150 seconds; protocol unit package
  0.071 seconds; examples compiled.
- Five normal repetitions: exported-client package 85.144 seconds; protocol unit
  package 0.326 seconds; examples compiled.
- Uncached full race: exported-client package 128.342 seconds; protocol unit
  package 1.461 seconds; examples compiled.
- Vet, module verification, exact-source inventory, reproducible generation, and
  whitespace checks passed.

The coalesced-prefix regression also passed one focused normal run and twenty
focused race repetitions. These supplement the final full gates rather than replace them.

Earlier verification history is retained: a full race run passed in 128.9 seconds,
but a concurrent five-normal run exposed two timing issues. Those changes
invalidated treating that earlier race pass as the final gate:

- Late-response tests had equated a peer receiving the request with the local
  writer having settled. They now use a later echo barrier before testing
  cancellation and reuse. Cancellation during an actually active write may still
  close the connection; dedicated blocked-write tests retain that contract.
- An HTTP transport timeout could precede publication of `ctx.Err()` and lose
  `context.DeadlineExceeded` classification. Dial failure handling now also
  preserves safe cancellation/timeout evidence from `errors.Is` and `net.Error`.

Thirty focused cancellation/context repetitions and five framing/queue
repetitions passed after those changes. Focused passes and independent review
do not replace the final whole-tree gates. A
subsequent normal run passed in 17.179 seconds before the coalesced-read valid-prefix
fix; it is superseded by the final normal/race results above.
The exact-source inventory check passed:
43 original official files retain their Git blob/SHA-256 hashes, registry entries,
and schema memberships. Dependency/license checksums and CI YAML structure have
also been checked. The final documentation update passed the whitespace check.

The CI workflow pins official GitHub Actions to immutable revisions and uses
Go 1.26.9 for normal tests, race tests, vet, dependencies, and the offline inventory
check. It has read-only repository permission, no persistent checkout credentials,
no service secrets, and no live-model step. Configuration is not a hosted CI pass.

## Finite limits and unverified behavior

The public defaults remain 8 MiB JSON payloads; 32 queued outbound frames; 128
pending calls; 32 subscriptions with 128 events each; 128 active reverse requests
and 32 queued reverse requests; 5-minute local handle expiry; a 64 KiB diagnostic
tail; 30-second call/startup timeout; 10-second blocked-write timeout; and a
2-second stdio shutdown budget. Earlier caller deadlines win. The HTTP upgrade
response header bound is 64 KiB. Smaller per-connection `Limits` can be supplied.

WSS fixture success is mock transport evidence only. It establishes no real
Codex/server deployment compatibility, supported-version range, real bearer
issuer behavior, production proxy behavior, remote durable history, active-turn
continuation after disconnect, model access, or legacy production request
emission. There is no automatic recovery, state reconstruction, or final-text
aggregation. Callers own reconciliation after uncertain outcomes and their own
policy decisions before answering a request.
