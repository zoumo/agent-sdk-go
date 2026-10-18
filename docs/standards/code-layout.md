# Code layout standards

Both native SDKs use the same separation of responsibilities:

| Location | Responsibility |
|---|---|
| `codex/`, `claude/` | Independent native SDK runtime and native types |
| `codex/cmd/<command>`, `claude/cmd/<command>` | Thin CLI `main` entry points; delegate to recorder tools and preserve error/exit behavior |
| `codex/tools/recorder`, `claude/tools/recorder` | Explicit recording orchestration, native SDK setup, guarded proxy dispatch and capture lifecycle |
| `codex/tools/replay`, `claude/tools/replay` | Importable native capture/safety primitives, fixture reading and offline replay helpers used by commands, SDK tests and replay examples |
| `codex/testdata/recordings/real-sanitized`, `claude/testdata/recordings/real-sanitized` | Sanitized real captures and their manifests, expected observations, ID maps and transport metadata |
| Each native `testdata/synthetic-faults` | Clearly identified synthetic protocol/fault data |
| Each native `examples` | Genuine SDK usage and offline usage demonstrations |
| `tests/consumers/codex-public` | Pure public API/import-boundary consumer, not an SDK usage demonstration |
| `tests/stories` | Shared story suite, native test adapters and synthetic protocol peers |

The recording commands are `codex/cmd/record-events` and
`claude/cmd/record-transcript`. Native wire formats and implementations may
remain different. Reusable helpers must remain importable by SDK tests and
examples: do not hide those shared helpers under `tools/internal`, which would
violate Go's internal visibility rule. Tool-only private implementation may use
an appropriate internal package when no outside consumer needs it.

Tools and fixture containers are not SDK runtime dependencies; preserve the
[architecture dependency gates](architecture.md). Each `testdata/embed.go` is a
small standard-library-only fixture container because `go:embed` cannot read a
parent directory. It stores no second copy of capture bytes. Example-local
synthetic data may remain with its example.

Move data rather than copying a second source of truth. Preserve real bytes,
manifest hashes, original source/CLI identities and evidence limits. Keep raw
captures, secrets and private configuration out of published fixtures. Synthetic
data must never be labeled real. Update imports, embeds, scripts, examples, CI
and documentation links whenever paths change. The [recording index](../recordings.md)
owns direct file links and current evidence limits.
