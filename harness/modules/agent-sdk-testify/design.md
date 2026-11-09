---
type: Design
title: SDK-owned simulation with the embedded testify library
status: stable
---

# SDK simulation

The SDK owns simulation, its Go driver, business assertions, policy comparison,
parallelism, cancellation, coverage and the authenticated loopback bridge used
for remote close. The exact pinned `agent-sdk-testify` Go module owns case and
binding contracts, official drivers, recording/verification, material
qualification, strict replay, history materialization, preparation, export and
typed execution evidence.

SDK test and simulation processes link the library in-process. The complete
transport and dependency contract is in [Go library mode](library-mode.md).

<a id="story-官方-sdk-仿真测试"></a>
<a id="story-录制生命周期"></a>
<a id="story-场景配置"></a>

## Lifecycle and configuration

Simulation prepares one caller-owned fixture per provider/policy and case or
external recording. Policy comparison runs in separate SDK host processes, but
each stdio host owns its in-process mock. Remote-close instead owns both mocks in
the simulation process behind the authenticated bridge. Each host receives a
new absolute output directory and never mutates a recording or prepared source.

The preparation result must be schema 5 with an embedded module identity,
material source identity, enabled mock route, execution environment, workspace,
protocol cwd and declared connection set. Validate all of those values and the
exact module pin before protocol I/O. A missing or mismatched value fails
closed.

For each declared connection:

1. Start `StartMockAgent` and read the immutable `ExecutionStart`.
2. Construct the SDK stream client from the actual execution paths,
   environment and native arguments.
3. Consume from `FullConsumption` and `Done`; at full consumption, half-close
   SDK input with `CloseInput`.
4. Finish the public SDK business flow while final server frames remain
   readable.
5. Close the Session/Client, wait for the mock run and read the typed execution
   report for the exact execution ID.

The driver performs typed public Session/Client/history operations and retains
native answer handles. It emits business results and deliveries. A policy
comparison happens only after both independent hosts complete.

Remote-close uses the SDK-owned authenticated loopback main/probe forwarding.
The bridge adapts WebSocket frames to the same directional stream endpoints.
The mock remains in the simulation process. Extra connections are rejected;
both owned connections are closed, drained and checked against their reports.

<a id="内置材料与下游直接使用"></a>

## Material ownership

Prepare is the only material source entry. Export to a new directory outside
the prepared fixture is the only assertion-input source. Validate the complete
export inventory, plan/source-plan relation, native-stream hashes, manifest-bound
saved history, report, official origin and connection set.

SDK assertion projections are INTERNAL SDK data derived from the public export.
The projection may decode a synthetic envelope or copy an
`agent-mock-jsonl` source, but it retains both source and projected hashes.
Runtime paths remain distinct from exported paths. Opaque history IDs never
become filesystem paths.

This repository does not copy library sources, recordings, scenarios or official
drivers. It calls only public prepare/open/export, stream-session and typed
report operations. It never traverses the library's private material or
execution storage. An external recording is passed only to those public
operations.

<a id="story-录制限制"></a>
<a id="story-严格离线回放"></a>

## Strict replay

The library validates the recorded client/server exchange, preserves unknown
native fields, number lexemes, IDs and original bytes, and reports full
consumption before accepting input EOF. The SDK must not infer completion from
a business event, a successful write or reader EOF.

Strict replay requires all of the following:

- every declared connection reaches a terminal state;
- full consumption is observed;
- SDK input is half-closed after full consumption;
- client EOF is observed;
- no native executable was launched for the embedded connection;
- the replay's failure class is empty; and
- owned resources and evidence finalization complete.

An intentional scripted disconnect remains a failed replay. Expected-failure
tests assert its typed failure and cleanup facts without relaxing matching.

<a id="story-数据格式与存储"></a>
<a id="story-结果与错误"></a>
<a id="story-版本责任与证据"></a>

## Evidence and errors

Persist the embedded module version/sum/revision and Go build identity, SDK
source/build identity, prepared schema-5 identity, source and export hashes,
runtime/export separation, projection provenance, SDK results and deliveries,
typed connection reports and cleanup results outside recordings.

The business result and the protocol/evidence result remain separate. A business
pass stays visible when later replay, evidence, bridge or cleanup validation
fails, while the overall run fails. A business result does not erase a protocol
failure.

Typed reports bind the fixture, execution, material and every declared
connection. Evidence validation must bind exactly the execution that used the
exported material, even if SDK projection files are later unavailable. Never
weaken strict matching or change production SDK behavior to make a fixture pass.

Cleanup uses a separate bounded context and removes every successful prepared
fixture, including after export/projection failure, business failure and
cancellation. Missing, corrupt or mismatched evidence fails closed.

Complete coverage has 36 cases across both providers, represented as 72
provider/case slots. Source verification, required/optional policy and current
SDK simulation status remain separate. Required gaps fail; optional business
gaps remain visible; source corruption fails regardless of policy.

An explicitly selected synthetic input cannot promote source qualification or
fill real recording coverage. Never rewrite the source index or discard trusted
material after SDK failure. A single external recording can pass only its own
slot; the other slots remain unverified and a full gate remains blocked when
required material is missing.

<a id="审核采用与固定回归门禁"></a>

## Adoption and regression gates

The exact dependency and in-process transport acceptance are defined in
[Go library mode](library-mode.md). Simulation acceptance adds the provenance
and coverage rules above: the offline gate preserves all 36 cases and 72 slots,
fails missing required material, and keeps optional gaps visible. It also
rejects retired process-oriented implementation and documentation surfaces.

A synthetic fixture, a protocol peer or a code integration test cannot fill a
real-material slot. The gate preserves failure evidence and leaves the source
index unchanged.

<a id="测试与依赖"></a>

## Tests and dependencies

`internal/mocktest` is the shared test-only bridge. It imports the pinned
library, prepares and opens fixtures, creates directional streams and exposes
typed evidence. It contains no protocol matcher, imports neither native SDK and
does not depend on `os/exec`.

Native tests use `codex.NewStreams` and `claude.OpenStreams`. Synthetic capture
input remains SDK-owned and is passed to library prepare. Existing synthetic
protocol peers remain valid for non-replay contract tests but are not
simulation evidence.

The module-graph gate proves that production packages do not import testify.
The identity test proves the embedded version, checksum and revision match
`go.mod`/`go.sum`. The removed-surface gate scans code and harness documentation
for retired process contracts.

Real Agent/model execution and new recording remain explicit opt-in work outside
default validation.
