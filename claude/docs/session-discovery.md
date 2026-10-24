# Claude Session stored subagent discovery

`Session.ListSubagents(ctx, SubagentQuery{})` performs a fresh, read-only native
record query. The implementation follows the fixed TypeScript SDK 0.3.258
ordinary-directory recursion and same-basename `.meta.json` sidecars described
in [native evidence §5](../../docs/research/claude-session-native-evidence.md#5-子记录查询布局与-sidecar).
Its contract is the [Session interface](../../harness/modules/native-session/interfaces.md).

Open captures the process cwd and effective environment. Discovery uses
`CLAUDE_CONFIG_DIR`, or the effective `HOME/.claude`; a missing effective HOME
makes discovery unavailable rather than searching the caller's personal home.
Relative config paths resolve against that captured cwd. Existing cwd/config
aliases are resolved at Open. A config root not yet present is opened when queried;
that explicit root may itself resolve an alias. Below that root, components must
be ordinary directories and records ordinary files. Each directory is pinned and
checked against its pre-open identity; record-tree symlinks are rejected, including
links to another location within the same config root. The supported filesystem
read adapter is Unix; other platforms report unsupported metadata reads.

The observed main UUID, canonical cwd's exact native project key, and main
transcript path select `projects/<key>/<mainUUID>/subagents`. Requested resume IDs
never substitute for observed identity. Truncated project keys are unsupported;
there is no prefix/project-wide search. Available, unambiguous sampled main
`sessionId/cwd` fields that contradict the scope reject the query. Missing,
malformed, growing or oversized main payloads produce metadata coverage issues,
not a full-history/terminal/resume prerequisite for discovering directory records.
Native project-key encoding is lossy; this does not prove a unique cwd across all
possible colliding native directory names or scan alternative layouts.

Every ordinary `agent-*.jsonl` file produces a record identified by its filename,
including records that predate this connection, nested records, terminal records,
and records with unreadable or incomplete payloads. Transcript `sessionId`, `cwd`,
`agentId`, task and run extensions remain raw metadata; they are not assumed to
be aliases of the main identity. Optional sidecar `toolUseId/parentAgentId` values
are storage metadata only. Missing/empty parents stay unknown. Unknown metadata
is retained in `Metadata`/`Sidecar`; invalid bytes and duplicate keys cannot create
parent/tool claims. Duplicate agent filenames in different directories remain
separate records with an explicit ambiguity issue. Lexical output ordering is
only presentation; it selects no latest record or run sequence.

`Coverage.Status` is `complete`, `partial`, or `unknown`. Complete describes only
the declared layout and metadata budget at the time each source was read. There
is no native cursor, atomic snapshot, running-state proof or guarantee that native
storage contains every agent. Every record's `Running` is `unknown`. A missing
sidecar is supported and leaves its metadata unknown; a missing discovery root,
orphan sidecar, unknown file layout, symlink, access/parse failure or resource
limit is an issue. Partial records and errors can be returned together. Always
check both error and coverage; an empty result alone proves nothing.

Default budgets are 10000 discovered records, 64 MiB actual source bytes,
100000 metadata records, 100000 directory entries and 64 nested levels. Optional
metadata sampling reads at most 64 KiB per source by default (`MaxFileBytes`).
Main samples and sidecars count toward the same byte/metadata budgets. JSONL
samples preserve raw lines; sidecars are whole JSON objects, including formatting.
A large file or unfinished line makes metadata partial. Exhausted metadata budgets
still permit directory discovery; record/entry limits stop enumeration, and a
depth limit leaves that subtree uncovered. `Coverage` reports limits, actual
bytes/files/metadata records/entries, visited directories, selected root/main path,
and path-specific failures. No limit is represented as complete truncation.
The reader samples each file's stat-time prefix and permits append activity; it
does not validate a frozen transcript chain. Filesystem changes can yield partial
coverage and are not an atomic inventory.

The query does not start/resume/subscribe, invoke a model, replay history, send
input/configuration/answers, create handles, or update membership/parent knowledge.
Cancellation and Session closure are checked during traversal and metadata reads;
all owned file/directory handles close on return, independently of Receive.
`Close` rejects subsequent queries. Query errors do not terminate the Session.

Public-method regressions use temporary synthetic native-shaped storage and the
existing synthetic subprocess peer only. They establish local discovery and
transport-side-effect behavior, not real agent interoperability, live forwarding,
recovery or actual execution state. No personal history/credentials or new
recordings are used.
