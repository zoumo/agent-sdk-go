# Load history through native event types

Status: approved for implementation, 2026-10-22. Local file tickets are the configured tracker.
Supersedes the separate public snapshot-consumption contract in async-history.

## Problem Statement

Consumers currently need a separate Load record parser even when they already
handle native real-time events. Excluding deltas does not remove this mismatch:
Codex returns complete turns and Claude returns transcript entries.

## Solution

Load delivers retained history using the same provider-specific event types as
real-time consumption. An application uses the same message and tool handlers
for both sources. Replay carries explicit historical provenance and original
source evidence; it does not claim that projected history was received live.

## User Stories

1. As a Codex consumer, I want Load events to use the subscription Event type so that one handler processes retained and live content.
2. As a Claude consumer, I want Load events to implement ReceivedEvent and carry the normal native Event value so that my existing type switch works.
3. As a consumer, I want ordered full messages so that I can rebuild a conversation without delta reconstruction.
4. As a consumer, I want recorded tool calls and results so that I can display prior work without executing tools.
5. As a consumer, I want retained thinking and compaction content so that history does not silently lose those records.
6. As a consumer, I want unknown native data preserved so that future item and content variants survive replay.
7. As a consumer, I want explicit replay evidence so that history does not trigger live creation or permission handling.
8. As a consumer, I want original raw source data so that I can inspect projection gaps without mistaking constructed envelopes for wire captures.
9. As a consumer, I want existing native identities retained so that messages and tool results remain correlated.
10. As a consumer, I want missing information left unknown so that the SDK does not invent lifecycle, timestamps, deltas or answer authority.
11. As a consumer, I want late failures and cancellation visible so that partial replay is never mistaken for complete history.
12. As a consumer, I want native resume completed before delivery so that Load still restores the same session before continuation.
13. As a consumer, I want slow-consumer backpressure and cleanup preserved so that migration does not create an unbounded second backlog.
14. As a consumer, I want sidechains and independent subagent histories kept outside the selected main conversation so that scope does not expand silently.

## Implementation Decisions

- Keep Codex and Claude independent; introduce no provider-neutral public event model.
- Each load handle exposes Events, Done, Err and Close. Events is the replay stream; the caller drains it, waits for Done and checks Err before continuation.
- Codex Events yields the exact same Event type as Subscription.Events. Claude Events yields ReceivedEvent with the same native Event values used by Client.Receive.
- Replace the separate public Records consumption contract; migrate in-repository callers and tests. Keep source indexing and validation private rather than making callers parse snapshots.
- Attach SDK-only history-source metadata to replay events, excluded from native JSON codecs. It includes exact original source bytes and recorded identity/time evidence. Live events have no history-source metadata.
- Codex projects each retained item, in native order, as an item/completed notification value carrying the original full item. This is a historical item snapshot, not proof that an interrupted or failed tool succeeded. Its native status remains authoritative.
- Codex emits a terminal turn/completed value only when the retained turn has a terminal status, retaining its error/status and complete turn. Do not emit fabricated turn/started or item/started updates or delta data.
- Codex constructed notification Params may use the normal native schema; Raw must not claim to be a received native envelope. History-source Raw contains the exact original item JSON for item snapshots and exact full-turn JSON for terminal/unknown turn snapshots. Do not duplicate a whole large turn for every item.
- The native item/completed type requires a completion timestamp that full-turn history does not retain. Replay Params omit that field, history-source MissingFields explicitly identifies it, and the typed zero value is unknown rather than observed. Generated native schemas are unchanged. An unknown future turn status uses Event with empty Method and a native Turn value rather than inventing a terminal notification.
- Claude projects retained main-chain entries through the ordinary native event decoder, preserving full message blocks, tool identities, unknown data and recorded system/progress/attachment entries. Native envelope identity is explicitly mapped from validated transcript identity. Exact transcript bytes remain separately available as history-source evidence.
- Historical question/permission content is data only. It creates no answer handles, sends no responses, changes no live execution state and confirms no live subagent creation.
- Preserve same-session resume, input gating, failure cleanup, bounded pagination/indexing, source mutation detection, main-chain selection, compaction boundaries and cancellation behavior.
- Replay type compatibility does not imply original live-event sequence reconstruction, missing token recovery, live-notice replay or recursive child-session loading.

## Testing Decisions

- Primary seams are public Load/Events/Done/Err/Close and the provider's existing live consumption interface. A single event handler must accept both historical and live event values without parsing a HistoryRecord.
- Use existing offline native protocol peers and transcript helpers; no real agent, model task or new recording.
- Test one vertical slice at a time: Codex event delivery, Claude event delivery, then cross-provider contract and documentation migration.
- Verify content and order using literal expected native event values, including tools, unknown variants, interrupted status and compaction; preserve existing lifecycle, late-error, cancel, mutation and resource tests.
- Validate exact original raw source evidence separately from projected native envelopes.
- Run pinned Go focused tests during implementation, required offline/race/vet/generation/dependency checks at completion, and independent Standards and Spec reviews.

## Out of Scope

- ACP adoption, generic cross-provider events, MASS implementation changes, new live tasks or recordings, approval restoration, source-history edits, path-alias fixes, push or release.

## Further Notes

Proposed local ticket frontier: Codex replay events; Claude replay events; migrate
consumers/documentation and run acceptance. The final integration ticket depends
on both provider slices. Native fidelity and explicit unknown information take
precedence over making replay look like an exact live recording.
