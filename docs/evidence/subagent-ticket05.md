# Subagent ticket05 delivery evidence

Date: 2026-10-18 UTC. Implementation reviewed from
`11d48dab8650647c42e70877cd1b49a4e3649ad6` through ticket04
`5755d278f00510b77b6e211500b8b23f8fd66727` on main. This delivery adds public compatibility documentation, an original executable
offline example with its contract test, and a narrow runtime correction for
unknown system subtype attribution/deduplication found during review. The exact ticket05
commit is the containing Git commit (`git log -1 --format=%H --
examples/subagents-offline/main.go`); CI records its full head SHA.

## Five-ticket acceptance scope

| Ticket | Commit / delivery | Accepted behavior and evidence |
| --- | --- | --- |
| 01 | `a062002c825e31535b3d0704b02e9fe9eec4c635` | Initialize-time opt-in/adapters, owned single stream, typed envelopes and lossless native forwarding; E2 handshake/single-layer tests |
| 02 | `c74a59d57241b30d9c45745aeddec7c9139e0bc3` | Recursive lineage, evidence-based alias enrichment, runs, bounded dedup and explicit conflicts; E2 lineage tests |
| 03 | `29e235c1d1a9aa2cb383497bb16e8a7c8f71b7bf` | Per-run patch/notification terminals, background/result/idle independence and explicit history gaps; E2 terminal tests |
| 04 | `5755d278f00510b77b6e211500b8b23f8fd66727` | Bounded unresolved buffering/atomic fanout, original permission/question authority and usage isolation; E2 bounds tests |
| 05 | This commit | Correct unknown-system attribution/dedup contract; per-capability native/SDK/version/evidence matrix, foreground/background/nested/legacy limits, executable offline fixture and corrected public compatibility inventory |

This accepts the five implementation tickets **within their documented E1/E2
scope**, not all original user stories without qualification. **The user's
request for proven first entity creation remains unresolved. Created is
conditional and this version never emits it.** task_started and SubagentStart
can each describe resumed work; neither alone proves creation. Associated is
an observed relation, not Created under another name. No additional native
criterion excluding resume was established, and no fake native capability was
introduced. Further creation research requires separate evidence.

## Source and license recheck (E1)

Fixed official bytes were downloaded again for review; the hashes match the
existing provenance. No downloaded package code was executed or redistributed.

| Source | Version / SHA256 |
| --- | --- |
| Python `_internal/query.py` | `f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab` / `0562517de949f4431177f204fe84cda3265f495d0eb0a949b9ced96cc5bd8276` |
| TS npm archive | 0.3.302 / `c7d7d61d6b0b7b7bb05cc053f229c5df88f151550ead0624c7393939d0d8e1f1` |
| TS `sdk.d.ts` | 0.3.302 / `26f9a0c91ad64e2020f0d3734932302e3cc09685bddf2b2f9ef60839ce278773` |
| TS `LICENSE.md` | `8ce94b9478bb9868f9641f818e06cd722fbe55d4c22e2d2ed11971b20146173a` |

Python initialize and task lifecycle handling were read; TS forwarding,
assistant/user identity, run ordering, parent task, REPLACE snapshot and native
session-state declarations were checked. CLI **2.1.302** is the Python source
target, not a runtime-validated version. No minimum supported CLI is established.
The [official Hooks reference](https://code.claude.com/docs/en/hooks#subagentstart)
was re-read on 2026-10-18 and still includes resume and teammate messages.
The [Python SystemMessage reference](https://code.claude.com/docs/en/agent-sdk/python#systemmessage)
was re-read and still requires `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` and
receiving beyond result. These mutable pages have no byte-hash claim.

Python is MIT; no source was copied/adapted. TS package licensing is **non-MIT**,
all rights reserved subject to linked legal agreements; declarations are schema
reference only. No implementation, declaration bundle or third-party fixtures
were added. The example/fixtures are original Apache-2.0 material. The ACP RFD
remains draft design context only, with no runtime dependency or conformance
claim. See [source/license provenance](../type-sources.md).

## Review findings

### Standards

No hard documented-standard breaches or material smell findings. The independent
Standards review found stale public wording that all examples only compile;
README and the canonical inventory now distinguish real-CLI examples from
the executed synthetic example.

### Spec

One related defect group was found and corrected. Unknown system subtypes with
no source were incorrectly treated as root (constraint 5 requires unscoped),
and were accepted into UUID dedup without a proven subtype contract (constraint 7).
The fixed TS 0.3.302 known-subtype boundary now governs both decisions. Explicit
source evidence still routes an unknown subtype normally; Native.Raw is unchanged.
`TestTreeUnknownSystemSubtypeNeedsSourceEvidence` checks unknown subtype and
missing subtype, repeated unknown UUID frames, known init scope and known-child
attribution. The independent Spec reviewer rechecked the narrow final diff and confirmed
both corrections with no remaining spec blocker in this supported subset.
No other material spec defects were found. Created remains the
explicit, unresolved source-evidence gap described above.

The public README now links the detailed matrix and explicitly preserves the
creation gap and no-live boundary. The canonical inventory's stale
`forward_subagent_text = Unsupported` and initialize limitation were corrected
to the implemented optional forwarding handshake, without claiming native
runtime support. SDK-defined agents/hooks and historical queries remain unsupported.

## Executed offline validation (E2)

Platform: macOS darwin/arm64, Go **1.26.9** via mise. Commands run on the above
runtime baseline plus this commit's narrow runtime correction, tests and documentation. All tests use
original synthetic subprocess peers, no real Claude executable/model/account.
`TestOfflineExample` actually runs its example logic and self-reexecuted peer;
it is not merely compiled.

Environment for the full checks (GO_BIN resolves the existing mise installation):

```sh
GO_BIN="$(mise where go@1.26.9)/bin/go"
env -i PATH="$(dirname "$GO_BIN"):/usr/bin:/bin:/usr/sbin:/sbin" \
  HOME=/tmp/subagent-ticket05-home GOCACHE=/tmp/subagent-ticket05-cache \
  GOTOOLCHAIN=local GOENV=off GOPROXY=off GOSUMDB=off CLAUDE_GO_LIVE=0 \
  GORACE=atexit_sleep_ms=0 "$GO_BIN" <arguments below>
```

| Arguments / check | Result |
| --- | --- |
| `version` | `go version go1.26.9 darwin/arm64`, exit 0 |
| `test -v -count=1 -run '^TestLiveCLIInitialization$' ./...` | PASS with explicit `LIVE SKIPPED: CLAUDE_GO_LIVE=0`; no provider/model calls |
| `test -count=1 -timeout 180s ./...` | PASS, exit 0; root 11.224s, example 0.423s, types 0.826s |
| `test -race -count=1 -timeout 300s ./...` | PASS, exit 0; root 13.159s, example 1.596s, types 1.205s; no race report |
| `vet ./...` | PASS, exit 0, no diagnostics |
| `list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./types` | PASS, exit 0; only nonstandard dependency is the types package itself |
| `run ./examples/subagents-offline` | PASS, exit 0; 21 wrapper records, 11 Native, 0 Created; final closed record |
| `git diff --check` | PASS, no diagnostics |
| Committed `bash scripts/check-changes.sh BASE HEAD` | Run immediately after commit; result returned with remote SHA and CI in delivery report |
| Relative documentation links and named test references | Checked; all resolve |

The embedded fixture SHA256 is
`15ee170a0bf5ceda9056f4c7f2c8ef2cd4e0e77c713d2567a27c7e57babebeca`.
It contains 11 original native frames. Its test checks their exact original bytes
via the output's native_raw string, association before attributed child frames,
known nested parent, explicit native run ID, child output after root result,
patch-only killed terminal, no Created, and explicit Close/closed/EOF.
The scripted empty background snapshot does not imply a run terminal.

**E3: not run. E4: not run.** Past partial live checks in unrelated type tickets
are not evidence for these subagent features. This round made no real CLI/model
calls, authentication changes or budget/account probes. Local tool output stays
outside Git; this document records reproducible commands and sanitized outcomes.
CI independently executes the committed Linux/macOS offline matrix and explicit
live gate; its run URL and full head SHA are returned in the delivery report.

## Remaining operational boundaries

Creation remains conditional; forwarding remains runtime-unverified. Only
native-emitted and attributable descendants can be observed; legacy tool-only
identities and missing run IDs retain their explicit limits. Native session-state
emission is opt-in, background snapshots REPLACE, and root result / child terminal /
native idle / pending action are separate dimensions. Permission and question
answers require their sole original epoch-bound handle. Usage stays source-local
and unknown in scope; no aggregate is calculated. Close owns the process;
disconnect/resume has a permanent observation gap with no cursor/replay or
cross-epoch exactly-once. See the [complete matrix](../subagent-compatibility.md).
