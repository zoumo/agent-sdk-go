# Architecture

`github.com/zoumo/agent-sdk-go` is one Go module with one version. Native clients
live in `codex/` and `claude/`; neither package imports the other. Pure protocol
data lives independently in `codex/types` and `claude/types`, using only the
standard library. There is no root Go client, nested module, or workspace file.

Native event decoding, process/transport ownership, identities, answering handles,
request lifecycle and event caches belong to their native SDK. Unknown native
data and explicit observation gaps retain their existing semantics. Relationship
observations grant no authority to answer requests. No common answer state machine
or public question interface is introduced.

Package documentation retains native protocol inventories, schema sources,
compatibility boundaries and recorded fixture provenance. Shared operating and
migration documentation lives here.

The [shared stories](stories/index.md) describe common acceptance criteria while
keeping native capability and evidence records separate. Acceptance checks and
private detached facts live directly in
[`tests/stories/acceptance_test.go`](../tests/stories/acceptance_test.go) on
`CommonStoriesSuite`; each native adapter retains its client lifecycle and answer
handles. These checks have no production package or shared answer implementation. The [glossary](glossary.md)
defines their terms. [Confirmed relationship bookkeeping](lineage.md) is the
private shared implementation; native event parsing and answer authority remain
in their SDKs. [Release preparation](releasing.md) covers one version for all
four public packages and external consumer verification.
