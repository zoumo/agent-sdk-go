# macOS default temporary-directory assertion repair

Date: 2026-10-18 UTC. Base: `8923bc26f7bbc94e692a26b9579a3bdb5f813197`.
Scope: the independent repair described in [ticket04 follow-up](type-ticket04-followup.md).

## Cause and change

The original focused test failed twice under the inherited macOS TMPDIR.
The fixture child reported cwd under `/private/var`, while the requested
WorkDir and literal HOME used `/var`. On this system `/var` is a symlink to
`private/var`; these names identify the same directory. Comparing their text
incorrectly reported an environment isolation failure.

`TestOpenUsesLiteralArgumentsAndMatchedInitialize` now stats both paths,
requires directories, and compares their filesystem identity with `os.SameFile`.
HOME and argv remain exact string/value comparisons. The original default-temp
case remains, and a symlink WorkDir case explicitly supplies an alias as both
WorkDir and HOME. Both cases exercise the existing initialize and send assertions.
This makes the alias scenario reproducible in Linux CI as well as on Mac.
No SDK production code, exported API, WorkDir selection or permission behavior
changed. No global TMPDIR, system setting or CLI configuration changed.

## Verification

Local platform: macOS arm64; installed Go 1.26.9 through mise.
All checks inherit the ordinary TMPDIR without normalization or replacement.
Build caches are isolated and writable; `GOTOOLCHAIN=local`, `GOENV=off`,
`GOPROXY=off`, `GOSUMDB=off`; race checks use `GORACE=atexit_sleep_ms=0`.
Fixtures use only the local test executable and replacement child environments.

| Check | Result |
| --- | --- |
| Original focused test, count=2, before repair | FAIL, cwd alias mismatch |
| Repaired default and explicit symlink cases, count=2 | PASS |
| Full `go test -count=1 -timeout 180s ./...` | PASS |
| Full `go test -race -count=1 -timeout 300s ./...` | PASS |
| `go vet ./...` | PASS |
| Existing compatibility pressure selector, race count=30 | PASS |
| Existing process-group/escalation selector, race count=10 | PASS |
| Default and symlink path cases, race count=30 | PASS |
| Independent types standard-library dependency boundary | PASS |
| Wrong-directory and literal HOME negative controls | PASS |

The pressure/lifecycle commands are the unchanged selectors in
[compatibility.md](../compatibility.md#reproduce-the-synthetic-checks).
The path-specific selector is `^TestOpenUsesLiteralArgumentsAndMatchedInitialize$`.
Negative controls were run as temporary local mutations and then restored:
restoring the old string assertion fails the explicit symlink case; removing
production WorkDir selection fails the new cwd identity assertion; appending
`/.` to the fixture's reported HOME fails the literal HOME assertion.
The restored focused race test passes. Production files have no final diff.

Private paths and original captures are omitted. Scratch acceptance notes,
check scripts, and local logs stay ignored and are not published.
Exact repair commit and matching Linux GitHub Actions run are reported after
push, avoiding a self-referential evidence hash.

## Independent review of prior live claims

This repair used **no live CLI or model calls**. The prior eight-input evidence
remains bounded to installed CLI **2.1.258** and configured model
`theta/GLM-5.3[1m]`. Source target **2.1.302 was not live-tested**.
Tool scenarios ending in `error_max_turns` / `is_error:true` demonstrate only
the specifically observed tool behavior, not successful completed conversations.
Neither this synthetic repair nor those bounded observations establish full
version compatibility. See [ticket04 evidence](type-ticket04.md).
