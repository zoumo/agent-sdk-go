---
type: Design
title: SDK tests with the Replay Go library
status: stable
description: "Defines connection ownership, call order, and acceptance for Go SDK tests that use Replay for in-process replay."
tags: [testify, replay, testing]
---

# Replay library integration

Replay owns fixture preparation, native replay, material exports and typed execution evidence. The SDK owns its driver, public SDK calls, business assertions and simulation coverage. The test-only `internal/mocktest` bridge coordinates the fixture and the SDK-side streams. Replay reads the SDK version and revision or dependency checksum from Go build information. The bridge checks the fixture against the linked build. Material provenance retains its original identity. [Identity resolver](../../../replay/internal/version/identity.go) · [Replay library](../../../replay/README.md)

## Connection ownership and order

1. Prepare or open a fixture and register cleanup. The caller owns the prepared and exported directories. The bridge creates two directional streams and keeps diagnostics separate from the protocol.
2. Start `StartMockAgent` and read `run.Start` before creating the SDK client. Once Replay claims the connection, it owns the fixture-side endpoints. The SDK takes the opposite endpoints after opening. If startup fails before an SDK client takes ownership, the bridge reclaims its endpoints and settles any started run.
3. Construct `codex.NewStreams` or `claude.OpenStreams` with the execution overlay, complete replacement environment and validated native arguments from `run.Start`. Codex takes its protocol directory from `ProtocolCwd`. Claude uses `Workspace` and the execution's configuration scope. Keep the environment in the transport and omit its values from reports and logs. Explicitly join any additional declared connection to the same execution ID. [Bridge](../../../internal/mocktest/library.go) · [Codex streams](../../../codex/stream.go) · [Claude streams](../../../claude/stream.go)
4. Consume output while watching `FullConsumption()` and `Done()`. When Replay signals full consumption, call `CloseInput()` to close only the SDK write end. Keep reading through output EOF. If the mock stops before full consumption, use `Wait` to read its terminal result.
5. Close the SDK client or Session and settle every mock run with `Wait`. On cancellation or failure, use a separate bounded cleanup context. Abort unfinished runs to release blocked I/O. `Wait` settles the current connection. Read the typed execution report for the exact ID after all declared connections settle. Export any required material before removing the fixture. [Simulation lifecycle](design.md#lifecycle-and-configuration)

For remote-close simulation, the SDK-owned authenticated loopback bridge forwards WebSocket traffic to the same directional mock streams. The simulation process owns the bridge and mocks. Separate main and probe connections provide independent evidence of disconnect and service availability. [Simulation design](design.md#lifecycle-and-configuration)

## Acceptance

- Run through the public SDK stream APIs for both providers, using the linked Replay library in process. The dependency gate excludes Replay from production packages.
- Full consumption precedes input half-close. Final server frames stay readable. Normal completion, abort and transport failure yield distinguishable terminal evidence. Cleanup after a completed half-close preserves the final result.
- A passing execution report covers every declared connection and confirms strict matching, full consumption, client EOF and cleanup. A failed or invalid report retains the failure. In-process mock connections report no native launch. A public SDK business assertion is a separate check.
- Bind each report to the fixture, exact execution and exported material. Treat identity or material mismatches and incomplete terminal evidence as failures. Read execution evidence before removing the caller-owned fixture. [Evidence rules](design.md#evidence-and-errors) · [Offline gate](../../testing.md#run-the-fixed-recording-gate) · [Bridge tests](../../../internal/mocktest/peer_test.go)
