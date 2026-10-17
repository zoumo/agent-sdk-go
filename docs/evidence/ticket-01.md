# Ticket 01: owned local thread

Verification date: 2026-10-17.

Tested SDK implementation commit: `b4870601497ccadce9e11563c8512f2b16f9c697`.

Normal, race, vet, and inventory commands were rerun successfully after that
implementation commit. This following evidence-only change records its exact
identifier without changing SDK source or tests. This is a mock-tested SDK slice,
not a live server or supported-version-range certification.

## Source and tooling

- Module: `github.com/zoumo/codex-sdk-go`
- Toolchain: mise-managed Go 1.26.9, `GOTOOLCHAIN=local`
- Dependencies: Go standard library only
- Official source: `d27764b82f7118f674371e6d6e76271d9d606edb`
- Verified official workspace label: `0.160.1`
- Full experimental export blob: `0a35c5522fa525a5c2d6a2138c244c9d5b88815d`
- [Source checksums and generation provenance](../protocol/README.md)
- [Canonical complete support inventory](../compatibility.md)

## Executed checks

All commands are run from the repository root with the pinned mise environment.

```sh
mise exec go@1.26.9 -- go version
mise exec go@1.26.9 -- go test ./...
mise exec go@1.26.9 -- go test -race ./...
mise exec go@1.26.9 -- go vet ./...
python3 docs/protocol/verify_inventory.py
git diff --check
```

The final normal, race, vet, inventory, and whitespace checks passed. Repeated
normal runs (`go test -count=5 ./...` through mise) also passed. Protocol generation
was reproduced byte-for-byte from the verified official archive. The inventory
verifier was additionally run against the exact downloaded official source bundle,
checking all recorded hashes, source registries, and schema memberships.

An earlier race-instrumented run exposed a timing assumption in the cancellation
test: its deadline could expire before request dispatch. The test now waits for
the fake peer to acknowledge an actual request byte before cancellation. Both
not-sent and outcome-unknown remain valid states at their respective boundaries;
the test no longer assumes that elapsed time proves delivery.

## Exported-client contract coverage

The external `codex_test` package imports only public SDK/protocol APIs. Its owned
peer is the test executable in an explicitly selected helper mode. No real Codex
binary or model is involved.

- Exact initialize → initialized → thread/start → thread/read order
- Native thread/session IDs and integers beyond float64's exact range
- String/integer RPC identity distinction, unmatched and late responses
- Missing/invalid/null/fractional/duplicate response IDs and malformed envelopes
- Immutable ready-server information and typed recursive result decoding
- Raw unknown JSON, explicit null, omission, empty collections, false, and zero
- Raw extension calls; definitive RPC errors preserved without replay
- Pre-write cancellation, confirmed-dispatch cancellation, connection loss
- Pending-call/outbound-queue admission limits and outgoing/incoming frame limits
- Child early exit, failed initialization, unterminated frames, retained output pipes
- Blocked writes, write timeout, cancellation releasing a blocked writer
- Independent stderr drain, bounded fully redacted diagnostics
- Concurrent/idempotent close, bounded child termination, owned-child reaping
- Exact argv, explicit environment replacement, and working-directory propagation
- Unsupported reverse requests fail explicitly; unknown notifications are counted

Model tests cover 398 generated synthetic round trips across 81 official native
definitions and 218 generated Go types. Targeted tests cover unknown union variants,
malformed known variants, duplicate members, invalid UTF-8, unpaired surrogates,
atomic decode behavior, and full experimental thread-start fields.

## Scope left to subsequent tickets

Notifications are counted and discarded; there are no subscriptions yet. Reverse
requests fail the connection; there are no answer handles. Turn start/interrupt,
resume/unsubscribe wrappers, and WebSocket transport are not implemented. Raw
`Call` is not certification for those workflows. The canonical inventory marks
each official capability separately.

No live validation was performed. Official Rust, TypeScript, and Python test
sources were inspected for patterns, but their tests were not run and their code
was not copied. [Testing-pattern notes](../protocol/testing-patterns.md) distinguish
TypeScript's exec transport from app-server harnesses.
