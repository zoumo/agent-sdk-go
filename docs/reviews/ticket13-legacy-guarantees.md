> Historical evidence for the pre-removal baseline. Load and its contracts have been removed; this report is not a current API guide.

# Ticket 13 legacy guarantee audit

Fixed historical base: `3f1776e4da94cc53d48e5029b20bd254dd727152`.
This audits removed runtime seams and test bodies against the
[Session design](../../harness/modules/native-session/design.md),
[interfaces](../../harness/modules/native-session/interfaces.md),
[event delivery](../../harness/contracts/event-delivery.md), and
history (removed Load design). The coordinator inventory is
`.scratch/session-refactor/legacy-regression-inventory.md`.

“Public” means the retained guarantee runs through Open/Load/Submit/Receive and
original native answer handles. “Mechanical” means an independent codec or queue/
graph guarantee. “Superseded” identifies an assertion that contradicts the new
contract, not permission to discard the native payload or transport behavior.
Historical evidence results remain historical; no old race/live result certifies
these replacement tests. Current execution results belong in the progress report.

## Runtime removal

Codex no longer has Subscription, Client.Requests, subscription routing/bridge,
held/pending/seen/dedup state, derived delivery queues, or LoadedThread lifecycle.
The native decoder, optional identity parser, request registry, raw transport and
writer fences remain. Claude no longer has public Client/NewClient, managedTree,
run/creation/UUID ledgers, native event FIFO, callback worker pool or separate
history stream. Its private interaction owns process/control/write mechanics;
Session owns the sole FIFO; the private interaction retains the original
permission registry. Neither native SDK
imports the other. Native generated roots and real captures are unchanged.

## Codex historical tests

Old names below omit `Test`; replacement names omit `Test` where unambiguous.
The Session-prefixed native replacements are in
[codex/session_native_regression_test.go](../../codex/session_native_regression_test.go)
unless a different file is named.

| Historical function | Retained guarantee and current check; superseded assertion |
|---|---|
| TreeLiveChildStream | Public SessionLegacyNativeChildEvidence, SessionMembershipOrderDuplicatesAndIsolation, turn_contract SessionOwnsPayloadAndNoReplay: exact Raw/Params/typed unknowns/malformed child; same-root broadcast, creation-before-native and dedup removed. |
| TreeFailedSpawnAndResumeAreNotCreated | Public SessionLegacyNativeChildEvidence: successful/failed/resumed native frames and both repetitions; no inferred creation or membership from failure/resume. |
| TreeBoundsAndLocalClose | Public session_test SessionSlowConsumerAndConcurrentReceivers, SessionSharedConnectionIsolationAndPendingClose and turn_contract SessionConcurrentLifecycle; Close now releases the owner with native detach. |
| TreeAdmissionAndNodeBound | Public turn_contract SessionAdmissionValidation, SessionOverflowIsolatedAndInspectable and session_test SessionFaultCleansUpAndPreservesAcceptedFacts; retired subscription option validation removed. |
| TreeCurrentVersionMetadata | Public SessionCurrentVersionMetadata: original root turn string/null, model/effort, partial answer and error matrix. |
| TreeNestedOrderingAndIdentity | Public SessionNativeNestedConflictsKeepFacts and SessionMembershipOrderDuplicatesAndIsolation: nested source, cycle/conflict/unknown facts; ancestor-first release and dedup counts removed. |
| TreePendingLimitsAndNoReplay | Public SessionQuarantineBoundsExpiryAndCopies and membership-order test; bounded unknown bytes remain diagnostic, never later inserted into live FIFO. |
| TreeDepthAndDedupBounds | Public SessionParentDepthBoundsKnowledgeNotMembership; graph depth bounds knowledge, never native membership; dedup ledger/eviction removed. |
| TreeV2CreatedWithoutThreadStarted | Public SessionV2StartedActivityAndDuplicateFacts: completed started activity proves membership, earlier child stays quarantine, duplicates survive. |
| TreeV2HistorySuppressesCreation | Public history_events TestLoadHistoricalV2ActivityKeepsFactsWithoutLiveMembership: actual Load retains typed activity, unknown large integer, exact History.Raw and no live membership/authority; caller seed API removed. |
| TreeV2GraphOrderingAndConfirmation | Public SessionNativeNestedConflictsKeepFacts, turn_contract SessionOwnsPayloadAndNoReplay; mechanical GraphSuite conflict/cycle atomicity; PreviousEvidence convenience projection removed. Mutation isolation is in the turn-contract test, not the nested test. |
| TreeV2NonCreationEvidence | Public SessionV2NonMembershipEvidence: all original 14 negative cases, including empty item identity; no synthetic creation. |
| TreeParentIdleChildActive | Public SessionParentIdleChildActive: parent idle/interrupted/result, child continuation and next turn remain native, distinct. |
| TreeCloseCancelSharedSource | Public session_load observed-descendant/owner-precedes-ancestor tests and turn_contract SessionConcurrentLifecycle; canceling a wait does not close Session, Close revokes its owner. |
| TreeDisconnectHistoryEpoch | Public SessionConnectionFailureDrainsThenReportsCause, history/load suites and SDK-US-017/018; no derived disconnect terminal or creation cache. |
| TreeDisconnectFullQueueAndSeedBounds | Public SessionLargeFaultBacklogAndConcurrentAbort: original 8192 ordered facts, concurrent Close/Abort/Err; history resource tests retain limits; derived reserved disconnect slot/seed parser removed. |
| TreeFanoutAdmissionIsAtomic | Public SessionSlowConsumerAndConcurrentReceivers and native observation checks; mechanical queue batch-close race; fixed-capacity derived fanout counts removed with that runtime. |
| TreePendingDeadlineFanoutIsAtomic | Public SessionQuarantineBoundsExpiryAndCopies; no timed release of unknown facts into FIFO. |
| TreeFullQueuePreservesOriginalRequestAuthority | Public SessionChildRequestBacklogEpochAndWaitCancellation, SessionRequestPrewriteRevocationAndConcurrentAnswers and mock consumer 2048-fact case; no second approval queue. |
| TreeObservationCancellationDoesNotCancelRequest | Public SessionChildRequestBacklogEpochAndWaitCancellation: wait cancel retains original handle; Session.Close revokes owner authority, unlike old observer Close. |
| TreeRequestTerminalAndEpochIsolation | Public child request table (cancel/resolved/close), fresh same-ID epoch and SessionTerminalRequestReplayNeverRestoresAuthority. |
| TreeTextThinkingAndUsageStayAtNativeSource | Public SessionTextThinkingAndUsageStayAtNativeSource: exact reasoning/text, independent parent/child usage and final answer. |
| TreeQueueCapacityOverflowIsRejected | Public limit constructor/request tests retain integer bounds; subscription capacity field removed, no fixed Session FIFO overflow. |
| SubscriptionNativeDeliveryAndConvenienceData | Public SessionLegacyNativeChildEvidence, SessionCurrentVersionMetadata and SessionRequestFIFOAndOriginalRaw; native/SDK distinction uses Received rather than invented Data variants. |
| SubscriptionDeliveryLifecycle | Public SessionLargeFaultBacklogAndConcurrentAbort, SessionOpenSubmitReceiveAndDrain, SessionAbortDiscardsAbandonedBacklog; mechanical queue tests; old delivery goroutine removed. |
| SubscriptionConcurrentAbort | Public large-backlog and SessionConcurrentLifecycle races. |
| SubscriptionBatchAdmissionOrder | Mechanical queue batch/copy tests; public SessionOwnsPayloadAndNoReplay; derived batch bookkeeping removed. |
| SubscriptionSharedLineageResolvesLateAncestors | Mechanical GraphSuite UnknownParentAndAncestors/ConfirmRejectionsAreAtomic; public nested native facts; reachability no longer gates membership. |
| SubscriptionCountsUnknownParentOnlyVertices | Mechanical GraphSuite TestUnknownParentVerticesHaveLiteralCapacity: literal 2-vs-3 capacity, rollback; graph limits do not terminate native membership. |

`turn_contract_test.go` preserves the native bodies under Session names:
TurnEarlyEventsKeepResponseIdentity, NotificationRawAndDecodeErrors,
SessionOverflowIsolatedAndInspectable, SessionSlowConsumerNeverBlocksResponse,
SessionOwnsPayloadAndNoReplay, CancellationDoesNotInterruptTurn,
TurnFailureNeverReplays, SessionTerminalStatus, SessionConcurrentLifecycle and
SessionAdmissionValidation. The backlog stays 4096 and the RPC barrier 3 seconds;
concurrency stays 64 distinct owners. These functions remain in the remote parity
table. The new observation suite tests explicitly cover stdio, WS and WSS using
actual loopback transports. No transport proof is inferred from a mock name.

`request_contract_test.go` and `requests_lifecycle_test.go` retain exact native
answer shapes, malformed known requests, unknown/null/absent diagnostic bytes,
numeric-vs-string reverse ID collision (actual pending outbound ID 3), preflight,
one-shot races, queued NotSent, noncooperative marshaler, partial-write
OutcomeUnknown/no retry, request lifetime, duplicate/retired IDs, capacity and
startup quarantine. They consume actual ReceivedRequest handles, with explicit
ownership on valid native requests. The native resolution event is consumed
before asserting a fresh same-ID request. Old queue overflow becomes explicit
active/terminal-identity registry capacity; unowned startup material cannot grant
late authority. Raw diagnostics cannot be used to manufacture a handle.

## Claude historical tests

For compact references, these current groups are exact test suffixes, all with
`TestSession` prefix in session_test.go or session_native_regression_test.go:

- ARR: PreservesArrivalAndDuplicatesWithoutParent.
- PARENT: ParentKnowledgeCanArriveAfterChildWithoutReordering.
- QUAR: QuarantineIsBoundedAndNeverBackfills, QuarantineByteBoundsAndSnapshotCopies,
  WaitCancellationAndQuarantineExpiry.
- DEPTH: DepthRejectionDoesNotRejectMemberOrSortRuns.
- CONFLICT: RejectsCyclesConflictsAndToolAliasesWithoutLosingFacts,
  NonAgentFactsAndAliasConflictsNeverFabricateIdentity.
- NATIVE: NativeUsageTerminalAndSnapshotsStaySeparate,
  NativeTaskTerminalMatrixAndNoRunOrdering.
- BOUNDS: ActiveObservationBoundsRejectBeforeSpawn,
  MemberOverflowAndToolKnowledgeLimitRetainRaw.
- CLOSE: CloseDrainsAndAbortDiscards, ConcurrentCloseWithUnreadFactsAndLongQuarantine.
- START: ForwardingBeforeInitializeAckAndFreshEpoch; also existing
  TestStartupFailuresAreBounded, TestStartupFailureReleasesUnconsumedNativeQueue,
  and clientProtocolSuite literal argv/matched-initialize tests.

| Historical function | Retained guarantee and current check; superseded assertion |
|---|---|
| ForwardSubagentTextHandshakeAndOptions | Public START: literal model/argv, forwardSubagentText true, hooks, matched ack; native ordinary ResumeID remains supported with no replay. |
| TreeSingleLayerNativeLosslessAndAssociationOrder | Public ARR/NATIVE/PARENT plus receive_test TestReceiveConcreteEventsAndNativeJSON exact whitespace and Origin absent/null/object; association-first release removed. |
| TreeUnresolvedAndNonAgentTask | Public QUAR/CONFLICT; unresolved bytes stay diagnostic and non-agent facts cannot create aliases. |
| TreePermissionUsesOriginalHandleAfterParentResult | Public permission/control tests below plus NATIVE: parent result never closes Session or replaces original permission handle. |
| TreeBoundsRejectionAndCloseRace | Public BOUNDS/CLOSE/START: resource rejection, unread-close and failed-startup cleanup. |
| TreeForwardingBeforeFirstNativeAndDisconnect | Public START and TestUnexpectedEOFAfterAcceptedEvent: early exact bytes, drain then fault. |
| TreeUnknownSystemSubtypeNeedsSourceEvidence | Public UnknownEnvelopeCannotBorrowMemberIDAndStreamUsesVerifiedAlias: unknown envelopes cannot borrow an identity; diagnostic Raw preserved. |
| DefaultManagedOpenAndNativeOriginBoundary | Public START plus receive_test exact native Origin codec comparisons. |
| DefaultManagedRejectsTreeBeforeSpawn | Public BOUNDS; retired fanout options removed. |
| TreeModernRecursiveRunsAndLateFrames | Public DEPTH/NATIVE: exact older run/late facts; newest/current/stale projection removed. |
| TreeLegacyRecursiveToolOwnershipAndEnrichment | Public DEPTH and verified-alias test; no tool-only inferred membership or extension identity merge. |
| TreeChildBeforeRelationUnknownParentAndNoDepthGuess | Public PARENT/DEPTH: optional parent can arrive later without sorting/replaying facts. |
| TreeNonAgentsHooksAndFailedSpawnNeverCreated | Public CONFLICT: hook/non-agent/failed receipt Raw retained, no aliases; Created API removed. |
| TreeUUIDDedupFullScopeAndNoIDPreservation | Public ARR/NATIVE/QUAR: duplicates and UUID-less facts survive; all UUID suppression removed. |
| TreeLineageConflictsDoNotRebind | Public CONFLICT; original facts survive conflicts, no poisoned alias. |
| TreeAdditionalBounds | Public BOUNDS/DEPTH; dedup-cache options removed. |
| TreeIndependentAliasesReconcileWithBridgeEvidence | Public UnknownIdentityExtensionsDoNotMergeTaskOrParent; unverified task/agent equivalence and alias merge removed. |
| TreeStaleMetadataCannotPoisonCurrentRun | Public NATIVE/CONFLICT; no SDK run ordering. |
| TreeKnownNonAgentToolDoesNotCreateLegacyAgent | Public CONFLICT, retaining hook/tool/failed receipt cases. |
| TreeConflictingToolAliasDoesNotPoisonTaskKey | Public CONFLICT with independent b/tool-b continuation after same-task tool conflict. |
| TreeResumeNewToolAndDuplicateRelationships | Public NATIVE/CONFLICT; duplicate native receipts survive, no derived/run dedup. |
| TreeNoRunUUIDNotSuppressedAcrossObservedRunBoundary | Public ARR/NATIVE; no inferred run boundary suppression. |
| TreeRunTerminalsAndBackgroundLevel | Public NATIVE: status, background level, patches and exact Raw; no derived terminal/current-run state. |
| TreeTerminalKindsAndReceiveCancellation | Public NATIVE, wait-cancellation and unexpected EOF tests. |
| TreeHistorySeedAndFreshEpoch | Public ForeignHistoryCannotSupplyLocalParentAlias and START: foreign seeds cannot supply local aliases. Explicit same-root caller-verified SubagentOptions.History still supplies member/parent knowledge; it never creates answer authority. |
| TreeRunLedgerBound | Public LongMainConversationHasNoUUIDLedgerLimit; run ledger removed. |
| TreeUnclassifiedRunCannotRegressAgentMetadata | Public NATIVE/CONFLICT; no stale/newest sorting. |
| TreeBufferedSourceResolvesBeforeNativeWithoutReplay | Public PARENT/ARR; holding/reordering removed. |
| TreeUnresolvedExpiryPreservesRawAndOnlyEnrichesLater | Public QUAR/PARENT; no retroactive backfill. |
| TreeUnresolvedCapacityFailsVisiblyWithoutPartialFanout | Public QUAR: count, bytes, oversized frame, expiry, copies and coverage gaps; terminal held overflow replaced by diagnostic eviction. |
| TreeAtomicFanoutAndHistoryBounds | Public BOUNDS; second pending FIFO/fanout capacity removed. |
| TreeNewBoundsRejectBeforeStartup | Public BOUNDS: all active limits reject before process spawn. |
| TreeFullQueueControlResponseAndOriginalQuestionAuthority | Public same-stream permission/question tests and 2048-fact mock consumer; control response independent of unread FIFO. |
| TreeQuestionWithHeldChildUsesSoleHandleAndKeepsInput | Public TestSessionPermissionExtensionsAndQuestions and question_interaction tests: original handle, question text/unknown input; no held child gate. |
| TreeCancelledQuestionOldEpochAndCloseRace | Public SessionPermissionCancellationCloseAndSnapshot, SessionPermissionConcurrentAnswersAndClose and retained control tests: native cancellation, epoch isolation. |
| TreeParentResultChildThinkingAndUsageStaySeparate | Public NATIVE: exact child text/thinking/usage, parent cost/result distinct, child continues. |
| TreeBacklogThenProtocolFailureWakesPendingInterruptAndQuestion | Public overload TestBacklogThenProtocolFailureWakesInterruptAndPermissionWithRetainedOrder and SessionFaultDrainsAcceptedFactsAndKeepsFirstCause. |
| TreeHeldFramesDoNotRelaxDenyOrUnknownControlPolicy | Default deny superseded by explicit original handle; unsupported-control responder remains bounded and tested, no fake answer. |
| TreeHeldFrameCloseDoesNotWaitForAssociationDeadline | Public CLOSE/QUAR with long quarantine/unread facts and concurrent close. |
| TreeBufferedSourceOrderAcrossUnrelatedUnresolvedSource | Public ARR/QUAR; never delay known facts behind unknown relationship. |
| TreeCreatedForegroundAndLateBackground | Public ARR/NATIVE for independent child continuation; no creation matcher. |
| TreeCreatedCorroborationOrders | Creation-specific six-way matcher expectations superseded: Session delivers native arrival order and does not infer creation. Native arrival permutations covered by ARR/PARENT. |
| TreeCreatedConservativeGates | Public FormerCreationInputsRemainNativeFacts preserves all 29 original raw input sequences, exact owned FIFO and foreign quarantine; matcher/Created assertions removed. |
| TreeCreatedHistoryAndRootResume | Public ordinary resume argv/no-replay test and strict Load/history tests; no Created claim. Explicit Load still ErrResumeUnconfirmed, no extra prompt. |
| TreeCreatedNestedParallelAndEntityDedup | Public DEPTH/CONFLICT, nested native input retained; entity dedup removed. |
| TreeCreationEvidenceBound | Public BOUNDS covers surviving member/tool resources; creation cache removed. |
| TreeCreatedSurvivesAliasMerge | Public unknown-identity extension test; unverified alias merge and creation bit removed. |
| TreeCreatedUUIDCapacityDoesNotEvictEntity | Public LongMainConversationHasNoUUIDLedgerLimit; both ledgers removed. |
| TreeCreatedDuplicateInitBoundary | Public DuplicateInitPreservesNativeFactsAndVersionRestraint; repeated init remains a native fact, not deduped. |
| TreeCreatedNestedBackgroundCaller | Public DEPTH/verified-alias tests; background corroboration matcher removed, snapshots remain native. |

Mechanical codecs moved unchanged into `claude/subagent_codec_test.go`:
TestSubagentEnvelopeNullAbsentAndTypedEdits,
TestTaskLineageFieldsLosslessNullAbsentEdits,
TestSessionStateAndBackgroundSnapshotCodec. The synthetic subprocess peer and
creation wire-data builders moved into session_protocol_peer_test.go; they are
not a second SDK runtime.

## Permission/control and transport mechanics

The retained permission, permission_cancellation, question_interaction, overload,
receive and lifecycle test files now open a public Session. Test helpers obtain
actual same-stream permission handles, never registry-manufactured authority or
another growing queue. Exact updated input, unknown request metadata, single-use
concurrent answers, duplicate/retired IDs, history bounds, epoch isolation,
malformed cancellation, control wakeup, transport failure, cancellation across
unread backlog and close invalidation remain in those bodies.

Invalid JSON/null/array/empty/contradictory answers now assert NotSent, no bytes,
and later explicit answer on the same live handle. Callback panic/default-deny/
worker scheduling tests are superseded because the SDK executes no user callback.
Tests for a caller ignoring cancellation retain the handle in a test-owned
caller, and native cancellation still prevents any later response.


The affected permission test entry points map as follows (unchanged names retain
their migrated body in the same file):

| Historical entry | Current classification |
|---|---|
| DeferredPermissionHumanWaitKeepsTransportMoving; DeferredPermissionRetainedHandleUsesExactUpdatedInput | Public, same names: unread reader progress and exact original/modified input via retained handle. |
| AssistantQuestionIsOnlyAnEvent; PermissionCancellationImmediatelyInvalidatesHandle | Public, same names: no question authority from assistant output; native cancel invalidates original authority. |
| PermissionInvalidAnswerAndPanicFailClosed | Public PermissionInvalidAnswerIsNotSentAndReusable retains invalid-input matrix; callback panic/default deny removed with user-code execution. |
| PermissionAnswerConcurrentSingleUse; PermissionCloseInvalidatesAndFreshClientHasNewEpoch | Public, same names: one effective answer and fresh-epoch authority. |
| PermissionWorkerBoundAndCloseDoNotWaitForUserCode | Public PermissionRetainedWaitAndRegistryBoundDoNotBlockClose retains request bound and nonblocking close; SDK worker scheduling removed. |
| PermissionDuplicateIDCannotCreateSecondAuthority; PermissionLateValidControlDoesNotMakeCloseProtocolError | Public, same names; late post-Close material cannot become new authority/admitted facts. |
| PermissionCanceledCallerContextDoesNotConsumeHandle | Public, same name: pre-canceled NotSent preserves usable handle. |
| PermissionQueuedCancellationNeverInvokesHandler | Public PermissionQueuedCancellationPreservesOtherHandles: native cancel invalidates only its original handle; callback invocation expectation removed. |
| PermissionPanicAfterAnswerDoesNotCreateSecondResponse | Superseded callback panic execution; public PermissionAnswerConcurrentSingleUse and same-stream answer tests retain exactly one native answer. |
| PermissionZeroHandleCannotAnswer; IncomingControlHistoryRejectsRetiredIDReuse; PermissionRegistryBoundsFailClosed | Public, same names: local rejection, retired-ID prevention and explicit registry limits. |
| PermissionTypedCancellationIncludesUnknownAndCompletedIDs | Public, same name, retaining unknown/completed native IDs and raw cancellation. |
| PermissionFailedAttemptRemainsDistinctAfterClose | Public PermissionLocalRejectionRemainsNotSentAndCloseInvalidates: invalid local attempts leave live authority until Close; no old fail-closed poison. |
| PermissionCancellationCrossesBacklogAndCloseInvalidatesOtherHandles; PermissionUnknownCancelHistoryExhaustionIsExplicit | Public, same names: unread FIFO independence and bounded cancellation history. |
| PermissionCancelledIgnoringHandlerCannotAnswerLater | Public PermissionCancelledRetainedCallerCannotAnswerLater: test-owned caller cannot answer after native cancel; no SDK callback. |
| PermissionDisconnectInvalidatesPendingHandle; PermissionMalformedCancellationIsProtocolFailure | Public, same names: pending authority revoked, exact protocol failure classifications. |
| ConfigurationRejectsIntegerOverflowBeforeSpawn; EventBacklogStillDispatchesControlResponses; BacklogThenProtocolFailureWakesInterruptAndPermissionWithRetainedOrder; TransportFailuresWakeAllPendingWork | Public, same names, preserving limits, failure matrix, wire/control wakeup and ordered drain. |
| ShutdownDoesNotWaitForIgnoringCallback | Public ShutdownDoesNotWaitForRetainedCaller: user caller cannot block SDK teardown; SDK callback execution removed. |


Linux permission wire tests retain actual FIFO/prefix barriers, original payload
sizes and deadlines. Queued native cancel writes no answer; queued caller cancel
returns context.Canceled/NotSent, leaves the interaction and authority live,
allows the unrelated started user write to finish, then retries exactly once.
Close still revokes. Started-write cancellation/close stays OutcomeUnknown and
cannot retry. Native control response overload still terminates explicitly.
Cross-compilation is not Linux runtime evidence.

Synthetic transport marker envelopes use a strict marker allowlist and explicit
fixture_kind; real future_event frames remain exact diagnostic Raw. No unknown
native variant is rewritten into assistant. Close backlog tests use native
Interrupt acknowledgement as the acceptance barrier; no post-Close material is
claimed admitted. Original 2048 mock facts, deadlines and request answer barriers
remain. The 20 new and 12 old stories stay distinct.

## Historical evidence path migration

`codex/docs/protocol/evidence.json` is unchanged. `test-migrations.json` enumerates
only removed historical paths and exact historical evidence IDs, fixed base,
this audit and existing current replacement files. The inventory verifier rejects
unknown missing paths, unmapped IDs, stale/duplicate mappings and absent current
targets. Source pins, snapshots, hashes, registry/schema membership checks remain.
The manifest is traceability, not a transfer of old test outcomes to new code.
