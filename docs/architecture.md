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
