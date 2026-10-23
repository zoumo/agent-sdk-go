---
type: Development Standard
title: SDK 测试规则
status: stable
---

# Testing standards

Apply these rules to Go tests and generated Go tests. Commands and validation
scope are maintained in the [testing guide](../docs/testing.md).

- Use `github.com/stretchr/testify/assert` and `require` for assertions. Use
  `require` when later steps depend on the check; use `assert` for independent
  checks that can continue and report additional failures.
- Prefer `testify/suite` for shared setup/teardown, client lifecycle and related
  stateful scenarios. Use named table-driven cases for single-function checks;
  suite methods may contain table-driven subtests. Avoid wrapping a second
  lifecycle/assertion framework inside suite.
- Suite tests run sequentially: do not call `t.Parallel` inside them. Preserve
  concurrent, synctest and timing-sensitive coverage with native `testing`
  tables/subtests where needed. Keep internal worker concurrency and barriers.
- Never call `require`/`FailNow` from a worker goroutine. Collect worker and
  callback outcomes through channels, synchronization or a bounded collector,
  then assert on the owning test goroutine. Preserve cleanup and cancellation.
- Preserve all cases, expected values, error classifications, unknown-field
  checks, backpressure, cancellation and race assertions. Do not lower coverage,
  loosen deadlines or substitute mocks for recorded protocol replay to make a
  migration pass.
- Keep Go `Example` output contracts, `TestMain`/subprocess dispatch, testing
  entry points, helpers, cleanup, environment management and explicit live skips
  native where those APIs are required. Helpers should serve a concrete protocol
  or synchronization need; suite does not implement a fake agent or replay peer.
- Change the generator for generated tests, then regenerate and verify. Preserve
  every schema case and its native type/data; do not hand-edit only the output.
- Keep explicitly enabled real CLI/agent checks under each native `tests/e2e`.
  Retain opt-in gates and default skips. Document initialization/transport-only
  probes accurately; the directory name does not prove full model/task coverage.
- Keep capability, source inspection, offline/synthetic tests and real recording
  evidence separate. A skipped live check is not a live pass. Default tests and
  CI do not run real agents or collect recordings.

Shared user stories run directly in `CommonStoriesSuite`; private observations
and acceptance checks live in `tests/stories/acceptance_test.go`. Protocol peers
remain in test files, and reusable recording/replay helpers follow the
[tools layout](code-layout.md). There is no separate `internal/testkit` framework.

Use mise's pinned compiler and retain offline, race, vet, generation, inventory,
fixture sanitization/hash, dependency-boundary and independent-consumer gates on
Linux/macOS. External consumer modules must fetch the new repository's precise
pushed revision outside the checkout, without `replace` or `go.work`.

通用验收、资源隔离和结果声明遵循[测试与验收原则](principles/testing.md)。
