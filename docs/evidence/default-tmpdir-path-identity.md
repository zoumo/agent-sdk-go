# Default TMPDIR directory identity repair

Date: 2026-10-18 UTC. Base: `1d15d9d2dd8d7499a2e6b350880cf43a14b20ffa`.
Toolchain: mise Go 1.26.9, Darwin/arm64, `GOTOOLCHAIN=local`.

The inherited TMPDIR was `/var/folders/<private>/T/`.
It was not normalized or overridden for any test below.

## Reproduction and repair

Before the repair, `go test -count=1 -run '^TestExplicitCommandEnvironmentAndDirectory$' .`
failed at the exact command assertion: the child reported `/private/var/...`, while
`t.TempDir()` supplied the equivalent `/var/...` directory. Arguments and explicit
environment values were correct. macOS resolves the directory alias in the child.

The test now uses `os.Stat` and `os.SameFile` to compare directory identities.
Stat errors fail the test; both objects must be directories. Arguments, literal
environment values and exclusion of the inherited variable remain strict.
Production code and the lifecycle tests are unchanged.

Regression cases accept identical paths and a real symlink alias; they reject
separate directories with the same basename and a nested child directory.
The original focused test and all four cases pass (0.724s package initially;
0.485s after the negative probe).

A temporary production probe replaced `cmd.Dir = options.Dir` with an empty Dir.
The repaired original test failed with `command was transformed`, reporting the
repository cwd instead of its configured temporary directory. The probe was
restored byte-for-byte immediately afterward; `git diff --exit-code -- client.go`
passed. Thus real cwd errors are still detected.

## Offline verification

All test commands explicitly set `RUN_REAL_CODEX_TESTS`, `RUN_MAC_STDIO_LIVE`,
`RUN_MAC_INTERACTIONS_LIVE` and `RUN_MAC_WEBSOCKET_LIVE` to `0`.

Passed with inherited default TMPDIR:

- `go test -count=1 ./...` (root package 23.361s).
- `go test -race -count=1 ./...` (root package 135.580s).
- `go vet ./...`.
- `go mod download` and `go mod verify` (all modules verified).
- `python3 docs/protocol/verify_inventory.py` (170 client requests, 11 server
  requests, 85 server notifications, 1 client notification; 23 controls).
- `go test -v -count=1 ./tests/live`: all three tests explicitly SKIP.
- `git diff --check`.

Initial sandboxed full-test/vet attempts could not access the normal Go build
cache; they were rerun with cache access. No global environment or system
configuration was changed. Zero live/model requests were made; no Claude files
were touched. This evidence is offline-only and does not extend historical live
claims. Exact delivery SHA and hosted CI status are recorded in the handoff.
