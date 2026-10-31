# agent-sdk-testify

Go prepares environments and runs one native SDK test driver per case. The driver
owns SDK operations and business assertions; Go reads its final result and checks
process/capture/mock completion. No command/event negotiation or driver catalog.

Build the tool, Go simulation driver and official-driver wheel together:

```sh
bash scripts/simulation.sh build
```

The default build directory is `.artifacts/simulation`; set
`AGENT_SDK_TESTIFY_BUILD_DIR` to use another absolute directory. `PYTHON` selects
Python 3.11+ with setuptools>=68 installed. Builds do not run an Agent or install
SDK dependencies. Both driver implementations live together under
`tests/simulation/drivers/<agent>/{official,go}`. Shared case materials remain in
`tests/simulation/scenarios`; SDK targeted integration remains in `tests/integration`.

Use any installed driver executable. For Python, install the official SDK extras
from [Agent drivers](../../tests/simulation/drivers/README.md) and select its interpreter/script
or the installed `agent-sdk-testify-codex` or `agent-sdk-testify-claude` command. TS/Rust drivers can implement
the same request/result convention without adding language logic to Go.

## Protocol operations

One binary provides case orchestration and independent protocol operations.
The runner launches its own executable by default; `--peer` optionally selects
another `agent-sdk-testify` build. SDK tests use `AGENT_SDK_TESTIFY_BIN`.
The [module design](../../harness/modules/agent-sdk-testify/design.md) owns the contracts.

```sh
agent-sdk-testify protocol mock --provider codex --scenario /absolute/raw.jsonl
agent-sdk-testify protocol record --output /absolute/new-capture -- /absolute/native-cli
agent-sdk-testify protocol connect --help
```

Protocol operations need no driver or case materials. Protocol stdout contains
only native messages; diagnostics use stderr and separate reports. The persisted
`agent-mock-jsonl` v1 format and original recording bytes remain unchanged.

## Reuse an existing recording

```sh
agent-sdk-testify replay \
  --materials /absolute/private/SDK-US-001/SDK-CASE-001 \
  --driver /absolute/venv/bin/agent-sdk-testify-claude \
  --output /absolute/private/new-official-replay

agent-sdk-testify simulate \
  --materials /absolute/private/SDK-US-001/SDK-CASE-001 \
  --driver /absolute/bin/sdk-driver \
  --output /absolute/private/new-go-simulation
```

Both commands are offline. They use the current selected driver and mock, require
no native CLI/credentials, and do not require matching old tool/SDK versions.
Existing materials can be simulated independently. New samples follow build →
official record → official replay → source/content review → dataset admission →
current Go simulation. Go passing is not an admission condition; an admitted
sample is retained when Go simulation fails and blocks the SDK gate. Existing plan.json supplies
data and native protocol identities, not a frozen-driver execution requirement.
Existing source manifest hashes are checked for original frames, plan data and
saved history; this does not lock the driver, SDK or mock binary. Original raw frames are read without rewriting. Saved native history is copied
to the new run, and writable fixtures live in a new workspace. Recorded cwd stays
unchanged where it is part of native protocol or history identity.

For an interpreter, use `--driver /absolute/python --driver-arg /absolute/driver.py`.
Driver arguments are passed directly, without shell evaluation.

## Batch execution

`--cases-file` replaces `--materials` and accepts a JSON array:

```json
[
  {"case": "/private/codex/SDK-US-001/SDK-CASE-001"},
  {"case": "/private/claude/SDK-US-001/SDK-CASE-001"}
]
```

Each item may optionally select a `driver` with `command` and `args`; otherwise
the command-line driver is used. When every item selects a driver, no global
`--driver` is needed. Provider/case identity is read from materials.
`--jobs 4` runs four independent cases. `summary.json` retains every result,
including failures. A non-passing case makes the batch command fail.

## Explicit recording

Only explicit `record` or `protocol record` launches a native process. Ordinary tests and replay do not call it:

```sh
agent-sdk-testify record \
  --provider claude --case SDK-CASE-008 \
  --materials /absolute/repo/tests/simulation/scenarios \
  --driver /absolute/venv/bin/agent-sdk-testify-claude \
  --cli /absolute/claude --config /absolute/selected-claude-config \
  --output /absolute/private/new-capture
```

Go reads case data and provider bindings, but does not interpret JSON workflow
steps. Native drivers own their case code. Shared files and prompts are reused;
provider-specific native inputs stay explicit. `--config` copies only the selected
provider's configuration/credential files, never an entire HOME; inherited auth
and proxy environment values are selected explicitly. Those copies are removed
when execution finishes. Private output is not automatically admitted or published.

Existing-child case 035 takes `--history-seed /absolute/completed-034-capture`.
Go prepares saved native records directly from that completed capture; no SDK
replay is required first. Original parent/child IDs and project identity remain
unchanged, and the case observes them without new input.

Codex remote-close uses a Go-owned authenticated native service and two Go-owned
recording peers. The driver only performs and asserts the two SDK connections.
All process/CLI paths and driver dependencies must already be installed. Actual
versions and errors are diagnostic information, not exact-version gates.

## Minimal driver convention

Go starts `driver [prefix arguments] --request /absolute/request.json`:

```json
{
  "case": "SDK-CASE-008",
  "materials": "/private/run/materials",
  "workspace": "/private/run/project",
  "peer": {"command": "/absolute/peer-entry", "args": []},
  "output": "/private/run/driver"
}
```

Go supplies HOME, native config and tmp through the environment. Selected native
configuration contributes connection settings and credentials only; inherited
hooks, projects and integrations are excluded from the isolated attempt. New
captures scan selected credentials without rewriting protocol evidence; a match
marks `credentialScan: quarantined` and makes the run fail. `materials/case.json`
contains case data and any provider-specific source inputs. The driver must use
the supplied peer; it must not discover or launch an alternative real Agent.
Remote cases also receive `peer.probe` with the second command/args. Go SDK
child-policy comparisons receive `peer.suppress` with an independent command/args
and receipt. Go prepares their writable material views before launching the driver.
For Go SDK remote simulation, `peer.remote` supplies `url` and `bearerToken` for
the runner-owned offline service; the driver performs SDK calls against that endpoint.
SDKs that accept only an executable path receive a runner-generated launcher.
Materials contain no runtime `_mock` or `_peers` entry.

The private `peer` subcommand is a process-lifetime adapter, not a second control
protocol. It preserves protocol stdio and gives client EOF time to drain on TERM.

The driver writes `output/result.json`:

```json
{"status": "passed", "message": ""}
```

Statuses are `passed`, `failed`, `untriggered`, `unsupported`. Additional result
fields and diagnostic files belong to the driver; Go does not interpret them.
A driver that delivered a result exits 0, including a business failure. Crashes
and startup failures use a nonzero exit code. Logs use stderr, independent of SDK
protocol pipes. Go preserves the result even if process or transport checks fail.
Only a passing driver result and a valid run count as passed.

No additional runtime Go-version check, SDK/CLI allowlist, installation step, or
frozen binary hash is required. `--timeout` optionally bounds the whole command;
zero is unlimited. Version probes share the task cancellation context. Cancellation
reaps owned processes and preserves diagnostics. Recording output is directly
reusable as the next stage's material directory, with a source-data manifest.
Protocol matching and business assertions remain strict.

Without `--output`, attempts persist under `$AGENT_SDK_TESTIFY_DATA_DIR/attempts/<mode>/attempt-*`.
The data root defaults to `$XDG_DATA_HOME/agent-sdk-testify`, or
`~/.local/share/agent-sdk-testify` when XDG_DATA_HOME is unset. Explicit output must
be a new directory whose parent exists; existing attempts are never overwritten.
Tests pass their own temporary output paths. Failures remain for review.
See the [driver handoff contract](../../tests/simulation/drivers/README.md#stage-handoff) for the next stage.

## Offline validation

```sh
mise exec -- bash scripts/with-agent-sdk-testify.sh go test ./tools/agent-sdk-testify ./tests/simulation/drivers
```

Runner tests use a synthetic native process and the real Go record/mock executable.
They check fresh environments, unchanged source data, different tool identities,
driver errors, false pass reports, capture completion and cancellation. Private
reviewed real recordings are indexed by the required corpus gate. See the
[adoption and gate commands](../../tests/simulation/drivers/README.md#审核采用和日常门禁).
Missing required data fails CI; synthetic tool tests do not fill that gap.
Actual real-Agent execution is not implied by an offline or synthetic test pass.
