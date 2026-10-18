# Changelog

## v0.1.0-rc.1 — planned, unreleased

- Consolidates the native Codex and Claude Go SDKs into the single
  `github.com/zoumo/agent-sdk-go` module, preserving their source commit ancestry,
  author information, licenses, attribution and recording provenance.
- Exposes `codex`, `codex/types`, `claude` and `claude/types` with one version.
  Native SDKs remain independent; both types packages use only the standard
  library. Previous module import paths require explicit migration.
- Adds US-001 through US-012 shared stories with separate native adapters and
  capability/evidence records. Question/approval handling retains each native
  request's original authority and API.
- Shares confirmed relationship bookkeeping through private `internal/lineage`,
  including conflict/cycle rejection, ancestors, depth and resource bounds.
  Native event parsing, creation evidence, Claude aliases/runs/deduplication and
  transport ownership remain in their respective SDKs.
- Adds public native `tools/mock` scenario runners and `cmd/mock` protocol peers
  for offline integration tests using the real SDKs, with strict bidirectional
  matching, bounded faults and independent fetched-module consumer checks.
- Pins mise-managed Go 1.26.9 and checks Linux/macOS offline contracts, race,
  vet, schemas/inventories, types boundaries, sanitized recordings and examples.
  External consumers retrieve exact pushed commits into independent modules.

This is release preparation. No tag or release has been published. Real agent
tests are not run for this migration; successful current native subagent
interoperability remains unverified. Historical recordings retain their
original evidence limits. See [release checks](docs/releasing.md),
[migration](docs/migration.md) and [testing](docs/testing.md).
