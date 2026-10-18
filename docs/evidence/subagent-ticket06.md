# Subagent ticket06: independent proven Created

Date: 2026-10-18 UTC. Baseline main
`8e4a1a9c1f2147f4ff7208144539cc2cb5d586ba`. This independently authorized
supplement completes conditional first-entity creation observation. It preserves
Associated, native delivery and Raw. The delivery commit is the containing Git
commit (`git log -1 --format=%H -- tree_creation.go`); CI records its full SHA.

## Current version decision and source evidence (E1)

The user subsequently directed: use the currently installed CLI, without old
version expansion. Read-only checks returned:

- Executable: `/Users/jim/bin/claude` (Mach-O arm64).
- `claude --version`: **2.1.258 (Claude Code)**, exit 0.
- Installed binary SHA256: `0d036f0b635d239844e6df6996bc05263fe305df505b073c71f79f83668ea35a`.

No CLI upgrade, initialization/model invocation, authentication change or account
probe was performed. A version probe is not live feature validation. The earlier
2.1.302 research target was superseded for this ticket. Creation accepts exactly
the native init version string **2.1.258**, never a >= range.

Official [SDK 0.3.258 archive](https://registry.npmjs.org/@anthropic-ai/claude-agent-sdk/-/claude-agent-sdk-0.3.258.tgz)
was downloaded only for static review. Its manifest names CLI **2.1.258**, commit
`b3cd543a1f6fcdf4d8fabc0f5e5538d2ee7f38e1`, build date 2026-09-01. The local
binary hash differs from the package manifest's darwin-arm64 checksum; no claim
is made that those are identical builds. Relevant local binary schema and code
paths were separately inspected as bytes, without executing embedded code.

| Fixed file | SHA256 |
| --- | --- |
| 0.3.258 archive | `656cf237bc567cb172a007a0fd5b3958cf960d154c03ab390a755d2c3bdbb398` |
| sdk.d.ts | `b209ed293b63bb92bde59a49fe8a6e3b06f92afe0d1d4987c937a3cfea90b6dc` |
| sdk-tools.d.ts | `a8bb537bb1624e9e68d5aa7c620260027278a9f83ce81943906a9485b06d7c9d` |
| manifest.json | `ec53b2ce3d9f95d2c94c07aacf9202e6df9f79c695b71a1af2b4491081fba60f` |
| LICENSE.md | `8ce94b9478bb9868f9641f818e06cd722fbe55d4c22e2d2ed11971b20146173a` |

Source facts reviewed:

- sdk.d.ts lines 5167–5202: local_agent/local_bash background registration;
  resumed subagents always backgrounded. Missing/null is not false.
- sdk-tools.d.ts lines 103–205: completed/async_launched Agent results have
  agentId; remote_launched has a different shape and is excluded.
- sdk-tools.d.ts lines 658–710: AgentInput includes prompt, description, optional
  background/name/isolation; no resume parameter.
- sdk.d.ts lines 5267 onward: user tool_use_result is the full structured tool
  output, distinct from model-directed tool_result text.
- Current sdk.d.ts task/message declarations have no run_id, parent_task_id or
  assistant/user agent_id contract. Current Created therefore requires no run ID
  and labels it legacy-unknown; ancestry comes from observed tool ownership.
- Installed binary byte offsets 163717594 (task_started emits task id/type/tool
  and registration background flag), 164960125 (background local_agent task uses
  the agent's ID), 164960470/164961114 (new/resumed background registration),
  164961789 (foreground registration), 163925062/163929730 (async launch result)
  were checked. These are locators into the hashed local binary, not copied code
  or claims about another build.

The earlier [SDK 0.3.302 archive](https://registry.npmjs.org/@anthropic-ai/claude-agent-sdk/-/claude-agent-sdk-0.3.302.tgz)
was also rehashed to `c7d7d61d6b0b7b7bb05cc053f229c5df88f151550ead0624c7393939d0d8e1f1`;
its sdk.d.ts lines 6218–6220 confirm the same resumed-background rule, but it is
not this ticket's runtime baseline. The [official v2.1.77 release](https://github.com/anthropics/claude-code/releases/tag/v2.1.77)
records Agent.resume migration to SendMessage. The [Python Agent reference](https://code.claude.com/docs/en/agent-sdk/python#agent)
was read as mutable corroboration, not a fixed-version support range.

The TS package is non-MIT; its existing legal boundary applies. No downloaded
source, declarations, binary or upstream fixtures are redistributed. Production
Go code, tests and fixture data are original Apache-2.0. No dependency was added.

## Delivered API and proof boundary

`TreeRecord.Kind == "SubagentCreated"` is an independent sdk-derived record,
with live-arrival provenance, native task/tool identity, known immediate parent,
reason creation-confirmed and criterion evidence. Its arrival confirms that
creation happened; it does not invent a provider creation timestamp or RunID.
`TreeOptions.MaxCreationEntries` is a positive retained-evidence bound (default
8192, maximum 131072, zero selects default). Evidence and entity creation bits
are never evicted. Alias merge ORs the creation bit and repoints caller evidence.

Creation requires a fresh owned OpenTree process, a single exact-version root
init, stable native session, complete advertised builtin Agent/Task request and
attributable ancestry. It then requires either explicitly false local_agent
registration, or one successful structured completed/async_launched result
matched by tool_use_id and corroborated by the same local_agent task ID.
Request absence of resume alone never proves creation: hooks can change input.
Missing/null/true background values do not meet the foreground criterion; a
successful corroborated result can independently prove creation.

History seeds or explicit ResumeID disable creation for the observation. Replay,
repeated init (even when the Native UUID is deduplicated), session changes and
graph conflicts disable further claims in the epoch. Remote, SendMessage,
namespaced/unadvertised tools, resume keys, invalid input/results, caller or
native-ID contradictions and multiple result blocks cannot supply proof.

The implementation makes an additional conservative choice: even the foreground
route requires a complete builtin request and resolved tool ancestry. Missing
those leaves a creation gap. Once a foreground creation is proven, a later work
failure does not undo that historical fact. Contradictions remain diagnostics;
previously delivered records are not rewritten. UUID history has no eviction;
capacity fails visibly instead of risking duplicate creation. No automatic
restart/replay exists. Unmarked history from an arbitrary producer cannot be
recognized, and cross-epoch exactly-once remains unsupported.

Created does not delay Associated or child Native. A background result may
confirm creation after child text and root result. Parent answer, task terminal,
idle and creation are independent; Raw and original request handles are intact.

## Offline tests (E2)

Public API tests use the existing self-reexecuted original synthetic peer,
OpenTree/Send/Receive and isolated environments. Named coverage:

- TestTreeCreatedForegroundAndLateBackground: explicit foreground; background
  confirmation after root result and uninterrupted child output; separate kinds,
  evidence, identity, known parent and exact native bytes.
- TestTreeCreatedCorroborationOrders: all six request/task/result permutations.
- TestTreeCreatedConservativeGates: false/true/missing/null background, version
  gates, missing init/version/input/result/status/identity, resume/SendMessage,
  remote, MCP names, error and ambiguous results, replay/restart/session/identity
  and tool-owner conflicts. Nonmatching versions are rejected, not supported.
- TestTreeCreatedHistoryAndRootResume: neither root recovery mode creates.
- TestTreeCreatedNestedParallelAndEntityDedup: nested/parallel native identity,
  identical descriptions, known tool ancestry, later work without second create.
- TestTreeCreatedSurvivesAliasMerge: independent alias merge retains entity bit.
- TestTreeCreatedNestedBackgroundCaller: nested results must belong to the initiating child, never the root caller.
- TestTreeCreationEvidenceBound and TestTreeCreatedUUIDCapacityDoesNotEvictEntity:
  bounded failure with accepted Raw preserved; no eviction/recreation.
- TestTreeCreatedDuplicateInitBoundary: UUID suppression cannot hide restart.
- TestOfflineExample: actually executes the current-shape example, verifies all
  13 original Native.Raw frames and 2 Created records, nested parent, late
  background proof, patch-only killed terminal, Close/closed/EOF.

The example emits 21 wrapper records, 13 Native and 2 Created (grandchild
foreground and child late background). Fixture SHA256:
`c8876854893748e37bed52b348daeb7fc0a39ee8aa2fdc490bb862e15111f720`.

Validation environment: macOS darwin/arm64, existing mise Go **1.26.9**;
replacement HOME/GOCACHE under /tmp, GOTOOLCHAIN=local, GOENV=off, GOPROXY=off,
GOSUMDB=off, CLAUDE_GO_LIVE=0, GORACE=atexit_sleep_ms=0. Commands and final
outcomes are recorded in the delivery report: full test, full race, vet, explicit
live-skip gate, types-only dependency check, executable example, diff/check and
committed formatting gate. Local logs/downloads remain under /tmp, outside Git.

**E3/E4: not run.** Linux/macOS CI checks the exact pushed commit offline; its
run URL, full remote SHA and job conclusions accompany the final delivery. A
version probe, source declaration or synthetic passing fixture is never labeled
live interoperability validation.

## Plan and remaining limits

Matt to-spec and to-tickets were read locally before implementation. The approved
single-ticket supplement is retained under .scratch/subagent-events as
spec-ticket06.md and issues/06-proven-created.md, including the superseding
current-version amendment. The tracker is local Markdown; no remote issue was
created. The original task13 plan and other repositories were not modified.

Remaining boundaries are intentional: missing proof gives Associated only;
current CLI has no documented run identity; observation relies on native
forwarding and emitted task/structured-result evidence; old/unknown versions do
not gain Created. No live validation, child attach/resume, hook installation,
history file watcher, durable cursor, automatic retry, aggregated usage or child
answer concatenation is claimed. See the complete compatibility matrix.

## Executed local gates

| Check on final production code | Result |
| --- | --- |
| Full `go test -count=1 -timeout 180s ./...` | PASS; final serial root 11.537s, example 1.867s, types 1.481s |
| Full `go test -race -count=1 -timeout 300s ./...` | PASS; root 30.543s, example 2.748s, types 3.456s; no race report |
| `go vet ./...` | PASS, exit 0 |
| Explicit TestLiveCLIInitialization gate | PASS with LIVE SKIPPED: CLAUDE_GO_LIVE=0; no session/model initialization |
| `go list -deps` on types | Only nonstandard dependency: the types package itself |
| `go run ./examples/subagents-offline` | PASS; 21 wrappers, 13 Native, 2 Created |
| `git diff --check` and relative documentation links | PASS |
| Mermaid source match/render/visual review | Three diagrams PASS; github-light SVG/PNG previews rendered under /tmp, no renderer dependency added to repo |
| Committed check-changes gate and Linux/macOS CI | Exact commit/run outcome returned in delivery report |

An earlier full normal suite run concurrently with race/vet reported one existing
TestProcessGroupTeardownAndDescendantHoldingStdout/lifecycle_tree shutdown timeout.
Its isolated rerun passed (root 2.224s); subsequent serial full normal suites passed.
No teardown code or timing threshold was changed to conceal that transient failure.

The added [Chinese event-sequence guide](../event-sequences.md) covers foreground/
background invocation, initial dispatch, tool receipt versus final task state,
interleaved child streams, Associated/late Created, independent parent/child
state, opt-in idle, approval/question authority, completed/stopped/deleted
semantics, absent current run IDs, replay and disconnect gaps. Three Mermaid
sources live under docs/diagrams and match the embedded blocks. Diagrams are
illustrative permissible orders, not live traces or a promised global order.
Current local binary static offset 160562614 confirms session-state emission is
gated by CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS; no live emission was tested.
