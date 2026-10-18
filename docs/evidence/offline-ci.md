# Offline CI policy and validation

The CI hardening starts at `ded7084385223dd573a1f268963e0e5a8f0a7044`.
The previous clean-checkout `git diff --check` checked no committed changes;
`RUN_REAL_CODEX_TESTS` did not control the three live harnesses; only Ubuntu
ran; inventory consistency did not exercise the generator.

## Gates and triggers

Every matrix entry (Ubuntu 24.04 and macOS 15, retaining native platform behavior)
runs Go 1.26.9 module download/verify, module-file cleanliness, verbose uncached
unit/mock/protocol tests, verbose uncached race tests, vet, offline inventory,
regeneration, and CI gate regression fixtures. Matrix fail-fast is disabled so
both platforms finish independently. Python 3.13.13 pins the generator runtime.
No Windows support claim is added. macOS temporary-directory behavior is not
normalized or masked by the workflow.

Push runs only on `main`; feature pushes use `pull_request` rather than duplicate
push+PR runs. `workflow_dispatch` permits a manual check. Concurrency is isolated
by workflow, event, and ref; new runs cancel obsolete runs on that same lane.
No path filters omit checks. PRs test the default checkout merge revision using
`pull_request`, never `pull_request_target`; no secrets or elevated token are
provided to PR code. Repository permissions are `contents: read`, and checkout
uses `persist-credentials: false` and full history.

Whitespace is checked against commit trees, independently of working-tree state:

- Push: event `before` to checked-out HEAD, covering the whole push.
- Initial push (all-zero `before`): empty tree to HEAD, checking the full tree.
- PR: merge-base of event base/head to PR head, excluding unrelated base changes.
  Tests and generation still run against the checked-out merge revision.
- Manual: first parent to HEAD, or empty tree for a root commit.

Invalid/missing comparison commits fail closed; they never silently skip the gate.
Event SHAs are parsed from the event JSON, validated, and passed as argument arrays,
not interpolated into shell code. Full history supplies comparison objects.
The policy rejects trailing whitespace, blank lines at EOF, and space before tabs.

## Generation and action maintenance

`bash scripts/check_generated.sh` runs the checked-in generator without an archive
argument, using `protocol/schema.json` at its recorded Codex commit
`d27764b82f7118f674371e6d6e76271d9d606edb`, formats both generated Go files with
the pinned Go toolchain, and requires no Git diff in schema/types/generated tests.
It downloads no upstream source. This proves local snapshot/output consistency,
not complete upstream provenance or live protocol compatibility.
`verify_inventory.py` without a source argument is likewise an offline consistency
gate, not exact-source revalidation.

Official release tag refs were read back through GitHub's API on 2026-10-18:

| Action | Release | Immutable commit |
| --- | --- | --- |
| [checkout](https://github.com/actions/checkout/releases/tag/v7.0.1) | v7.0.1 | `3d3c42e5aac5ba805825da76410c181273ba90b1` |
| [setup-go](https://github.com/actions/setup-go/releases/tag/v7.0.0) | v7.0.0 | `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` |
| [setup-python](https://github.com/actions/setup-python/releases/tag/v7.0.0) | v7.0.0 | `5fda3b95a4ea91299a34e894583c3862153e4b97` |

Weekly Dependabot GitHub Actions updates propose reviewed pin changes; they do not
automatically merge. Review official release/tag SHA correspondence and runner
requirements before accepting updates. Toolchain changes are explicit maintenance.

## Validation evidence and limits

Local results are recorded below; exact final hosted disposition is delivered with
the pushed SHA and run URL in the execution handoff. All live harnesses
are explicitly disabled: `RUN_MAC_STDIO_LIVE=0`, `RUN_MAC_INTERACTIONS_LIVE=0`,
`RUN_MAC_WEBSOCKET_LIVE=0`. Verbose tests report the actual SKIP outcomes;
a package-level `ok` means its offline harness completed, not a real-model pass.
No new live budget is authorized or consumed.

Local validation on 2026-10-18, Darwin/arm64, with inherited TMPDIR and mise Go
1.26.9 (`GOTOOLCHAIN=local`), passed:

- `go test -v -count=1 ./...`: root 23.921s, protocol 1.230s; all three live
  scenarios visibly SKIP.
- `go test -race -v -count=1 ./...`: root 135.302s, protocol 3.220s; all three
  live scenarios visibly SKIP.
- `go vet ./...`, `go mod download`, `go mod verify`, and module-file cleanliness.
- `python3 -m unittest discover -s scripts -p 'test_ci_checks.py' -v` under
  Python 3.13.13: six fixture tests, including push/PR/manual, zero-before,
  root commit, divergent PR base, unavailable/invalid base, multi-commit push,
  committed trailing whitespace, and drift in each generated Go output.
- Offline inventory: 170 client requests, 11 server requests, 85 server
  notifications, one client notification, and 23 controls.
- Pinned regeneration: 397 types from 154 definitions and 744 synthetic cases;
  gofmt of both outputs leaves no Git diff.
- `actionlint` v1.7.12: workflow accepted; `git diff --check`: clean.

The negative fixtures use isolated temporary Git repositories. Bad whitespace and
committed generated drift are expected failures of the real gates, asserted by
successful regression tests; they are never inserted into the delivery commits.
Private logs and temporary drafts remain under ignored `.scratch`.

## Hosted readback

Implementation commit `48fd6701712c10f5ec75933126cf516c1065d669` passed
[run 39092259072](https://github.com/zoumo/codex-sdk-go/actions/runs/39092259072)
on its first attempt. The API returned `completed/success` for the run and both
jobs: [Ubuntu](https://github.com/zoumo/codex-sdk-go/actions/runs/39092259072/job/117571301985)
and [macOS](https://github.com/zoumo/codex-sdk-go/actions/runs/39092259072/job/117571301788).
The jobs completed at 2026-10-18 03:06:15 UTC and 03:07:19 UTC respectively.

Both hosted logs confirm comparison from the approved baseline
`ded7084385223dd573a1f268963e0e5a8f0a7044` to the exact implementation SHA,
six successful CI fixture tests, the stated inventory counts, and regeneration of
397 types / 744 cases. All three real scenarios explicitly SKIP in both normal
and race suites on each platform (12 scenario-skip records total).
All other named gates actually ran successfully; no failed-job rerun was used.

This readback documents the implementation commit. The evidence-only follow-up
must also pass at its own exact pushed HEAD; its final SHA/run disposition is
provided in the execution handoff rather than implying an ancestor's CI covers it.
PR/zero-before/manual event selection is validated by isolated regression fixtures;
this hosted run exercises a normal push, not those other event types. No live
model or complete upstream provenance validation is claimed.
