# Architecture standards

These rules describe the agreed repository boundaries. For their context and
navigation, see the [architecture guide](../architecture.md).

- Use one module, `github.com/zoumo/agent-sdk-go`, and one version. Keep a single
  root `go.mod`/`go.sum`; do not introduce nested modules or `go.work`.
- Keep the supported native SDK imports `codex`, `codex/types`, `claude` and
  `claude/types` independent. Do not add a root generic client, old-path forwarding
  packages, a common public question interface or a shared answer state machine.
- `codex` and `claude` must not import each other. Native SDK production code
  must not depend on recording/replay tools or fixture containers.
- Both native `types` packages must have standard-library-only **production**
  dependencies. Their tests may use testify. Dependency gates must distinguish
  production dependencies from test dependencies, and remain enabled.
- Shared runtime code must not import either native SDK or native `types`.
  `internal/lineage` accepts confirmed relationship facts; it does not parse
  native events, infer creation, send protocol messages or grant answer authority.
  Its precise contract and resource bounds live in [lineage](../lineage.md).
- `internal/queue` shares only concurrent FIFO mechanics. It imports no native
  SDK, native types, tools or fixtures. Its only non-standard production dependency
  is the pinned `github.com/zoumo/golib/channx.ChannX` implementation; other golib
  packages must not enter SDK production dependency trees. Native types remain
  standard-library-only. Each Codex subscription and Claude client owns one
  ChannX; there is no process-wide queue, per-agent partition or second growing
  derived-event FIFO. See [event delivery](../event-delivery.md).
- Each observer owns its graph. Native parsing, unknown data, identity aliases,
  run state, deduplication, event caches, transport and process lifecycle stay
  with the native SDK. Preserve explicit observation gaps and native subagent
  association behavior.
- Answers require their original native request/permission handle. Relationship
  records carry context only. Do not restore `TreeRecord`/`TreeEvent` APIs.

The [testing standards](testing.md) govern shared story acceptance. The
[layout standards](code-layout.md) distinguish SDK code from reusable tools,
command entry points and test data. Python validation scripts keep their native
generation, inventory, provenance and sanitization responsibilities.
