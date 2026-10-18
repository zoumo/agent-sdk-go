# CI workflow repair evidence

Date: 2026-10-18 UTC. Scope baseline:
`9d6b006f37c0d8aff1ba8f4d69380152c815d4ee` on `main`.
This evidence records local checks; hosted results must be matched to the
resulting commit SHA, not a moving branch label.

## Changes

Linux/macOS offline matrix, Go 1.26.9, read-only token, checkout without
persisted credentials, full history for committed range checking, SHA-pinned
Node 24 Actions, main push/PR/manual triggers and superseded-run cancellation.
Full synthetic unit/subprocess, existing compatibility, race, vet and independent
`types` dependency checks remain enabled. Cache isolation and `-count=1` remain
intentional. No generator was introduced for handwritten types.

See [CI documentation](../ci.md) for exact commands, range rules, Action release
links and live gate behavior.

## Local verification

Host: macOS arm64. Toolchain: `go version go1.26.9 darwin/arm64` through mise.
The default host temporary directory was retained for the complete normal test
suite. An inherited older GOROOT was corrected only for the command environment
by `mise exec`; no global settings or credentials were changed.

| Check | Result |
| --- | --- |
| `go test -count=1 -timeout 180s ./...` | PASS, root and types |
| `go test -race -count=1 -timeout 300s ./...` with CI-style `env -i`, isolated HOME/GOCACHE and offline Go settings | PASS, root and types |
| `go vet ./...` in the same isolated environment | PASS |
| Independent `types` nonstandard dependency inventory | Only `github.com/zoumo/claude-agent-sdk-go/types` |
| actionlint 1.7.12, official release archive checksum verified | PASS |
| `bash -n scripts/check-changes.sh`, shellcheck, whitespace | PASS |
| Default and explicit `CLAUDE_GO_LIVE=0` initialization test | Visible `LIVE SKIPPED` |
| Invalid live value, live enabled in CI, live enabled without absolute CLI path | Expected failure before CLI startup |

Disposable Git repositories verified 16 positive/negative cases using the actual
`check-changes.sh` and pinned gofmt. Each malformed input was committed before
checking; all repositories and outputs were temporary and contained synthetic
content only.

- Clean PR base/head and push before/head comparisons pass.
- Clean manual first-parent and root-commit fallbacks pass.
- Clean all-zero initial-push comparison passes.
- Committed trailing whitespace fails with a clean working tree, PR multi-commit
  range, push range, manual parent range and initial-push range.
- Editing the working tree to remove whitespace cannot hide the committed error.
- Committed unformatted Go fails; formatting only the working tree cannot hide it.
- An empty commit range still checks every committed Go file's formatting.
- A missing nonzero base object fails closed.
- A malformed root commit fails through the manual empty-tree fallback.

No installed real CLI was started, no user prompt was sent and no model call was
made. Live CLI/model compatibility remains outside this ticket's validation.
Linux execution is verified by the corresponding hosted matrix job, not by
cross-compilation or the local Mac result. Private logs and scratch drafts are
not part of the committed evidence.
