# Native subscription delivery evidence

Baseline: clean main `885bf4de79c9bde75c342c807a9e3e45f098187c`. Target CLI 0.162.1, official source `092d3acd6bec3e3a14bdc7e7a2810ab628ab759d`.

The [scoped spec](../design/native-subscription-spec.md) and one focused local ticket (`.scratch/native-subscription/issues/01-native-subscription.md`) were written in this independent session using the personal to-spec/to-tickets formats. The user's requested single slice and established offline test seam resolved their review questions. No branch or worktree was created.

Subscription.Events now returns types.Event directly; SubscriptionOptions and convenience data live in types. Public Tree wrappers/kinds/aliases and native Origin/Scope/Sequence/Epoch metadata are absent. Relationship discovery, association/creation evidence, pending forwarding and bounded batching remain private. Generated native structs/JSON, original request authority and sanitized recordings are unchanged. The synthetic runnable example is renamed to observe-subagents-offline; CI and current documentation follow the new path. Historical evidence hashes retain their original keys; test_files references follow the relocated current files.

## Completed offline validation

- Go 1.26.9: full uncached unit/mock suite passed (root package 28.978s), including the direct event-shape test across stdio/WS/WSS and all existing V2 creation/negative, nested, history, bounds, request and lifecycle cases.
- Go vet, module verification and changed whitespace passed.
- Reproducible generation passed: 399 types, 154 pinned definitions, 750 synthetic round-trip cases; native schema/generated files unchanged.
- Public consumer, synthetic subagent observation and both real sanitized transcript replays passed offline. S1 retains 66 frames, S2 28 frames; recordings and request correlation checks passed.
- Protocol inventory passed: 173 client requests, 11 server requests, 86 server notifications, 1 client notification and 24 controls.
- Six CI gate edge-case tests and seven sanitizer/publication tests passed using installed Python 3.13. The system Python lacked tomllib; using the compatible interpreter resolved that environment-only failure.
- Full uncached race run passed across every package (root 193.730s). Hosted CI and the exact delivery SHA are verified after publication and reported in the final handoff.

The optional re-verification of the entire upstream compressed schema archive was not run to completion because the local Python lacks zstandard. It is separate from the required offline inventory/generation gates. The seven actual official implementation files used by the comparison were independently fetched at the requested pin without credentials and matched local bytes; hashes follow. TS/Rust fragments are source-verified, not compiler-verified. The complete Go usage package is compiled by the full suite and never executed against a model.

No live CLI/model scenario was run, no credentials were changed, and no authentication/security configuration was modified. All four live harness switches were explicitly disabled for final Go checks. Native child auto-attach remains best-effort and live subagent coverage remains unverified.

## Official comparison sources

- `sdk/typescript/src/codex.ts`: `25a5b9ccb6a485f3aaa474d6e39f3d48ad72903d9e3a2f02e7b6218228d6a470`
- `sdk/typescript/src/codexOptions.ts`: `702158cdee21c53ef4ad3905a8d7c94d2459925242acf300bbe9ff74c3c83c2f`
- `sdk/typescript/src/thread.ts`: `21e5c7dc1c3047564750ecea5dfab8a47ab732488c5023e4e22adb01e874483f`
- `sdk/typescript/src/exec.ts`: `0df2fe1ccd90e05b2f2fb0cb3e1bd3293e23f36e3bbb369258edc6a63ae8655c`
- `codex-rs/app-server-client/src/lib.rs`: `a7ae45d19c725a4087121478de07b58451bd4a2d7b938a782c9b04cec1060bf5`
- `codex-rs/app-server-client/src/remote.rs`: `c1b89c748ebc812836d68882dbd7848b72fe5e4cc7c226cc2cb7d3846515b5e5`
- `codex-rs/app-server-protocol/src/protocol/common.rs`: `e25ebb90bf79be79def79ebf2dd704057577fa9806710fbea0ee240a909d0dc1`
