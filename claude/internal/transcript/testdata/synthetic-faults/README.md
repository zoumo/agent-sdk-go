# Synthetic protocol cases

Provenance: synthetic. These are handwritten tests, never recorded model output.

`controls.jsonl` exercises two in-flight requests, an intervening notification, out-of-order responses, a reverse permission request/answer, and cancellation. `replay_test.go` also checks mismatched/missing IDs, duplicate answers/IDs, early EOF and extra frames. Capture frame/output limits are checked with bounded readers, without a CLI.

Existing root suites remain the synthetic source for parent-result followed by child output, unknown events, missing creation evidence, cancellation races, EOF, and resource limits. See `tree_terminal_test.go`, `tree_lineage_test.go`, `tree_bounds_test.go`, `permission_cancellation_test.go`, and `overload_test.go`. This directory does not claim these events were observed in S1.
