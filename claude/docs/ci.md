# CI checks and live gate

The [shared workflow](../../.github/workflows/ci.yml) validates the single module
on Linux (`ubuntu-24.04`) and macOS (`macos-15`) with mise-managed Go 1.26.9.
See [root validation](../../docs/testing.md) for current commands and gates.
Native scripts and examples remain relative to `claude/`.

Committed whitespace and formatting checks inspect Git objects over the event
range. Unit/subprocess, compatibility, types independence, transcript checks,
offline examples, race and vet run with live gates disabled. The recorded sample
is an authentication-failure transcript, not successful model coverage.

After pushing a commit, run `bash scripts/check-managed-consumer.sh COMMIT_OR_VERSION`
with dependency network access to compile an independent consumer downloaded from
the new module. This uses neither `replace` nor `go.work` and never calls Open.

`CLAUDE_GO_LIVE=0` and `SDK_REAL_AGENT_E2E=0` disable real agent/model tests in CI.
The initialization/shutdown probe in
[`tests/e2e/initialization_test.go`](../tests/e2e/initialization_test.go) remains opt-in
and refuses execution when `CI` is nonempty. A skip or initialization-only pass cannot upgrade the
[full native live compatibility checklist](live-compatibility.md).

Actions use pinned commits, read-only repository permissions, no persisted checkout
credentials and no `pull_request_target`. Claude types are handwritten; Codex has
its own independent schema inventory and regeneration checks.
