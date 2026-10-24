# 共享设计的来源与实施记录

规范集中在 [harness](../../harness/index.md)。本文件保留设计文档迁移时分离的历史状态、来源与验证说明，不作为通用行为合同。

## docs/glossary.md

规范：[harness/glossary.md](../../harness/glossary.md)。

These terms describe the shared stories and acceptance criteria. They introduce
no shared public protocol or client API. Native event names and evidence stay
in [Codex event sequences](../../codex/docs/event-sequences.md) and
[Claude event sequences](../../claude/docs/event-sequences.md).

## docs/glossary.md

规范：[harness/glossary.md](../../harness/glossary.md)。

Capability status (supported, conditional, unsupported) is separate from evidence
status (source inspection, offline contracts, real observations). The
[native story evidence](stories/index.md) retains both. Historical sanitized recordings
do not prove current live compatibility; current successful native subagent
interoperability remains unverified.

## docs/lineage.md

规范：[harness/contracts/lineage.md](../../harness/contracts/lineage.md)。

`internal/lineage/graph_test.go` covers unknown parents, ancestors, rejection
atomicity, late ancestors, literal zero limits, independent observers and identity
contraction. Native regressions in `codex/lineage_test.go` and
`claude/lineage_shared_test.go` exercise the actual adapter paths; the original
native nested, conflict, creation, deduplication, overflow and answer tests remain
required. These are offline synthetic tests. Current real-agent child forwarding
and interoperability remain unverified.

## docs/event-delivery.md

规范：[harness/contracts/event-delivery.md](../../harness/contracts/event-delivery.md)。

[Offline validation](../testing.md) includes concurrent FIFO/race tests, native
observer tests and external consumers using built mock CLIs against real SDKs.
Synthetic scenarios are distinct from real recording evidence.

## docs/specs/async-history.md

规范：[harness/contracts/async-history.md](../../harness/contracts/async-history.md)。

Status: implementation spec, 2026-10-21. Resume-and-replay revision baseline: `7878e36`.
Event-consumption contract revised 2026-10-22; see [history events](../../harness/contracts/history-events.md).

## docs/specs/async-history.md

规范：[harness/contracts/async-history.md](../../harness/contracts/async-history.md)。

Each slice starts red, then gains its implementation. New acceptance verifies native resume before first replay,
no-prompt/no-new-session traces, input gating, same-ID continued Send/StartTurn,
ordinary resume without replay, startup/resume mismatch and cleanup failure. Run pinned mise Go tests
regularly, full offline/race/vet checks at completion, independent Standards and
Spec reviews against this baseline, fix findings, then commit only task changes.

Real validation is separate: use owned temporary cwd/data and externally supplied
authentication, never personal history or copied credentials. Report capability,
native read-only integration, and real model-created histories separately. A
missing prerequisite is unverified, not a pass. Do not push. After SDK validation,
update MASS harness contracts only, preserving its current branch and unrelated
changes; no MASS implementation or dependency upgrade.

## Sources

- [Codex app-server](https://developers.openai.com/codex/app-server/): pagination,
  explicit ordering and full items; current documentation is newer than the pin.
- [Pinned Codex schema](https://github.com/openai/codex/blob/092d3acd6bec3e3a14bdc7e7a2810ab628ab759d/codex-rs/app-server-protocol/schema/precomputed/app-server-exports-experimental.json.zst).
- [Claude session API](https://code.claude.com/docs/en/agent-sdk/python#get_session_messages).
- [Pinned Python session reader](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/sessions.py):
  format and branch-selection reference only; Go implementation is original.

## docs/specs/history-events.md

规范：[harness/contracts/history-events.md](../../harness/contracts/history-events.md)。

Status: implemented and independently reviewed, 2026-10-22. Local file tickets are the configured tracker. See [validation evidence](../specs/history-events-validation.md).
Supersedes the separate public snapshot-consumption contract in async-history.

## Problem Statement

Consumers currently need a separate Load record parser even when they already
handle native real-time events. Excluding deltas does not remove this mismatch:
Codex returns complete turns and Claude returns transcript entries.

## Solution

Load

## docs/specs/history-events.md

规范：[harness/contracts/history-events.md](../../harness/contracts/history-events.md)。

## Further Notes

Proposed local ticket frontier: Codex replay events; Claude replay events; migrate
consumers/documentation and run acceptance. The final integration ticket depends
on both provider slices. Native fidelity and explicit unknown information take
precedence over making replay look like an exact live recording.
