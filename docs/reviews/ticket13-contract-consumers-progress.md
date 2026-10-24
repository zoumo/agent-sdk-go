# Ticket 13 consumer migration progress

Status: in progress. This is a reviewable implementation slice, not completion
of the expand-contract contract phase.

Fixed base: `3f1776e4da94cc53d48e5029b20bd254dd727152`.
Ticket: `.scratch/session-refactor/issues/13-contract-consumers.md`.

## Implemented slice

- Codex and Claude recorder, recorded playback, offline examples, native Go
  examples, local public consumer and mock consumers use the public Session
  input/receive path. Claude initialization-only gated probe also uses Open.
- Codex Client.Load/LoadedThread and its separate history stream/attachment owner
  were removed. Existing history tests consume the public FIFO inline; retained
  payload/raw/projection/paging/mutation/cancellation/release checks remain.
  Both child-first and ancestor-first attachment admission are covered.
- Replay preserves known native turn execution options explicitly. Unknown
  execution extensions fail before submission. Native early failure and Submit
  rejection cannot become successful completion or leave Receive blocked.
- Each mock consumer keeps the 2048-fact backlog unread while Interrupt and an
  already received actual request are answered, then verifies exact FIFO drain
  after native disconnect. Claude questions use valid full-question-text keys.
- Recorder source metadata comes from clean Go VCS build evidence or an explicitly
  unverified full caller revision; missing/dirty/conflicting evidence rejects.
  Unknown modification state is null. Cleanup errors join the original run error.
- README tutorials, package comments and the breaking migration guide describe
  the new public path; original native source pins, generated roots, recording
  bytes/manifests, sanitization and historical architecture reports are unchanged.

## Outstanding ticket requirements

Do not mark ticket 13 done while these remain:

- Codex Subscription/Requests runtime state and its old parallel observation/
  approval consumers; Claude managedTree/native FIFO and callback worker pool.
- Migration and guarantee-by-guarantee audit of legacy runtime tests/helpers,
  plus Codex gated lifecycle/interaction consumers. Do not delete valid native
  mechanical guarantees to obtain compilation.
- Complete current capability/event/subagent documentation reconciliation and
  source search proving no independent old runtime/normal caller remains.
- Final two-axis review of the completed contract phase and final serial gates.

## Evidence boundaries

No real CLI, Agent, model, new recording, upstream update, push or release was
performed. The exact fetched pushed-revision external consumer is unverified:
there is no push authorization. Local public/mock checks are separate evidence.
The 20 new and 12 old stories remain distinct and pass in the offline gate.
Claude Load still fails ErrResumeUnconfirmed without replay or extra input.
Known full-Claude-race 100 ms write cases and 1 second writer-prefix timing
remain ticket 14 items; this slice does not claim full-repository race success.

## Checks so far

- Full root offline gate passed with mise Go 1.26.9 and Python 3.13 shim;
  dependency/generation/inventory/sanitization/hash checks, all Go packages,
  default live skips, local public consumer and four runnable offline examples.
- Explicit local mock consumer test passed with built native mock commands.
- Relevant race selection passed for history/load, replay, recorder provenance,
  mock consumers and CommonStories (all 20 new plus 12 old targets).
- go vet ./... and git diff --check passed. README first Go blocks and Claude
  comparison Go block compiled independently as local consumer files.
- The initial full offline/race runs overlapped briefly; a final serial run is
  required before treating the complete ticket as verified.

The local ticket remains in progress. Commit and independent review results are
recorded below when available.
