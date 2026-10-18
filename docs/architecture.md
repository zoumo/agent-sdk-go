# Architecture

The [architecture standards](standards/architecture.md) are the authoritative
module, native protocol, answer-authority and dependency rules. The
[layout standards](standards/code-layout.md) define the SDK/cmd/tools/testdata
separation; [testing standards](standards/testing.md) define suite/table and
worker-safety requirements. Humans and agents use the same standards.

One module and version keep builds and releases coordinated while native SDKs
retain their own protocols. Sharing confirmed relationship rules avoids two
implementations of graph admission without coupling native event interpretation.

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
