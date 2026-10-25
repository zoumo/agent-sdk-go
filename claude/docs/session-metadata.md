# Independent local discovery and metadata

Contract: [Claude metadata](../../harness/contracts/claude-session-metadata.md).
Acceptance: [shared scenarios](../../harness/contracts/stories/claude-session-metadata.md).

```go
scope := claude.SessionQueryScope{ConfigDir: "/explicit/config", ProjectDir: "/absolute/project"}
page, err := claude.ListSessions(ctx, claude.ListSessionsOptions{Scope: scope, Limit: 100})
// Always inspect err and page.Coverage; partial records may accompany errors.
info, err := claude.GetSessionInfo(ctx, sessionID, claude.SessionInfoOptions{Scope: scope})
// Info may be nil when the first source record identifies sidechain/team storage.
messages, err := claude.GetSessionMessages(ctx, sessionID, claude.SessionMessagesOptions{Scope: scope})
```

No Session or CLI is required, no query writes or model calls occur, and existing
live delivery/approval authority stays independent. Run the self-contained example:

```sh
mise exec -- go run ./claude/examples/session-metadata-offline
```

Scope and filename resolution are shared with [messages](session-messages.md),
including explicit absolute ConfigDir, optional canonical project key, 200-character
project-key boundary, regular/no-follow source opening and rejection of tree symlinks.
With no ProjectDir, list scans every immediate project directory. Only immediate
main UUID JSONL files are metadata sources. Known UUID subagent containers and
regular sessions-index.json are outside the scan; other entries report unsupported.
Depth is fixed at projects → project → immediate entry. No git/worktree expansion,
remote history, old layout, alpha SessionStore or Managed Agents adapter exists.
Programmatic and daemon transcripts in the same layout are included without an
entrypoint filter; separate layouts are unsupported. Unlike the official default,
project queries do not expand worktrees or fall back to another worktree.

Defaults: 100 returned sessions, max page 1000, inventory 10000, visited entries
10000, 64 KiB each for head/tail, 16 MiB initial sampled bytes per call, and 10000
complete sampled lines (including blank lines). All limits and offsets must be
nonnegative; zero means the documented defaults. Discovery and metadata sampling
are bounded before sorting by modification time descending and applying offset/
limit. Ties sort by absolute Path ascending, an explicit SDK policy rather than a
native ordering guarantee. Duplicate identities are returned separately with an
ambiguous error. There is no native cursor, nextCursor or total. Each page is a
new discovery/sample; content slicing is not filesystem I/O pagination.

Info reads only the selected source's bounded head/tail. Complete JSON objects
inside those windows become Records; raw Head/Tail preserve exact bytes, unknown
extensions, null/omitted distinctions and numbers. Partial edge lines remain only
in raw samples. Sampled=true means the middle was not read. Unknown metadata and
valid empty sources retain filename identity/stat with an empty Summary. The
first complete source record can exclude a sidechain/team file; otherwise a
missing title is not grounds to drop an identity. File existence never proves
running: Running is always unknown. LastModified is time.Time from file stat;
CreatedAt is optional parsed RFC3339 timestamp from a complete head record, and
Timestamp preserves the original string. No project path is invented as cwd.

Summary precedence follows the pinned Python metadata intent: last nonempty
customTitle, then aiTitle, then lastPrompt, then summary, then first meaningful
head user prompt. CustomTitle and AITitle stay separately available. Prompt
extraction skips tool_result/meta/compact/sidechain/team content and native
system-generated prefixes, uses command-name only as fallback, and truncates
at 200 Unicode codepoints plus an ellipsis. Tag is extracted only from tag
records. This is a preview of sampled storage, not the selected message chain.

Coverage complete means the declared discovery/sample was checked, including
when Sampled=true. It does not certify the unread middle, availability of full
messages, all native layouts or liveness. Missing/invalid/corrupt/inaccessible/
unsupported/ambiguous/resource-limit/changed and context cancellation are distinct.
OS causes and multiple errors survive errors.Join. List keeps readable partial
records and filename/stat facts alongside errors; info may also retain a partial
Info alongside an error. Callers must not display partial as complete. Explicit
missing project/config is an error, unlike a valid empty declared directory.

Samples are reread before return and compared with original bytes plus size,
mtime and root-relative file identity. This detects observed append, sampled
rewrite and file/parent-directory replacement. Validation rereads at most the
same byte budget; Coverage.Bytes counts initial reads, not verification reads.
No transaction is acquired. An unread-middle rewrite with unchanged size/restored
mtime, fully restored changes between checks, writes after checks, explicit root
renames and changes to the discovery set can evade detection. Atomic is always
false and pages have no shared snapshot. Errors carry private paths/raw material;
applications decide retry and redaction policy.

## Fixed native references and strict differences

Python SDK 0.2.174, commit
[`0e89b298e771259378a6cd3734e00d323dff5703`, sessions.py](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py)
was checked for `_read_session_lite`, `_parse_session_info_from_lite`,
`_extract_first_prompt_from_head`, `_apply_sort_limit_offset`, `list_sessions`
and `get_session_info`. Metadata preview selection is adapted with MIT attribution
in [NOTICE](../NOTICE). Official npm 0.3.310 sdk.d.ts/sdk.mjs were statically
inspected; tarball SHA-512 matched the research pin:
`fo+sr+9Ks73P001f6XKRQlyzfIBAJMgXArLUkH5Tu98NikRDZeGtFQVj23d5/hj2ijpFgzffrr4fHywY9qUkoQ==`.
No official SDK executes as part of this backend.

Go deliberately preserves unknown/empty identities where Python drops files
without a summary; distinguishes errors where Python returns []/None; rejects
malformed sampled objects, duplicate keys, invalid Unicode/typed metadata and
incomplete final lines rather than lenient string extraction; reports duplicate
locations rather than deduplicating newest or selecting first; uses explicit
scope and bounded defaults. Unknown record types remain raw, not unsupported
messages. The npm implementation has additional storage/relocation/entrypoint
policies; this slice does not claim parity with those paths or the official
unbounded/default-worktree behavior. See the message guide for stricter complete
source parsing and supported chain reconstruction.

Synthetic public acceptance runs inside CommonStoriesSuite with original native
query values, temporary config, an unavailable runtime PATH, file hash/mode
checks, selection/pages, unknown/partial/failures and live synthetic approval
coexistence. Focused internal fault injection verifies only source mutation cases
that cannot reliably be timed through public filesystem calls. Actual results
are recorded in [ticket03 validation](../../docs/validation/claude-session-metadata.md).
