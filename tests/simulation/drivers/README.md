# SDK simulation drivers

<a id="repository-recording"></a>

Use this guide to run the Go SDK against fixed native recordings. The SDK's Go
drivers call public SDK operations and check application behavior. The linked
Testify library prepares fixtures, matches protocol traffic, and produces
typed results. [Go library contract](../../../harness/modules/agent-sdk-testify/library-mode.md)

<a id="fixtures-仿真录制与测试输入"></a>
<a id="stage-handoff"></a>

## 仿真录制与测试输入

Run commands from the repository root with Go 1.26 or newer. For the full
strict gate, run `TestSimulationGate`. For a single built-in case, select the
provider and case ID with `TestSimulationCase`. To replay an external capture,
pass its path to `-simulation.recording`.

Use a new output directory for each run that sets `-simulation.output`.

```sh
go test -count=1 -timeout=600s -v ./tests/simulation/simulate -run '^TestSimulationGate$' -simulation.jobs=2
go test -count=1 -v ./tests/simulation/simulate -run '^TestSimulationCase$' -simulation.provider=claude -simulation.case=SDK-CASE-001 -simulation.output=/absolute/new-run
go test -count=1 -v ./tests/simulation/simulate -run '^TestSimulationCase$' -simulation.recording=/absolute/external-capture -simulation.output=/absolute/new-run
```

`TestMain` builds the SDK driver. The test coordinates the simulation and
starts each driver in its own environment, workspace, and process group.

The default gate runs in a short private directory and archives its unchanged
output under `.artifacts/simulation/gates/`, including failures and handled
cancellation. This avoids deep-checkout project keys without changing Claude's
native 200-character limit. An explicit `-simulation.output` remains caller-owned.
See [offline validation](../../../docs/testing.md) for evidence locations,
temporary-directory cleanup and archive failure handling.

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

Native tests/testdata integration inputs are explicitly synthetic. internal/mocktest
converts them into manifest-bound synthetic captures and drives the in-process
library lifecycle with typed execution evidence.
Simulation hosts separately consume typed reports bound to the
exported material; the adapter contains no matching engine. Full-consumption
notification signals when tests can half-close SDK input.

<a id="catalog-仿真测试流程目录"></a>

## 仿真测试流程目录

The SDK checks every provider and case combination with the library's public
prepare and export operations. The shared acceptance catalog defines each
story, case, and requirement.
[共同验收](../../../harness/sdk/validation-cases.md#stories-sdk-用户故事)

The Go driver implements the public SDK route for available cases. `passed`,
`failed` and `unverified` status is recorded per execution; catalog availability
alone does not prove that a flow succeeded. Focused synthetic tests remain code
integration tests and do not add real-recording evidence.
