---
type: Design
title: SDK tests with the agent-sdk-testify Go library
status: stable
description: "Defines connection ownership, call order, and acceptance for Go SDK tests that use Testify for in-process replay."
tags: [testify, replay, testing]
sources:
  - id: document
    resource: "library-mode.md"
    title: "SDK tests with the agent-sdk-testify Go library"
---

# Testify library integration

Testify owns fixture preparation, native replay, material exports and typed execution evidence. The SDK owns its driver, public SDK calls, business assertions and simulation coverage. The test-only `internal/mocktest` bridge coordinates the fixture and the SDK-side streams. The linked library version and checksum come from `go.mod` and `go.sum`. When Go test build metadata omits the dependency, the bridge supplies the pinned embedded identity. Its identity test checks the version, checksum and revision against the module pin. Material provenance has its own source identity. [Module pin](../../../go.mod) · [Identity check](../../../internal/mocktest/testify_identity_test.go) · [Testify library](https://github.com/zoumo/agent-sdk-testify)

## Connection ownership and order

1. Prepare or open a fixture and register cleanup. The caller owns the prepared and exported directories. The bridge creates two directional streams and keeps diagnostics separate from the protocol.
2. Start `StartMockAgent` and read `run.Start` before creating the SDK client. Once Testify claims the connection, it owns the fixture-side endpoints. The SDK takes the opposite endpoints after opening. If startup fails before an SDK client takes ownership, the bridge reclaims its endpoints and settles any started run.
3. Construct `codex.NewStreams` or `claude.OpenStreams` with the execution overlay, complete replacement environment and validated native arguments from `run.Start`. Codex takes its protocol directory from `ProtocolCwd`. Claude uses `Workspace` and the execution's configuration scope. Keep the environment in the transport and omit its values from reports and logs. Explicitly join any additional declared connection to the same execution ID. [Bridge](../../../internal/mocktest/library.go) · [Codex streams](../../../codex/stream.go) · [Claude streams](../../../claude/stream.go)
4. Consume output while watching `FullConsumption()` and `Done()`. When Testify signals full consumption, call `CloseInput()` to close only the SDK write end. Keep reading through output EOF. If the mock stops before full consumption, use `Wait` to read its terminal result.
5. Close the SDK client or Session and settle every mock run with `Wait`. On cancellation or failure, use a separate bounded cleanup context. Abort unfinished runs to release blocked I/O. `Wait` settles the current connection. Read the typed execution report for the exact ID after all declared connections settle. Export any required material before removing the fixture. [Simulation lifecycle](design.md#lifecycle-and-configuration)

For remote-close simulation, the SDK-owned authenticated loopback bridge forwards WebSocket traffic to the same directional mock streams. The simulation process owns the bridge and mocks. Separate main and probe connections provide independent evidence of disconnect and service availability. [Simulation design](design.md#lifecycle-and-configuration)

## Acceptance

- Run through the public SDK stream APIs for both providers, using the pinned Testify library in process. The dependency gate excludes Testify from production packages.
- Full consumption precedes input half-close. Final server frames stay readable. Normal completion, abort and transport failure yield distinguishable terminal evidence. Cleanup after a completed half-close preserves the final result.
- A passing execution report covers every declared connection and confirms strict matching, full consumption, client EOF and cleanup. A failed or invalid report retains the failure. In-process mock connections report no native launch. A public SDK business assertion is a separate check.
- Bind each report to the fixture, exact execution and exported material. Treat identity or material mismatches and incomplete terminal evidence as failures. Read execution evidence before removing the caller-owned fixture. [Evidence rules](design.md#evidence-and-errors) · [Offline gate](../../testing.md#run-the-fixed-recording-gate) · [Bridge tests](../../../internal/mocktest/peer_test.go)
