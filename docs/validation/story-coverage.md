# Phase 1 current Story coverage and evidence

Audit base: `4c03ac701901ce3e37414946f09a56347f620fe7` (2026-10-25),
[zoumo/agent-sdk-go #8](https://github.com/zoumo/agent-sdk-go/issues/8), parent
[#6](https://github.com/zoumo/agent-sdk-go/issues/6). This is the current crosswalk,
not a compatibility certificate. The [19 common goals](../../harness/sdk/user-stories.md)
and [native acceptance supplements](../../harness/sdk/native-stories.md) remain requirements.
Historical [Session report](stories/session-targets.md) retains removed Load/020 results;
those old targets do not describe the current SDK-US-019.

## Evidence vocabulary and source pins

Implementation and evidence are independent axes. **I** = implemented within the
stated native scope; **P** = partial implementation; **N** = not implemented;
**V** = native/version/backend unsupported, a specific operation/condition rather
than a whole common requirement. **M** = missing retained real evidence;
**R-part** = a retained real fragment, never whole-story completion. All tests below
are synthetic/mock-only unless explicitly identified as retained real replay.
Native rejection/unsupported is an outcome to retain, not a synthetic success.

| Pin | Exact source and use |
|---|---|
| C | Codex CLI/schema 0.162.1, upstream `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`; [source hashes](../../codex/docs/protocol/sources.json), [registry and capability inventory](../../codex/docs/protocol/capabilities.json). [Input/config receipts](../../codex/session_config.go), [history backend semantics and pinned handlers](../../codex/docs/history-query.md). No pin upgrade. |
| A | Claude TS SDK 0.3.258 / CLI 2.1.258, manifest commit `b3cd543a1f6fcdf4d8fabc0f5e5538d2ee7f38e1`; archive SHA256 `656cf237bc567cb172a007a0fd5b3958cf960d154c03ab390a755d2c3bdbb398`, declarations `b209ed293b63bb92bde59a49fe8a6e3b06f92afe0d1d4987c937a3cfea90b6dc`. [Fixed wire/declaration positions and hashes](../research/claude-session-native-evidence.md), [request protocol](../../claude/docs/session-requests.md), [input/config scope](../../claude/docs/session-input-config.md). Newer optional lineage fields are not 2.1.258 emission evidence. |
| Q | Claude local queries: Python 0.2.174 commit `0e89b298e771259378a6cd3734e00d323dff5703`, sessions.py; npm 0.3.310 static inspection, SHA512 `fo+sr+9Ks73P001f6XKRQlyzfIBAJMgXArLUkH5Tu98NikRDZeGtFQVj23d5/hj2ijpFgzffrr4fHywY9qUkoQ==`. [Message algorithm/differences](../../claude/docs/session-messages.md), [metadata algorithm/differences](../../claude/docs/session-metadata.md). This additional source reference does not upgrade the recording CLI pin. |

Go source links below refer to the audit base plus this documentation-only work;
no runtime feature is added. C/A/Q are reused on every applicable row. Exact
upstream declarations and hashes are linked above; source availability alone is
not live execution evidence.

## Retained real fragments

Frame numbers are **original capture ordinals**, not JSONL line numbers. Exclusions
remain gaps. Manifests are authoritative for hashes, original source/CLI/build
identity, prompt/input hashes, durations, usage and omissions. No original bytes
or manifests are rewritten.

| Key | Manifest / wire | Retained scope and exact frames |
|---|---|---|
| C1 | [S1 manifest](../../codex/tests/testdata/recordings/real-sanitized/s1/manifest.json), [wire](../../codex/tests/testdata/recordings/real-sanitized/s1/protocol.jsonl) | SDK `d465677969cf968c6245e0adeaf7e32c42356913`, CLI digest `74c6a6d263f40d65f99c27a815275f5b4ed0bcc2baecc396b4cf3255498bdfc0`. 66 retained / 23 omitted. initialize 1–4 (3 omitted); root 5–7; one turn 9–12; dynamic tools 40–43 and 51–54 (requests 41/52, answers 42/53); root completion 89; text 21–35/60–81, usage 46/57/84. No native command/file approval or children. |
| C2 | [S2 manifest](../../codex/tests/testdata/recordings/real-sanitized/s2/manifest.json), [wire](../../codex/tests/testdata/recordings/real-sanitized/s2/protocol.jsonl) | Same SDK/CLI/digest as C1. 28 retained / 17 omitted. root 5–7, one turn 9–12, requestUserInput 24, original Amber answer 25, resolved 26, text 33–37, usage 30/40, completion 45. No deferred-cancel/free-text/native approval/child coverage. |
| A1 | [S1-auth manifest](../../claude/tests/testdata/recordings/real-sanitized/s1-auth-blocked/manifest.json), [wire](../../claude/tests/testdata/recordings/real-sanitized/s1-auth-blocked/wire.jsonl) | SDK `282f85aed93c571d4aaa500751d6786b021897b6`, CLI 2.1.258 digest `0d036f0b635d239844e6df6996bc05263fe305df505b073c71f79f83668ea35a`. All 11 frames retained: initialize 1/4, hooks 2/3, user 5, states 6–8, synthetic auth-error assistant 9, result subtype success + is_error true 10, idle 11. Zero successful generations/tools/permissions/children; native zero cost is not independent billing proof. |

Public real replay assertions: Codex
[`TestManagedRecording/TestRecordedManagedIntegration`](../../codex/tools/replay/recording_test.go)
and Claude
[`TestRecordedObservationSuite/TestRecordedAuthenticationFailureReplay`](../../claude/recorded_replay_test.go).
[Fixture index](../recordings.md) retains expected-observation/ID-map links.
Replay proves handling of these retained fragments only.

Historical live (**H**, not replay): [Codex lifecycle/interaction observations](../../codex/docs/compatibility.md#live-validation)
at separately identified SDK bases include two-turn/history/resume/WS and a BLUE
question; native approvals and interrupt were untriggered, old turn budget **9/9
exhausted**. [Claude bounded observations](../../claude/docs/compatibility.md#bounded-runtime-coverage)
at CLI 2.1.258 include eight inputs, questions, Write and pending-question cancel;
private 655-frame material is not a published fixture, tools ended capped error
results. Do not inspect or republish private material.
**U** = parent-reported unpublished query tests at Codex 0.164.0 / Claude 2.1.310
([#6](https://github.com/zoumo/agent-sdk-go/issues/6)); no retained per-row trace or
public fixture. H/U never fill M below or silently relax C/A.

## Per-goal matrix

**T/NNN** expands exactly to
`TestCommonStories/TestSessionTargets/SDK-US-NNN/{codex,claude}` in
[public suite](../../tests/stories/stories_test.go) with both native adapters and
[acceptance assertions](../../tests/stories/acceptance_test.go), driven by the
[synthetic native peer](../../tests/stories/session_peer_test.go). Rows 001–018
all use this seam. **B** is
`TestCommonStories/TestIndependentHistoryBrowser/US-014/{codex,claude}` in
[history suite](../../tests/stories/history_query_test.go), assertions in the same
acceptance file; 019 is deliberately **outside the 001–018 loop**.
Bundle names and their expected replay/gap outcomes are defined in the
[capture gate proposal](../capture-plan.md). C1/C2/A1 always mean R-part.

| SDK goal | Scenario cross-reference | Codex implementation / source C | Claude implementation / source A (Q for 019) | Published real / missing evidence | Exact synthetic public check | Capture/replay outcome route |
|---|---|---|---|---|---|---|
| SDK-US-001 | US-001 | I: long Session, repeated Submit, one owner; [session](../../codex/session.go) | I: repeated local writes, result leaves Session open; [session](../../claude/session.go) | C1 9–12/89 and C2 9–12/45 each one turn only; A1 5/10–11 failure only; multi-turn M, H not retained | T/001 | R1 two ordinary turns; native continuation or explicit failure, no blanket long-session certification |
| SDK-US-002 | US-001, US-002 | I in supported text/image/native input scope; unknown input rejected; [input](../../codex/session_input.go) | I in text/image/document/tool_result scope; [input](../../claude/session_input.go) | C1/C2 9 text + native parameters partial; A1 5 text failure; media/document success M | T/002; [Claude structured input test](../../claude/session_input_config_test.go) `TestSessionStructuredInputPreservesNativeWire` | R1 text; optional separately approved media extension; unsupported/untriggered stays M |
| SDK-US-003 | US-001 | I: native turn/start during execution, no steer; [input](../../codex/session_input.go) | I: write during execution, no acceptance ack/scheduler; [input](../../claude/session_input.go) | Both M; sequential C1/C2 do not prove interleaving | T/003 | R3 bounded second input; record accept/reject/write-only separately |
| SDK-US-004 | US-001, US-002 | I: submit/tool/root result separate; [session](../../codex/session.go) | I: local write/tool/result/idle separate; [events](../../claude/types/events.go) | C1 9–12,40–54,89 R-part; A1 5,9–11 failure-only R-part, successful tool M | T/004 | R1 terminal + R2 native tool/result separation; never count tool completion as root completion |
| SDK-US-005 | US-010 | I: Raw/unknown/duplicates preserved; [session](../../codex/session.go) | I within native decoding/attribution scope; [session](../../claude/session.go) | C1 21–35/60–81, C2 33–37 and A1 9–11 native fields only; deliberate future/duplicate M | T/005 | R1–R4 native Raw; F unknown/duplicate remains synthetic |
| SDK-US-006 | US-003, US-004 | P: membership routing/attachments, unverified native children; [observation](../../codex/session_observation.go) | P: native task/tool routing; forwarding incomplete/unverified; [subagents](../../claude/session.go) | Both M, all captures zero children | T/006 | Phase 2/S3 deferred; F ordering companions only |
| SDK-US-007 | US-005 | P: root completion keeps confirmed attachments, gaps visible; [observation](../../codex/session_observation.go) | P: result keeps Receive, forwarding not guaranteed; [subagents](../../claude/session.go) | Both M, no parent-first background sample | T/007 | Phase 2/S3 deferred; F parent-first/late-child only |
| SDK-US-008 | US-013 | I bounded native descendant discovery, not cache; [discovery](../../codex/session_discovery.go) | I bounded local existing-child discovery, plus independent child saved messages; [child history](../../claude/docs/subagent-history.md); [discovery](../../claude/session_discovery.go) | Both M; existing code is not missing implementation | T/008; native `TestSessionDiscoveryExistingNestedRecords` / `TestSessionDiscoveryExistingNativeRecords` in [Codex](../../codex/session_discovery_test.go)/[Claude](../../claude/session_discovery_test.go) | Phase 2 controlled stored children; root R4 cannot certify children |
| SDK-US-009 | US-006 | I: original approval with native extensions; [requests](../../codex/session_requests.go) | I conditional on actual can_use_tool, auto-approval may emit none; [requests](../../claude/session_requests.go) | C1 41/52 are dynamic tools, NOT approvals; both native approval M; H unretained | T/009 | R2 exact one native approval allow and one deny, else untriggered gap |
| SDK-US-010 | US-006, US-008 | I delayed Receive/original single answer/revocation; [requests](../../codex/session_requests.go) | I delayed permission/revocation; [requests](../../claude/session_requests.go) | Both M; C2 24–26 correlation does not prove deferred control/cancel | T/010 | R2 hold handle while reading/control; F revoke/late/duplicate/close |
| SDK-US-011 | US-007, US-008 | I question ID to native answers; [requests](../../codex/session_requests.go) | I original AskUserQuestion via updatedInput/full question text; [questions](../../claude/question_interaction_test.go) | C2 24–26 Amber R-part, free text/cancel M; Claude success M, H unretained | T/011 | R2 choice + free text in one request if emitted; untriggered stays gap |
| SDK-US-012 | US-001 | I partial native setting dimensions; V known unsupported; [config](../../codex/session_config.go) | I model/budget/display subset; N effort policy, V invalid combinations; [config](../../claude/session_config.go) | Both M for recorded changes; input model value is not change evidence | T/012; native config tests [Codex](../../codex/session_config_test.go)/[Claude](../../claude/session_input_config_test.go) | R1 creation + dynamic request, preserve native ack/rejection |
| SDK-US-013 | US-001 | I requested/ack/observed separate; effective turn/child scope not inferred; [config](../../codex/session_config.go) | I receipt Accepted with Observed nil; no child recursion; [config](../../claude/session_config.go) | Both M for native confirmation sequence | T/013 | R1 later native observation or explicit unknown, never forced completion |
| SDK-US-014 | US-009 | I FIFO/control/drain; available memory is bound; [session](../../codex/session.go) | I same ownership rule, no total RSS guarantee; [session](../../claude/session.go) | Both M for real slow consumer; model capture alone cannot prove driver stress | T/014 | F slow-consumer driver reuses real input and synthetic bursts, labeled driver evidence |
| SDK-US-015 | US-008 | I local wait cancel vs dispatched unknown; [errors](../../codex/errors.go) | I NotSent vs started-write ambiguity; [errors](../../claude/errors.go) | Both M; exact dispatch races necessarily synthetic | T/015 | F controlled-time/blocked-write, zero models, no resend |
| SDK-US-016 | US-012 | I Interrupt vs owned Close/shared remote distinct; [session](../../codex/session.go) | I interrupt ack vs owned process Close; [session](../../claude/session.go) | C1 89/C2 45 + manifest EOF/reap and A1 10–11 + cleanup are partial terminal/Close only; real interrupt M | T/016 | R3 one bounded interrupt request then explicit cleanup; F precise cancel races |
| SDK-US-017 | US-011, US-012 | I task failure vs transport/terminal drain; [session](../../codex/session.go) | I is_error vs SDK failure, idle and drain; [session](../../claude/session.go) | A1 9–11 authentication failure R-part, no successful dialogue; Codex failure M | T/017 | Preserve A1; F transport failure/drain, no model-manufactured crash |
| SDK-US-018 | US-011 | I report loss; N automatic reconnect (outside contract); [remote](../../codex/remote.go) | I report loss; N automatic reconnect (outside contract); [session](../../claude/session.go) | Both M for retained application-led recovery; H not a fixture | T/018 | F disconnect/new epoch; no restored answer authority |
| SDK-US-019 | US-014 | I list/read/turns/items, V backend method availability; [query](../../codex/history.go), [list](../../codex/client.go) | I bounded main/child local list/info/messages; V other layouts; [metadata](../../claude/session_metadata.go), [messages](../../claude/session_query.go) | Both M for published successful query/storage corpus; U unpublished only, not replay | B; [Codex](../../codex/history_query_test.go), Claude `TestClaudeQueryAlongsideSession` + `TestClaudeMessageQueryFailures` in [suite](../../tests/stories/history_query_test.go) | R4 query saved-content fixture + metadata scope/pages; never wire log or live handle |

## Scenario index (different namespace)

`TestNativeAdapters/US-001…012` are the legacy scenario paths in the
[public suite](../../tests/stories/stories_test.go). US-013 is not in its storyIDs
loop; T/008 supplies current discovery. US-014 uses B. A cross-reference means
related acceptance, not numeric identity or a claim every scenario has a dedicated
legacy test. Config/input requirements have no separate new scenario number.

| Scenario | Common goals | Existing public acceptance |
|---|---|---|
| US-001 | SDK-US-001/002/003/004/012/013 | TestNativeAdapters/US-001 + T/001/002/003/004/012/013 |
| US-002 | SDK-US-002/004 | TestNativeAdapters/US-002 + T/002/004 |
| US-003 | SDK-US-006 | TestNativeAdapters/US-003 + T/006 |
| US-004 | SDK-US-006 | TestNativeAdapters/US-004 + T/006 |
| US-005 | SDK-US-007 | TestNativeAdapters/US-005 + T/007 |
| US-006 | SDK-US-009/010 | TestNativeAdapters/US-006 + T/009/010 |
| US-007 | SDK-US-011 | TestNativeAdapters/US-007 + T/011 |
| US-008 | SDK-US-010/011/015 | TestNativeAdapters/US-008 + T/010/011/015 |
| US-009 | SDK-US-014 | TestNativeAdapters/US-009 + T/014 |
| US-010 | SDK-US-005 | TestNativeAdapters/US-010 + T/005 |
| US-011 | SDK-US-017/018 | TestNativeAdapters/US-011 + T/017/018 |
| US-012 | SDK-US-016/017 | TestNativeAdapters/US-012 + T/016/017 |
| US-013 | SDK-US-008 | T/008, existing native discovery tests; not in legacy loop |
| US-014 | SDK-US-019 | B, independent history browser + native query checks |

## Native supplement implementation and evidence

Acceptance IDs below refer to the [supplement contract](../../harness/sdk/native-stories.md).
Tests are exact existing entrypoints, synthetic unless C1/C2/A1 named. No new
production control or history API is implied.

| Native Story | Pin / concrete API and state | Public-seam test | Real scope / gap / capture route |
|---|---|---|---|
| CODEX-US-001 | C: I original requests/dynamic-tool distinction | [TestSessionNativeApprovalAndQuestionAnswers](../../codex/session_requests_test.go); T/009/010/011 | C1 tool 41–54, C2 question 24–26 only; native approval M → R2 |
| CODEX-US-002 | C: I turn/start and settings; N dedicated steer/queue API | T/003/012/013; [TestSessionConfigurationDoesNotSelectInputSchedulingOrTarget](../../codex/session_config_test.go) | C1/C2 one start only; settings/interleaving M → R1/R3 |
| CODEX-US-003 | C: I native queries; V local backend availability, resume is side-effecting | B; [TestHistoryNativePagesWithoutResume](../../codex/history_test.go), [query tests](../../codex/history_query_test.go) | Query M/U only → R4; independent child history Phase 2 |
| CODEX-US-004 | C: I stdio/WS/WSS ownership/security | T/014/016/017/018; [TestRemoteSecurityTLSVerificationAndConfigIsolation](../../codex/remote_security_test.go), [TestRemoteCloseLeavesListenerAvailable](../../codex/remote_backpressure_test.go) | C1/C2 EOF/cleanup only; H loopback WS not public WSS certification → F/R3 |
| CLAUDE-US-001 | A: I local native streaming/control, N SDK scheduler | T/001/003/004/015; [TestSessionInputDuringExecutionKeepsNativeAcceptanceSeparate](../../claude/session_input_config_test.go) | A1 1/4/5/10 failure-only; success/interleaving M → R1/R3 |
| CLAUDE-US-002 | A: I conditional native can_use_tool/question | T/009/010/011; [question tests](../../claude/question_interaction_test.go) | A1 no permission; H private only; M → R2/F |
| CLAUDE-US-003 | A: I is_error/idle/unknown observation | T/004/005/007/017; [TestRecordedAuthenticationFailureReplay](../../claude/recorded_replay_test.go) | A1 9–11 failure R-part; successful and background M → R1/Phase 2 |
| CLAUDE-US-004 | A: I creation/dynamic subset; N effort policy | T/012/013; [TestSessionCreationThinkingIsSeparateFromDynamicControl](../../claude/session_input_config_test.go) | M → R1; budget not hard tokens |
| CLAUDE-US-005 | Q (+ A child discovery): I explicit local main/child queries and child discovery | B; `TestClaudeMessageSelection`, `TestClaudeMetadataSamples`, `TestClaudeQueryAlongsideSession` in [suite](../../tests/stories/history_query_test.go); [discovery tests](../../claude/session_discovery_test.go) | M/U only → R4; synthetic compaction/parallel chain stays companion unless genuinely observed |
| CODEX-US-005 | C: N optional filtering; [contract](../../harness/contracts/child-detail-delivery.md) | Planned F1–F5, not current executable tests | Real child M; #16 implementation, #19 gates, #21 capture |
| CLAUDE-US-006 | A: N optional filtering; explicit forwarding true preserved | Planned F1–F5, not current executable tests | Real child M; #17 implementation, #20 gates, #22 capture |
| CLAUDE-US-007 | Q: I independent bounded child content; existing Session discovery I | H1–H5 synthetic [mapping](../../claude/docs/subagent-history.md); shared no-Session/coexistence and native faults | Real child files M; #18 synthetic implementation; #22 real evidence remains separate |

## Carry-forward and delivery gates

Phase 2 retains SDK-US-006/007 real child/forwarding/parent-first gaps,
SDK-US-008 real stored-child evidence, ADR0004 optional child-detail filtering and
real independent child-history/attachment evidence gaps. Independent Claude
child queries now have synthetic coverage (#18). Existing ListSubagents works in bounded
native scope on both SDKs; it must not be mislabeled absent. Main query APIs do not
fill independent child history or full event-log replay requirements.

Run `python3 scripts/check-story-evidence.py` and
`python3 -m unittest discover -s scripts -p test_story_evidence.py -v`, then the
[offline, race, vet, generation, inventory, sanitization/hash, dependency and example gates](../testing.md).
Public suite must run with all live switches zero. Six configured hosted jobs
and exact **published** SHA external consumers remain integration gates; a local
unpublished commit cannot claim those have passed. Validation results belong in
#8 comments and the delivery report; this matrix records evidence scope, not a
permanent claim that all environments ran successfully.
