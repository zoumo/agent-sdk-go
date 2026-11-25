# SDK simulation

<a id="repository-recording"></a>

Use this guide to run the Go SDK against fixed native recordings. The SDK's Go
drivers call public SDK operations and check application behavior. The linked
Replay library prepares fixtures, matches protocol traffic, and produces
typed results. [Go library contract](docs/library-mode.md)

<a id="fixtures-仿真录制与测试输入"></a>

## 仿真录制与测试输入

Run commands from the repository root with Go 1.26 or newer. For the full
strict gate, run `TestSimulationGate`. For a single built-in case, select the
provider and case ID with `TestSimulationCase`. To replay an external capture,
pass its path to `-simulation.recording`.

Set `-simulation.output` to a new absolute archive directory whose parent exists.

```sh
go test -count=1 -timeout=600s -v ./tests/simulation -run '^TestSimulationGate$' -simulation.jobs=2 -simulation.output=/absolute/new-gate
go test -count=1 -v ./tests/simulation -run '^TestSimulationCase$' -simulation.provider=claude -simulation.case=SDK-CASE-001 -simulation.output=/absolute/new-run
go test -count=1 -v ./tests/simulation -run '^TestSimulationCase$' -simulation.recording=/absolute/external-capture -simulation.output=/absolute/new-run
```

The test requires a clean checkout to verify build identity. If Go omits the
VCS metadata, the test rebuilds once with VCS stamping. `TestMain` builds the SDK driver. The test coordinates the simulation and
starts each driver in its own environment, workspace, and process group.

The test uses a short private directory for the run and archives the evidence
afterward. See [offline validation](../../docs/testing.md) for the default
archive location, cleanup, and failure handling.

### 审核采用和日常门禁

Prepare is the only material source entry. SDK validates the schemaVersion 7
fixture identity, embedded identity, source and in-process mock route. Export
into a new directory outside the fixture is the only assertion-input source: SDK
validates the manifest-bound export inventory, plan/source-plan, native frames,
material report and plan-bound saved history before projection.

The material plan and source streams are projected into INTERNAL SDK fixture
files under each run's sdk directory so the existing typed Go flows retain their
assertions. Source bytes and projected `agent-mock-jsonl` bytes are both retained
with hashes. Portable materials supply assertion expectations in their material
report; official verification and execution evidence remain outside the export.
Legacy external captures retain their original report selection. The original
source plan remains separately hash-bound provenance; the material plan supplies
creation arguments, frozen seed identity and explicit history bindings.
SDK history expectations come from the bound transcript bytes, and child
relationships come from recorded native protocol facts.
Those files are SDK implementation details, not paths the SDK may discover inside
the tool. runtime.json contains workspace/config paths from the prepared host.
Opaque saved-history IDs never become paths. Claude history queries use the
config and workspace of the actual execution, resolved from the typed execution
start rather than the pre-launch template.

Prepare twice for policy comparison. Export every successful prepare, read its
execution evidence, and remove the caller-owned fixture after failures or
cancellation. summary.json keeps business results separate from
protocol/evidence/bridge failures. Typed execution reports bind the prepared
fixture, execution, connection and material hashes; SDK validation must bind
exactly the one execution that used that export even if SDK projection files
disappear.
Source capture paths are opaque to assertions; external captures are passed only
to library prepare/export operations.

Read `coverage.json` for source verification, route, requirement, and SDK
simulation status for each provider and case. The gate fails for required gaps
and corrupt sources. The report retains optional business failures.
`TestSimulationGate` with `-simulation.recording` still evaluates the full
matrix. The selected recording can pass its own row, and the remaining rows
report unverified coverage. Run `TestSimulationCase` to check one capture.
Only qualifying native recordings contribute to required coverage.

<a id="fixtures-私有仿真数据"></a>

### 私有仿真数据

Store source captures and execution evidence separately. Never rewrite frames,
IDs, unknown fields, numeric lexemes or provenance to obtain a pass. Official
verification, controlled synthetic SDK checks and real-material SDK simulation
are independent evidence. No model or new recording runs by default.

<a id="fixtures-代码集成测试输入"></a>

### 代码集成测试输入

Provider fixture packages under `internal/testsupport/` embed synthetic inputs.
`internal/testsupport/mocktest`
converts them into manifest-bound synthetic captures and drives the in-process
library lifecycle with typed execution evidence.
Simulation hosts separately consume typed reports bound to the
exported material; the adapter contains no matching engine. Full-consumption
notification signals when tests can half-close SDK input.

### Replay Pi extensions through the unified Go SDK

The fixed recording gate replays official Pi cases through the native Go SDK. [Shared cases](../../docs/sdk/validation-cases.md)
Use this entry point for controlled captures made through the unified Client.
On a Unix host with a writable `/tmp`, build the driver from the repository
root with the same-module Replay library.

```sh
GOWORK=off go build \
  -o /absolute/go-simulation-driver ./tests/simulation/driver
```

Supply the unchanged Replay consumer export directory for the selected case.
It contains `material.json`, `plan.json`, and `raw/raw.jsonl`. Use a capture
of the unified flow from official Pi 1.6.0 with a controlled loopback model.
The recorded requests include `get_commands` during session open and
`streamingBehavior: steer` on explicit prompts. Write `/absolute/request.json`
with an absolute path to the export and a new output directory whose parent
already exists.

```json
{
  "case": "012",
  "material": "/absolute/pi-extension-captures/012/export",
  "output": "/absolute/new-replay-012"
}
```

```sh
/absolute/go-simulation-driver --pi-extension-replay /absolute/request.json
```

Run each case from `008` through `014` and from `037` through `038` with its own
export and output directory. The driver starts a local mock backed by the
Replay Go library. It maps the recorded temporary extension path to the SDK
session's generated extension and configuration on disk. Run replays of the
same export serially because they reserve the same temporary path.

Read `observations.jsonl` for SDK events, acknowledgments, and session close.
The execution report binds the source hash and matched frame count to the
strict replay result, full consumption, and client EOF. Use the source capture
report to verify native Pi cleanup and the loopback model requests. This entry
runs on demand with explicit captures.

<a id="catalog-仿真测试流程目录"></a>

## 仿真测试流程目录

The SDK uses the library's public prepare and export operations for every
provider and case combination in its acceptance matrix. Replay supplies the
scenarios and bindings.
[SDK acceptance matrix](coverage.go) · [共同验收](../../docs/sdk/validation-cases.md#stories-sdk-用户故事)

The Go driver implements the public SDK route for available cases. `passed`,
`failed` and `unverified` status is recorded per execution; catalog availability
alone does not prove that a flow succeeded. Focused synthetic tests remain code
integration tests and do not add real-recording evidence.
