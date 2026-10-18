# Type organization ticket03: permissions and AskUserQuestion

Date: 2026-10-18 UTC. Base main:
`949d7448bf30921e2bd2dd544c3e3ba9b8c7fef3` (ticket02; prior CI
[39086039956](https://github.com/zoumo/claude-agent-sdk-go/actions/runs/39086039956)
confirmed success through the GitHub API).

A separate clone at `/workspace/claude-agent-sdk-go` was developed directly on
main, without a branch or PR. The preinstalled codex-sdk-go was not modified.
No AGENTS.md or relevant .agents/skills existed in the clone or workspace
ancestors. The original WIP/Library package was not accessed. The implementation
is limited to the [formal JSON contract](../message-json.md); references and
license revalidation are recorded in [type-sources.md](../type-sources.md).

## Delivered scope

- Independent `types` data and root aliases: PermissionMetadata, PermissionResult,
  ControlResponse/ControlResponseData, AskUserQuestionInput, Question,
  QuestionOption and QuestionAnswer. Event.Response exposes the nested response.
- ControlRequest.PermissionMetadata decodes nested data only;
  PermissionRequest.Metadata returns an owned snapshot. IDs and original input
  in the live handle remain immutable.
- WithAnswers retains questions and unknown input while adding full-question-text
  answers. String/free text, joined multi-select strings and multi-select arrays
  are supported. Invalid keys, unions and shapes return errors.
- Raw retention, typed edit priority, explicit presence, repeated-decode reset
  and invalid final JSON checks extend ticket02's contract. Wire results remain
  distinct from the existing runtime PermissionAnswer command/fallback semantics.
- The existing answer registry, cancellation, single-use reservation, timeouts,
  failure propagation and uncertain-write handling were left intact. No new
  control send method, generator, hooks/MCP host or persistent permission API.

## Synthetic validation

All commands ran with mise Go 1.26.9 on Linux/amd64. mise itself and its toolchain
were installed under `/tmp`; no system compiler or cloud preinstalled SDK was
changed. The executable reported `go version go1.26.9 linux/amd64`.
The command environment was cleared with an isolated temporary HOME/GOCACHE,
`GOTOOLCHAIN=local`, `GOENV=off`, `GOPROXY=off`, `GOSUMDB=off`, and
`GORACE=atexit_sleep_ms=0`.

| Check | Result |
| --- | --- |
| `go test -count=1 -timeout 180s ./...` | PASS |
| `go test -race -count=1 -timeout 300s ./...` | PASS |
| `go vet ./...` | PASS |
| Existing race pressure selector, count=30 | PASS |
| Existing process-group/escalation selector, race count=10 | PASS |
| `go list -deps` independent types boundary | PASS; only non-standard package is types itself |
| Root/types compatibility and entry-point tests | PASS |
| `git diff --check` | PASS |

Exact existing pressure selectors are in
[compatibility.md](../compatibility.md#reproduce-the-synthetic-checks).
`types/permission_data_test.go` covers permission metadata ownership,
allow/deny and nested success/error response codecs, optional presence,
unknown nested fields/large integers, typed edits, malformed/contradictory
shapes, repeated/failed decode, full-text keys, free text and multi-select values.
`question_interaction_test.go` runs the exported client against a scripted child:
assistant events cannot create handles; detached metadata edits cannot change
IDs/input; the original handle sends exactly one correctly nested response;
questions and unknown input survive; cancellation rejects a prepared answer;
deny remains distinct. Existing lifecycle, timeout, duplicate answer, error and
Linux FIFO uncertain-write tests continue to pass. No synthetic result is
presented as a live CLI result.

The exact pushed SHA and its CI run are reported after push outside this commit
(to avoid a self-referential hash). No model, credential, account or Mac was
used. Runtime CLI compatibility remains **UNKNOWN**.

## Ticket04 handoff — proposals only, not executed here

The parent must create a separate Mac thread only after ticket03 is pushed.
Start from the exact pushed SHA, record the installed CLI's actual version
(the pinned source target is 2.1.302), platform and executable provenance.
Use an isolated authorized workdir, and keep raw evidence separate from mocks.
The existing [live checklist](../live-compatibility.md) still applies.

Useful local inspection commands:

```sh
claude --version
claude --help
mise exec go@1.26.9 -- go version
git rev-parse HEAD
```

The current Open implementation launches this literal argv (shown as a shell
command for diagnosis, not run in ticket03):

```sh
claude --output-format stream-json --verbose --input-format stream-json \
  --permission-prompt-tool stdio --include-partial-messages
```

Options.Model appends a single `--model=<authorized-model>` argument; Options.ResumeID
appends `--resume=<observed-session-id>`. First verify these exact flags against
the installed CLI. If rejected, record that incompatibility before changing any
probe flags. Keep stdin open for the bidirectional protocol. The first SDK
control request is `initialize` with `hooks:null`; match its actual request ID
and observe the nested response before sending a user event. A bare `echo |`
probe closes stdin too early to establish deferred permission behavior.

For the Go probe, use `claude.Open` with CLIPath/WorkDir and a PermissionHandler,
then Send/Receive. In the handler, inspect p.RequestID(), p.ToolUseID(), p.Input(),
p.Raw() and p.Metadata(). Retain the original handle; use its Context for
cancellation. Decode p.Input() as types.AskUserQuestionInput, build answers with
keys from each Question.Question, call WithAnswers, then call that same p.Answer
with types.PermissionAnswer{Behavior:types.PermissionAllow, UpdatedInput:updated}.
For denial use PermissionDeny and Message. Observe Event.Response or decode
captured wire as ControlResponse/PermissionResult; the runtime consumes matched
control responses internally rather than delivering them through Receive.
Never synthesize a handle from assistant blocks, metadata or a tool-use ID.

Required focused scenarios:

1. Harmless permission prompts: allow unchanged (nil UpdatedInput), explicit
   replacement object, empty replacement `{}`, and deny. Record exact correlation
   and continuation; a complete local write alone is not execution proof.
2. Ask for one single-choice question, one multi-select question and custom text.
   Check which assistant events and can_use_tool controls are actually emitted.
   Record full-text answer keys, retained questions, optional unknown fields and
   actual CLI acceptance. The model is not guaranteed to emit an expected tool
   call: absence is BLOCKED/NOT OBSERVED, not a passed scenario.
3. Delay while retaining the handle, then answer once. Observe cancellation from
   an interrupt/abandoned question; prepared answers must fail after cancellation.
   Duplicate/late/close races must retain their existing error semantics. Do not
   claim that cancellation retracts an already authorized write.
4. Record unsupported/auto-approved paths explicitly; an assistant tool_use alone
   cannot be answered. Avoid bypass/auto-approval settings for the prompt probe.

Ticket04 must report actual commands, installed version, SHA, redacted wire
observations and PASS/FAIL/BLOCKED for each case. No live validation is claimed by
this handoff, and no Mac task was started from ticket03.
