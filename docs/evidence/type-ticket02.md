# Type organization ticket02: message/content/result codecs

Date: 2026-10-18 UTC. Base: main at
`5b5e9bd7454ce724e527f217e7f04c8169c98473` (ticket01).

The independently cloned Claude repository was changed directly on main. The
preinstalled codex-sdk-go repository and unavailable original WIP were not used
or modified. No AGENTS.md or .agents/skills instructions existed in the cloned
repository or workspace ancestor. Implementation scope is the
[message JSON contract](../message-json.md); [source/license review](../type-sources.md)
records the pinned references. All fixtures for this ticket are original and
synthetic.

## Validation

Executed on Linux/amd64 using mise Go 1.26.9 (`go version go1.26.9 linux/amd64`).
Go commands used a cleared environment, isolated temporary HOME, GOCACHE,
`GOTOOLCHAIN=local`, `GOENV=off`, `GOPROXY=off`, `GOSUMDB=off`, and
`GORACE=atexit_sleep_ms=0`.

| Check | Result |
| --- | --- |
| `go test -count=1 -timeout 180s ./...` | PASS, runtime and types |
| `go test -race -count=1 -timeout 300s ./...` | PASS, runtime and types |
| `go vet ./...` | PASS |
| Existing race pressure selector, count=30 | PASS |
| Existing process-group/escalation selector, race count=10 | PASS |
| `go list -deps` independent types boundary | PASS, only non-standard dependency is types itself |
| `git diff --check` | PASS |

The exact pressure selectors and reproduce commands are in
[compatibility.md](../compatibility.md#reproduce-the-synthetic-checks).
New `types/json_test.go` tests cover nested wire layout, constructed frames,
unknown message/content/system subtype and field round trips, explicit presence,
zero/null/false, repeated decoding, invalid unions, nested mutation over stale
Raw, exact unknown integer retention, envelope/view precedence, all documented
result subtypes, success+is_error, and camelCase modelUsage.
Root/type entry-point compatibility is checked by `TestMessageCodecAliases`.
Existing full runtime tests retain event ordering, permission single-use,
cancellation, transport error and teardown coverage.

No real Agent/CLI, Mac, account, credential, or paid service test was executed.
Live interoperability remains UNKNOWN. CI checks only synthetic behavior; its
run URL and exact committed SHA are reported after push, outside the commit to
avoid a self-referential evidence hash.

## Ticket03 handoff

Canonical definitions/codecs remain in `types`; root names are aliases. Add new
permission/question data schemas there while leaving answer authority,
single-use state, cancellation, registries and process control in the runtime.
Do not move outer message fields into `Message`. Extend the existing raw/typed
and presence contract when adding modeled JSON fields, and keep unknown keys.
This ticket introduces no typed permission question, persistent permission
update, generator, hosted MCP, hook callback or new control method. No Mac/live
work for ticket04 was performed.
