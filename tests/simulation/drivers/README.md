# SDK simulation drivers

<a id="repository-recording"></a>

`codex/go` and `claude/go` own public SDK operations and business assertions.
`../simulate` owns process orchestration, independent policy hosts, coverage,
cancellation and authenticated loopback remote main/probe forwarding.
The [external tool](https://github.com/zoumo/agent-sdk-testify) owns record/verify,
official drivers, case/binding contracts, recordings, matching, preparation and
export. SDK code links the exact pinned module and consumes only its public
prepare/open/session, execution-start and typed execution-report surfaces. It
never resolves or reads the tool's source or material directories directly.
Embedded identity records the module revision and sum, while material identity
records the immutable builtin source revision separately. This repository does
not build tool sources or read legacy recordings, scenarios, official-driver
code or Python packaging as a fallback.

<a id="fixtures-仿真录制与测试输入"></a>
<a id="stage-handoff"></a>

## 仿真录制与测试输入

Build only the SDK host and simulate executable:

```sh
bash scripts/simulation.sh build
bash scripts/simulation.sh simulate --provider claude --case SDK-CASE-001 --output /absolute/new-run
bash scripts/simulation.sh simulate --recording /absolute/external-capture --output /absolute/new-run
bash scripts/simulation.sh gate --jobs 2
```

Use Go 1.26 or newer. Test and simulation hosts link the pinned in-process
testify library; no external binary or environment override is required. Build
never creates the tool or a wheel.

### 审核采用和日常门禁

Prepare is the only material source entry. SDK validates the schemaVersion 5
session descriptor, embedded identity, source and in-process mock route. Export
into a new directory outside the session is the only assertion-input source: SDK
validates the export inventory, plan/source-plan, native frames, report and
manifest-bound saved history before projection.

plan.json/case.json and source streams are projected into INTERNAL SDK fixture
files under each run's sdk directory so the existing typed Go flows retain their
assertions. Source bytes and projected `agent-mock-jsonl` bytes are both retained
with hashes. For built-in exports, the official verification report supplies the
assertion expectations; an external capture falls back to its top-level report.
Those files are SDK implementation details, not paths the SDK may discover inside
the tool. runtime.json contains workspace/config paths from the prepared host.
Opaque saved-history IDs never become paths. Claude history queries use the
config and workspace of the actual execution, resolved from the typed execution
start rather than the pre-launch template.

Prepare twice for policy comparison. Export every successful prepare, read its
execution evidence, and remove the caller-owned session after failures or
cancellation. summary.json keeps business results separate from
protocol/evidence/bridge failures. Typed execution reports bind the session,
execution, connection and material hashes; SDK validation must bind exactly the
one execution that used that export even if SDK projection files disappear.
Source capture paths are opaque to assertions; external captures are passed only
to library prepare/export operations.

Coverage retains all 72 provider/case slots with source verification, route,
required/optional and current simulation status. Required gaps fail. Optional
business failures stay visible; source corruption cannot be waived. `--gate
--recording` evaluates all 72 slots: only the selected material can pass and the
other 71 remain unverified, so the gate still fails. A synthetic or consumer input
cannot fill real-material coverage. SDK failures preserve external source bytes
and the source index.

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

The SDK-owned 72-slot provider/case matrix and its required/optional policy come
from the shared acceptance catalog. The gate probes each slot through public
prepare/export operations; there is no runtime list/describe catalog. This
repository does not copy tool scenarios, recordings or official drivers. Story
and case semantics remain in
[共同验收](../../../harness/sdk/validation-cases.md#stories-sdk-用户故事).

The Go driver implements the public SDK route for available cases. `passed`,
`failed` and `unverified` status is recorded per execution; catalog availability
alone does not prove that a flow succeeded. Focused synthetic tests remain code
integration tests and do not add real-recording evidence.
