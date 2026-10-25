# Claude independent child history — #18

Implementation starts at #15 squash
`0011f7241e4f05142ccaf451d6988eb31f72bfe4` in the unified
`github.com/zoumo/agent-sdk-go` repository. The prerequisite #8 audit is included
through `52b329a`. Thread: `01a1721d-2a48-73d9-a675-16b23d1cd0d8` delegated ticket
context; branch `feat/18-claude-child-history`. Parent specification is #7.

The [native API and synthetic acceptance mapping](../../claude/docs/subagent-history.md)
implements the [reviewed contract](../../harness/contracts/claude-subagent-history.md).
Only Claude package-level local queries are added. Existing main query,
Session.ListSubagents, filtering (#17), recording code and workflow checkout
semantics are unchanged. Shared US-014 acceptance includes no-Session and a
same-identity/config live synthetic peer coexistence barrier, with unchanged
peer input and original approval ownership.

Independent Standards and Spec reviews ran against the fixed baseline. Standards
found no hard violations and one low-severity coverage-string-as-mode heuristic;
query defaults/inventory caps now use explicit internal values. Spec identified
three issues, each reproduced in a failing regression and fixed: returned
MaxMessages ceiling versus whole-chain size, duplicated missing-child error Path,
and a directory masquerading as an orphan sidecar's transcript. Final review and
exact published head/base/synthetic-merge SHAs and hosted results are recorded in
the PR/issue evidence, avoiding self-referential commit IDs in this file.

Local validation uses official Go **1.26.9**, matching mise.toml, downloaded from
`https://go.dev/dl/go1.26.9.linux-amd64.tar.gz`, SHA-256
`42d158b4d8f7b61ac0a830567c940a86098fb7aac52e467a5ebec03ef5cc2f8d`.
No usable compiler/mise existed at standard locations; the preloaded `/usr/bin/go`
did not accept `version`. The official archive was verified against go.dev's
published download JSON and extracted under `/tmp/ticket18-tools`, with caches
under `/tmp`. Mise metadata endpoints returned HTTP 403; local checks therefore
invoke the exact pinned compiler directly. Hosted gates retain mise unchanged.

Validation commands:

- Focused native public history, invalid input/identity, slicing/ownership,
  malformed/sidecar/resource, regular-file/containment and integrity tests.
- Shared `TestCommonStories/TestIndependentSubagentHistory`, covering no process
  fallback and coexistence without answer/Receive injection.
- `bash scripts/check-offline.sh`: schemas/generation, inventories, provenance/
  sanitization/hashes, production dependency boundaries, full tests and examples.
- `go test -race -p=1 -count=1 -timeout 300s ./...` and `go vet ./...`.
- Exact pushed revision external consumers, including the new complete child
  browser, run by the existing six hosted Offline/External jobs. Offline keeps
  the GitHub synthetic merge checkout; External retrieves the exact PR head.

All local saved content is temporary synthetic data; real-agent flags remain
zero. No old/private transcript or platform credential was read, no real CLI or
model was launched, no live budget was spent and no fixture was relabeled.
Successful real child-file interoperability/background completion remains a #22
live/publication-gated evidence gap. Atomic is always false; final verification
cannot prove absence of restored-between-checks or later writes, and pages do
not share a snapshot. No release/tag, permission changes or automatic merge.
