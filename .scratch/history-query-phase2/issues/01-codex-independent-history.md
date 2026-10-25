# 01: Codex 独立发现与历史页查询

Status: done
Blocked by: None

实现独立 ListThreads、ReadThread、ListThreadTurns、ListThreadItems 的原生合同、公开离线测试、示例与证据；不实施其他票。

## Comments

- Thread: 01a15713-e031-76d5-abd3-ebec1796c0c6 (delegation source); execution orbit 1bdb393e-e6d8-4941-8f81-3b62ef78778e~6ac68c753c58819c8ee98f7697f79e59.
- Actual base: bd557d91b98ab4db5e2b968b125fba6d583f791b.
- Implementation result commit: cb26731b8a467ce458075bf6e302ef39c9c9cccd (after initial df856b602371b5b65f49ad6f440a7b80f77f4930); final evidence-only commit follows and is recorded in the transferable manifest.
- Checks (mise Go 1.26.9): focused stdio/ws/wss history/common story `go test -race -v ./codex ./tests/stories -run 'TestIndependentHistoryQueries|TestCommonStories/TestIndependentHistoryCodex' -count=1` passed; actual peer request traces retained in verbose log. `go vet ./...`, local public consumer, five offline examples, shell syntax and git diff --check passed.
- `bash scripts/check-offline.sh`: boundaries, modules, 417 generated types/783 cases, inventory, sanitization/hashes and transcripts passed; full suite failed existing ws concurrent answer/resolution assertion (not-sent/0 frames vs outcome-unknown/1 frame). Full `go test -race -p=1 -count=1 -timeout=300s ./...` hit the same assertion, other packages passed, no DATA RACE report. Independent git-archive base reproduced with count=5 from /tmp/history-query-baseline. Assertions unchanged.
- Required code-review: Standards initially 1 test dependency-check issue, fixed; Spec initially 1 native typed-live evidence gap, fixed. Independent rechecks: Standards 0 remaining, Spec 0 remaining. No scope creep or production logic defect reported.
- Remaining limits: full-suite green blocked by independently reproduced base concurrency failure; macOS/live checks and new precise fetched-module consumer not executed. No push, PR, external issue, model, new recording, Mac or private session access. Claude/common completion not claimed. No automatic approval denial.
- Status done describes ticket implementation and review; it does not mark full-suite/release acceptance green. See docs/reviews/2026-10-25-codex-history-query.md for exact evidence.

## Acceptance criteria

- Preserve ReadThread/ListThreadTurns/ListThreadItems; add typed thread/list without Open or old Load gates.
- Preserve filters/archive/source, omitted/null/value, metadata/summary/full, unknown fields/variants including upstream readState.
- Preserve native order, page size, opaque next/backwards cursors; explicit end, reverse turn re-inclusion, exclusive item anchor requiring nonempty turnId; no sorting/dedup/fetch-all/snapshot promises.
- Retain default scoped lists, local/hosted differences, legacy/DB unsupported cases, native errors/cancel; no implicit resume/event replay.
- Document app-server initialization/process startup, metadata repair, useStateDbOnly, server-side remote storage/paths.
- Public stdio/ws/wss synthetic peer tests: no Session, coexistence, pages/empty, cursor/anchor errors/native rejection/cancel/unknown/detached; barrier and trace prove no extra start/resume/subscribe/input/answer or Receive projection, live still delivered.
- Update this slice's harness/interfaces/stories/index, offline example and pinned-source/validation evidence; do not mark Claude/common capability complete.
