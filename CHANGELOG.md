# Changelog

## v0.1.0-rc.1 — planned, unreleased

- Consolidates the native Codex and Claude Go SDKs into the single
  `github.com/zoumo/agent-sdk-go` module, preserving their source commit ancestry,
  author information, licenses, attribution and recording provenance.
- Exposes `codex`, `codex/types`, `claude` and `claude/types` with one version.
  Native SDKs remain independent; both types packages use only the standard
  library. Previous module import paths require explicit migration.
- Adds the optional provider-neutral `agent` core and `agent/codex`/`agent/claude`
  adapters for live Provider and Session operations, events, input, interactions
  and classified errors. The core uses only the standard library; adapters stay
  provider-specific, and the layer adds no history abstraction or root client.
- Makes `StartOrSteer` the unified input operation with explicit
  `applied_current_work`, `applied_new_work`, `applied_best_effort` and
  `not_applied` results. It does not queue, wait for idle, persist or retry; the
  strict `Submit` remains transitional.
- Removes the unpublished SDK history replay projection while retaining native
  history queries, native replay protocol fields and simulation replay.
- Adds typed Claude permission modes and Codex approval policies while retaining
  Codex compatibility aliases and native unknown-variant round trips.
- Defines eight user stories and 36 complete simulation flows with separate
  native capability/evidence records. Synthetic faults remain code integration
  tests. Question/approval handling retains the original native request authority.
- Shares confirmed relationship bookkeeping through private `internal/lineage`,
  including conflict/cycle rejection, ancestors, depth and resource bounds.
  Native event parsing, creation evidence, Claude aliases and
  transport ownership remain in their respective SDKs.
- Moves recording, replay, material qualification and official SDK drivers into the
  independent `agent-sdk-testify` tool. This repository retains the Go SDK drivers,
  public-flow assertions, simulation orchestration and authenticated loopback bridge,
  and consumes the external tool only through its versioned CLI/process contract.
- Runs SDK fixture adapters against the external `mock` process using versioned
  synthetic captures and strict matching. No protocol matcher or recording source
  tree remains in this repository.
- Requires Go 1.26 or newer, uses the root Makefile for formatting and linting,
  and checks Linux/macOS offline contracts, race, vet, schemas/inventories,
  types boundaries, simulation tooling and examples.
  External consumers retrieve exact pushed commits into independent modules.

This is release preparation. No tag or release has been published. Real agent
tests are not run for this migration; successful current native subagent
interoperability remains unverified. No recording material is bundled here;
external catalog availability and synthetic tests do not establish real-flow coverage.
See [release checks](docs/testing.md#release-preparation),
[migration](docs/migration.md) and [testing](docs/testing.md).
