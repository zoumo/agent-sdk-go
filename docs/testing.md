# Offline validation

Run from the repository root with the pinned compiler:

```sh
mise install
mise exec -- go mod download
mise exec -- bash scripts/check-offline.sh
GOMAXPROCS=2 GORACE=atexit_sleep_ms=0 mise exec -- go test -race -p=1 -count=1 -timeout 300s ./...
mise exec -- go vet ./...
```

The offline script checks single-module and native dependency boundaries, both
standard-library-only types packages, native protocol inventory, reproducible
Codex generation, sanitization canaries and recordings, unit/subprocess tests, and
all runnable offline examples. Validation uses `GOMAXPROCS=2`, package scheduling
`-p=1`, and `GORACE=atexit_sleep_ms=0` to bound concurrent test load and avoid
per-subprocess race-detector exit sleeps. CI runs the same checks, race and vet on Linux and
macOS. Every example compiles with `go test ./...`.

Live tests are disabled through `RUN_MAC_STDIO_LIVE=0`,
`RUN_MAC_INTERACTIONS_LIVE=0`, `RUN_MAC_WEBSOCKET_LIVE=0`, `CLAUDE_GO_LIVE=0`,
and `SDK_REAL_AGENT_E2E=0`. Neither these checks nor recorded replay invokes a real
agent/model. Native successful subagent interoperability remains unverified;
see each package's compatibility and recording documentation for precise evidence.

## External consumers

After pushing a commit, validate the new module from independent temporary
consumer modules using its full SHA:

```sh
mise exec -- bash scripts/check-consumers.sh "$(git rev-parse HEAD)"
mise exec -- bash claude/scripts/check-managed-consumer.sh "$(git rev-parse HEAD)"
```

Both scripts create their modules and module caches under `/tmp`, disable
`go.work` and inherited Go overlays/modfiles, and fetch from the new repository
without `replace`. The root script resolves the exact SHA directly from GitHub,
checks its full VCS origin hash, compiles all four public imports, copied public
examples and documented configuration/approval snippets, and runs only synthetic
value/configuration checks and the pure Codex public consumer. The retained
Claude script separately compiles its native consumer and configuration snippet.
Dependency download requires network access; compilation/runtime checks then use
`GOPROXY=off` and `GOSUMDB=off`. Neither script opens a client or invokes an agent.

External consumer CI runs on Linux/macOS for pushed `main` commits and manual
workflow runs, targeting `github.sha`. PR offline checks remain separate because
synthetic merge SHAs may not be retrievable as Go module versions. See
[release readiness](releasing.md) for the required exact-commit CI evidence.
