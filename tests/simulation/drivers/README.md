# SDK simulation drivers

<a id="repository-recording"></a>

`codex/go` and `claude/go` own public SDK operations and business assertions.
`../simulate` owns process orchestration, independent policy hosts, coverage,
cancellation and authenticated loopback remote main/probe forwarding.
The [external tool](https://github.com/zoumo/agent-sdk-testify) owns record/verify,
official drivers, case/binding contracts, recordings, matching and preparation.
Its current source layout groups the authoritative scenarios, admitted provider
materials, fixed index and generated builtin snapshot under `data/`. That layout
is internal to the tool: SDK code consumes only the versioned list/describe
process contract and never resolves or reads these paths directly. The status
file records the tool source revision and immutable builtin source revision
separately, so source-layout changes do not rewrite material provenance.
This branch does not build tool sources or read legacy recordings, scenarios,
official-driver code or Python packaging as a fallback.

<a id="fixtures-仿真录制与测试输入"></a>
<a id="stage-handoff"></a>

## 仿真录制与测试输入

Build only the SDK host and simulate executable:

```sh
bash scripts/simulation.sh build
export AGENT_SDK_TESTIFY_BIN=/absolute/prebuilt/agent-sdk-testify
bash scripts/simulation.sh simulate --provider claude --case SDK-CASE-001 --output /absolute/new-run
bash scripts/simulation.sh simulate --recording /absolute/external-capture --output /absolute/new-run
bash scripts/simulation.sh gate --jobs 2
```

Use Go 1.26 or newer. There is no published release pin; the single status
file records the observed development artifact source/hash and is not a release
pin. Build never creates the tool or a wheel. Hosted CI separately builds the
public tool at the status file's source revision and exports its absolute path;
local checks still require an explicit `AGENT_SDK_TESTIFY_BIN`.

### 审核采用和日常门禁

The tool's describe is the only assertion-input source. Its native frames,
originalBase64, report and savedHistory are projected into INTERNAL SDK fixture
files under each run's sdk directory so the existing typed Go flows retain their
assertions. Those file names are SDK implementation details, not the tool's
recording contract. plan.json/case.json contain exact source bytes; runtime.json
contains workspace/config paths. Opaque saved-history IDs never become paths.
SDK history queries use the prepared host environment, set before process startup.

Prepare twice for policy comparison. Close/wait and finish every selected run,
including failures/cancellation. summary.json keeps business results separate
from protocol/process/finish/bridge failures. Every selected connection is checked
by finish even if SDK projection files disappear. Source capture paths are opaque
to assertions; external captures are passed only to tool CLI operations.

Coverage retains all 72 catalog combinations with source verification, route,
required/optional and current simulation status. Required gaps fail. Optional
business failures stay visible; source corruption cannot be waived. --gate with
--recording runs only the matching provider/case and reports the other 71 as
unverified; this is useful for synthetic checks and cannot qualify real material.
SDK failures preserve external source bytes and the source index.

<a id="fixtures-私有仿真数据"></a>

### 私有仿真数据

Store source captures and execution evidence separately. Never rewrite frames,
IDs, unknown fields, numeric lexemes or provenance to obtain a pass. Official
verification, controlled synthetic SDK checks and real-material SDK simulation
are independent evidence. No model or new recording runs by default.

<a id="fixtures-代码集成测试输入"></a>

### 代码集成测试输入

Native tests/testdata integration inputs are explicitly synthetic. internal/mocktest
converts them into manifest-bound external captures and uses mock/report rather
than the removed legacy protocol CLI. SDK-only answer/assert barriers are actual
synthetic frames; the adapter contains no matching engine. The mock's inherited
progress descriptor signals consumption before EOF so tests can close input.

<a id="catalog-仿真测试流程目录"></a>

## 仿真测试流程目录

The external tool's `mock --list --json` is the authoritative 72-combination
provider/case catalog, including availability and required/optional policy.
`mock --describe` supplies each selected case's original plan, bindings, saved
history and assertion inputs. This repository does not copy that catalog or its
scenario files. Story and case semantics remain in
[共同验收](../../../harness/sdk/validation-cases.md#stories-sdk-用户故事).

The Go driver implements the public SDK route for available cases. `passed`,
`failed` and `unverified` status is recorded per execution; catalog availability
alone does not prove that a flow succeeded. Focused synthetic tests remain code
integration tests and do not add real-recording evidence.
