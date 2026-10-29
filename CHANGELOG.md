# Changelog

## v0.1.0-rc.1 — planned, unreleased

- Consolidates the native Codex and Claude Go SDKs into the single
  `github.com/zoumo/agent-sdk-go` module, preserving their source commit ancestry,
  author information, licenses, attribution and recording provenance.
- Exposes `codex`, `codex/types`, `claude` and `claude/types` with one version.
  Native SDKs remain independent; both types packages use only the standard
  library. Previous module import paths require explicit migration.
- Defines eight user stories and 36 complete simulation flows with separate
  native capability/evidence records. Synthetic faults remain code integration
  tests. Question/approval handling retains the original native request authority.
- Shares confirmed relationship bookkeeping through private `internal/lineage`,
  including conflict/cycle rejection, ancestors, depth and resource bounds.
  Native event parsing, creation evidence, Claude aliases and
  transport ownership remain in their respective SDKs.
- Consolidates stdio recording and protocol matching in the Go `tools/agent-mock`
  executable. Python `tools/record-user-story` owns official SDK drivers and case
  evidence; native Go fixture adapters delegate matching to the executable.
- Adds versioned offline scenario peers
  for offline integration tests using the real SDKs, with strict bidirectional
  matching, bounded faults and independent fetched-module consumer checks.
- Pins mise-managed Go 1.26.9 and checks Linux/macOS offline contracts, race,
  vet, schemas/inventories, types boundaries, simulation tooling and examples.
  External consumers retrieve exact pushed commits into independent modules.

This is release preparation. No tag or release has been published. Real agent
tests are not run for this migration; successful current native subagent
interoperability remains unverified. No recorded simulation dataset is bundled;
catalog availability and synthetic tests do not establish real-flow coverage.
See [release checks](docs/testing.md#release-preparation),
[migration](docs/migration.md) and [testing](docs/testing.md).
