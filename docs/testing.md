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
