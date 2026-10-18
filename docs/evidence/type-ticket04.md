# Ticket04: bounded Mac stream-json comparison

Date: 2026-10-18 UTC. Tested base:
`83b2cca42f94c9b9d2f161cdd7034f1224c97887`.
The local main was clean and safely fast-forwarded from `8c89959` to that
already-pushed revision. No reset, force push, branch, credential inspection,
CLI upgrade, global permission change, or other repository/process manipulation
was performed. No applicable AGENTS.md or .agents/skills was present in the
repository or its ancestors. The only mise configuration was Go 1.26.9; it was
trusted under the existing authorization.

## Actual environment and limits

- macOS / darwin arm64, Go 1.26.9 through mise.
- Installed native Claude Code **2.1.258**, executable SHA256
  `0d036f0b635d239844e6df6996bc05263fe305df505b073c71f79f83668ea35a`.
  This differs from the source target **2.1.302**, which was **NOT TESTED**.
- Normal existing authentication/configuration was inherited without reading,
  copying or printing credentials. Authentication method was not independently
  inspected. The CLI selected `theta/GLM-5.3[1m]`; this is evidence for this
  configured runtime combination, not official Claude model validation.
- Synthetic workdir outside either repository, owner-only permissions. Only
  AskUserQuestion and Write were exposed to model-backed runs. Write approval
  required an exact expected file path and exact synthetic content. All other
  requests were denied. No business files or user data were supplied.
- Eight user inputs total, one attempt per scenario, no automatic resend or
  model-level replay. `--max-turns 1` prevented a second inference per input;
  eight distinct assistant message IDs were observed. Tool results report
  `num_turns:2` because the CLI reaches the next loop boundary, not because a
  second distinct model message was observed. Actual provider HTTP requests or
  internal retries were not instrumented.
- Per process: `--max-budget-usd 0.25`; user/model run timeout 60s (direct probe
  45s), SDK initialization 30s and default bounded shutdown. No timeout occurred.
  No additional model runs were made after the eight-input budget was exhausted.

## Exact launch and comparison method

A temporary Go harness first launched the installed executable directly with
the SDK's literal arguments, kept stdin open, sent initialize with `hooks:null`,
and verified a successful nested response with the original request ID. This
startup-only check sent no user prompt and used `CLAUDE_CODE_SAFE_MODE=1`.

```sh
claude --output-format stream-json --verbose --input-format stream-json \
  --permission-prompt-tool stdio --include-partial-messages
```

All eight model-backed runs added these process-local restrictions, accepted by
the actual CLI (no persistent settings edits):

```sh
--safe-mode --strict-mcp-config --mcp-config '{"mcpServers":{}}' \
  --permission-mode manual --max-turns 1 --max-budget-usd 0.25 \
  --tools AskUserQuestion,Write
```

Input 1 used direct bidirectional pipes. Inputs 2–8 used exported
`claude.Open`, `Send`, `Receive`, `SessionID` and `Interrupt`, with a local
launcher recording the actual stdin/stdout byte streams before adding exactly
the restrictions above. Resume appended the single literal
`--resume=<observed-native-id>` argument. The SDK was not patched.

For each SDK event, the harness reparsed Event.Raw through types.ParseEvent.
An offline audit additionally parsed every captured stdout frame, marshaled it,
and compared decoded JSON values with the original frame. All **655 frames**
parsed and round-tripped with **zero semantic differences**, including consumed
initialize/interrupt responses. This is semantic equality, not byte equality.
The independent types dependency check remained standard-library-only.

The temporary executed harness SHA256 was
`4f5eb519317b52b4d0714f5ddc7584bc962a85272d11aa0d2b6a907a1d6c05d7`.
It and unredacted local captures were retained locally in an owner-only temporary
directory, outside git; they contain local paths, session and tool identifiers
and are deliberately not published. This report publishes selected projections,
not those raw logs. There is no checked-in opt-in runner or live CI job.

## Observed scenarios

PASS below applies to the named observable behavior only. Tool scenarios
finished with `error_max_turns` and `is_error:true` after executing/denying the
tool, as deliberately bounded. They are **not successful completed dialogue
turns**. No attempt was repeated to obtain a cleaner outcome.

| Input | Scenario and observation | Status | CLI reported USD |
| --- | --- | --- | ---: |
| 1 | Direct CLI prompt, no tools: `DIRECT_OK`, success, is_error:false | PASS | 0.011310 |
| 2 | SDK prompt remembers synthetic code, returns `SDK_OK`, success | PASS | 0.003158 |
| 3 | Fresh SDK process resumes observed ID, no replay; returns previous `ORBIT_47`, requested/observed IDs match | PASS | 0.002415 |
| 4 | One AskUserQuestion call containing single choice, multi-select, free text; real can_use_tool handle, delayed answer, accepted user tool_result | PASS for answers; dialogue bounded | 0.010228 |
| 5 | Write allow with nil UpdatedInput: exact `SYNTHETIC_ONLY` file exists | PASS for execution; dialogue bounded | 0.003608 |
| 6 | Write explicit replacement input: exact `SYNTHETIC_UPDATED` file exists | PASS for replacement; dialogue bounded | 0.005733 |
| 7 | Write deny: file absent, result permission_denials includes original tool | PASS for denial; dialogue bounded | 0.004833 |
| 8 | AskUserQuestion pending; matched interrupt success, control_cancel_request, cancelled handle context, late answer rejected, no permission answer bytes written | PASS for cancellation; dialogue bounded | 0.005268 |

Reported total: **$0.046553**. The normal/resume values are not cumulative:
each was a fresh process and resume's reported cost was lower than normal's.
This is CLI accounting, not an independently verified bill. Eight result frames
and eight distinct model message IDs were observed. Frame counts by capture:
startup 3, direct 13, normal 62, resume 41, questions 140, allow 48,
replacement 80, deny 74, cancel 94.

### Question and correlation evidence (sanitized projections)

The actual flow was **p.Metadata() → json.Unmarshal(metadata.Input,
AskUserQuestionInput) → WithAnswers() → the same p.Answer()**. All answers
used full question text keys; questions were preserved. The harness waited
200ms while holding the original handle. Assistant tool_use was display-only;
authority came from the matching incoming can_use_tool control request.

The CLI's real user tool result retained these exact synthetic answer entries:

```json
{
  "Which single option do you prefer?": "Option A",
  "Which choices do you want to select? (Select all that apply.)": ["Choice 1"],
  "Please provide your answer — pick one of the options or choose Other to enter your own free-text response?": "Synthetic free text"
}
```

All three original question objects/options were present in that tool result.
Only one multi-select label was selected; selecting multiple labels and joined
string encodings remain untested live. No unknown question/input field was
emitted, so live unknown-field preservation is **NOT TRIGGERED**, even though
offline whole-frame semantic round trips passed.

Correlated wire projections (request IDs below are aliases, not raw identifiers):

```json
{"type":"control_request","request_id":"QUESTION","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","input":{"questions":["<three real question objects>"]}}}
{"type":"control_response","response":{"subtype":"success","request_id":"QUESTION","response":{"behavior":"allow","updatedInput":{"questions":["<same objects>"],"answers":{"<full question text>":"<answer>"}}}}}
{"type":"control_cancel_request","request_id":"PENDING_QUESTION"}
{"type":"control_response","response":{"subtype":"success","request_id":"INTERRUPT","response":{"still_queued":[]}}}
```

These intentionally abbreviated projections are not executable fixtures.
The cancel input capture contained initialize, user and interrupt requests only;
no response to PENDING_QUESTION followed the rejected late Answer.
The eventual cancel result was `error_max_turns`, `terminal_reason:aborted_tools`;
interrupt acknowledgment alone was not treated as a completed turn or rollback.

## Coverage limits and follow-up

- **NOT COVERED:** explicit empty replacement `{}`, multi-label selections,
  joined-string multi-select, close/answer and concurrent cancel/answer races,
  disconnect ambiguity, deliberate live queue overflow, failed auth/init,
  unsupported controls, descendant escape and target CLI 2.1.302.
- **NOT TRIGGERED:** unknown question fields and auto-approved tool paths.
- No observed SDK runtime defect in these real flows. A macOS synthetic test
  portability defect was found and is assigned as a separate proposed repair
  ticket in [type-ticket04-followup.md](type-ticket04-followup.md). No runtime or
  test implementation was changed in this evidence ticket.
- Full compatibility certification remains **UNKNOWN**. These bounded partial
  checks do not satisfy every item in the live compatibility checklist.

## Synthetic checks on Mac

The first full suite failed TestOpenUsesLiteralArgumentsAndMatchedInitialize
because the platform supplied `/var/...` HOME while the child's cwd resolved
to equivalent `/private/var/...`; preserve this failure as evidence. With a
canonical owner-only `TMPDIR` under `/private/tmp`, all checks below passed:

| Check | Result |
| --- | --- |
| `go test -count=1 -timeout 180s ./...` | PASS with canonical TMPDIR |
| `go test -race -count=1 -timeout 300s ./...` | PASS with canonical TMPDIR |
| `go vet ./...` | PASS |
| Existing pressure selector, race count=30 | PASS |
| Process-group/escalation selector, race count=10 | PASS |
| `go list -deps` independent types boundary | PASS |

Go 1.26.9, GOTOOLCHAIN=local, GOENV=off, GOPROXY=off, GOSUMDB=off,
isolated writable GOCACHE; race checks used GORACE=atexit_sleep_ms=0.
Synthetic subprocess fixtures replace their child environment. The focused
selectors are unchanged from compatibility.md. CI remains synthetic and cannot
certify the real-CLI observations. Exact evidence commit and matching CI run
are reported after push to avoid a self-referential commit hash.
