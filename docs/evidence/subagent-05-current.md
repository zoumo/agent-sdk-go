# Ticket 05 follow-up — match the installed Codex 0.162.1

Baseline: main `c0d7201f80e6c766835ec2e9d8c6d5721acaf046`, whose initial
Linux/macOS offline run [39101437569](https://github.com/zoumo/codex-sdk-go/actions/runs/39101437569)
passed. The user then requested adaptation to the current installed version,
without an old-version compatibility requirement. Current README, compatibility
matrix, generator pin, source hashes and method inventory now target **0.162.1**.
Earlier 0.160.1 evidence remains historical; no negotiation or compatibility
branch for it is maintained.

## Read-only installed-version check

On 2026-10-18 UTC, `command -v codex`, `which -a codex`, and `codex --version`
confirmed that PATH first selects the Bun global @openai/codex launcher. Its
package version and its Darwin arm64 platform binary both report **0.162.1**.
The binary was invoked with **--version only**, never app-server or a model call.
The binary SHA-256 is
`74c6a6d263f40d65f99c27a815275f5b4ed0bcc2baecc396b4cf3255498bdfc0`.
Local installation paths are excluded from the public record.

The official release tag rust-v0.162.1 peels to
[`092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`](https://github.com/openai/codex/tree/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d),
whose Cargo workspace is 0.162.1. Source was read from that immutable release,
without upgrading the installed CLI or changing any existing upstream checkout.
Matching version labels and binary hashing identify what was inspected; they are
not proof of a reproducible upstream binary build or runtime workflow coverage.

## Current contract differences and changes

Eight definitions in the 33-root generated closure changed relative to the
historical pin:

| Native definition | Current difference / SDK handling |
| --- | --- |
| ThreadItem | SubAgentActivity model and reasoningEffort now typed, with omission/null/value preserved |
| Turn | rootTurnId retained separately from native thread and current turn identity |
| TurnStartParams | parentTurnId/rootTurnId available unchanged on wire; a cyber-program description also changed |
| MessagePhase | partial_answer added; it never implies completion or a synthesized parent final answer |
| CodexErrorInfo | Current anyOf explicitly permits arbitrary string/object fallback; generator keeps opaque objects in Unknown |
| MisalignmentErrorDetails | Native opaque reviewTarget retained; presence grants no continuation capability |
| ResponseItem | Native additional_tools represented; no new tool workflow certification |
| CyberAccessProgram | Native description updated; no policy or authorization change |

There are still 154 definitions, now generating **399 types / 750 synthetic
round-trip cases**. The complete registry now has **173 client requests, 11 server
requests, 86 server notifications, 1 client notification**, plus 24 controls.
Three newly declared requests and thread/prediction/updated are inventoried only;
no additional SDK workflow was implemented. All source blob IDs, SHA-256 digests,
archive/aggregate/snapshot hashes and method source lines were refreshed together.
Historical evidence records retain schema_protocol_commit and do not become
current-schema test passes just because the target changed.

**Historical conclusion corrected:** the V2 limitation in the following paragraph
was corrected by [the V2 creation follow-up](subagent-v2-created.md). The paragraph
records the earlier implementation, not the current API contract.

Current V2 spawn sends collab details to analytics and emits activity Started
to the execution stream. Started alone never confirms Created under this SDK
contract; native lineage can establish Associated. This current-mode limitation
is explicit in the matrix and [事件时序](../event-sequences.md). V1 completed
spawn collab evidence remains the supported Created criterion.

Current auto-attach still attempts all initialized connections on new-thread
creation, with lag skipping resync. try_attach_thread_listener is still explicitly
best-effort. Native per-thread status logic is byte-identical; TurnComplete still
awaits that thread's note_turn_completed before handle_turn_complete. Collab
creation criteria and parent/source lineage remain usable. New metadata does not
justify changing any root membership, creation, deduplication or lifecycle rule.

The generator initially rejected the new open union. Its narrow raw fallback
support now follows that native schema and retains known typed branches when
valid, unknown/ambiguous/opaque object values otherwise. Non-string/object input
still fails decoding. The initial new transport fixture referenced an enum
constant for a type that is a string alias; that fixture-only build error was
corrected before validation. No schema or assertion was weakened.

The [Chinese event-sequence guide](../event-sequences.md) includes six Mermaid
sequence diagrams, 16 JSON examples and a table of guaranteed partial orders.
All six diagrams rendered with beautiful-mermaid 1.1.3 / github-light and were
visually checked as PNGs. The Chinese style check found zero mandatory or
advisory findings. Rendering tools and private QA outputs stay outside the repo.
The 65 inspected source blob IDs were independently matched to the official
exact-commit Git tree; source bytes, registries and schema memberships also passed.

## Offline validation and scope

Final current-version normal suite passed in 44.095 seconds for
the SDK package. Full race passed in 220.345 seconds, with
GOMAXPROCS=2 and package scheduling -p=1 on the shared host. Vet, cached modules
and all six gate self-tests passed. All three live tests visibly SKIP.

The first current full race attempt failed existing blocked-writer prefix and
construction timing assertions, with no race report. Host load was approximately
12. The unchanged assertions then passed two focused race repetitions (31.790
seconds) and the resource-bounded complete suite. No production deadline, test
timeout or assertion was relaxed. Hosted CI uses its ordinary full commands;
its exact-delivery Linux/macOS result is reported separately.

Current public-API contracts cover typed activity metadata, source-separated
turn-chain IDs, partial_answer, explicit rootTurnId null, opaque current error
objects and child events after parent completion over stdio/loopback WS/WSS.
The existing turn-start contract verifies exact parentTurnId/rootTurnId bytes in
the captured transport request. Codec cases exercise current native raw errors,
including empty, unknown, ambiguous and known-key opaque objects; invalid kinds
remain rejected. The runnable offline example retains its complete transcript.

Use the same offline environment and commands as the
[original validation](subagent-05.md#e2-commands-and-exclusions), with live flags
all 0 and GOPROXY/GOSUMDB off. Current records `subagent05-current-*` in
[evidence.json](../protocol/evidence.json) retain exact commands, UTC time,
baseline and source/test hashes. Full official-source verification used the
installed zstd CLI adapter documented in the original report. Hosted CI must be
read back at the follow-up delivery SHA separately; it is returned in handoff.

All five ticket behaviors are accepted only within the current E1/E2 contract.
Native auto-attach remains best-effort and live-unverified. No automatic child
resume/read/list/start, no replay, permanent-gap on disconnect, history Associated
only and no cross-epoch exactly-once guarantee. Parent/child status and observation
lifetime remain independent; idle is not turn/completed. Exact Raw and original
permission/question answer authority remain. ACP is draft reference only.
No E3/E4, actual provider integration, real model or authentication change was run.
