---
type: Design
title: SDK-owned simulation against external mock
status: stable
---

# SDK simulation

The SDK owns simulate, its Go driver, business assertions, policy comparison,
parallelism, cancellation, coverage and its authenticated loopback bridge.
The independent agent-sdk-testify owns case/binding contracts, official drivers,
record/verify, material qualification, strict protocol matching, history
materialization, environment preparation and receipts. It exposes record,
verify, official orchestration and the consumer lifecycle implemented by prepare,
agent mock and export. The SDK consumes processes, never a cross-repository Go
library.

The proposed Go-library backend for Go test hosts is defined in
[Go library mode](library-mode.md); this page remains the accepted process
contract until that proposal is adopted.

<a id="story-官方-sdk-仿真测试"></a>
<a id="story-录制生命周期"></a>
<a id="story-场景配置"></a>

<a id="内置材料与下游直接使用"></a>

## Process seam

Prepare one session per policy, using `--agent/--case` or `--recording` and a new
absolute session directory. Read schemaVersion 3, then validate the tool path,
version and hash, source identity, route and connection commands before launching
anything. The session has no finish command and its runtime must be
`<session>/runtime`. Unknown versions fail. Do not use runtime
list/describe, read the tool recording layout or replay captured client frames as
a substitute for public SDK operations.

Before starting each SDK host, apply the mock route's env, workspace and protocol
cwd. Start only its declared connection commands, or Claude's declared launcher,
through `agent mock --session-dir ... --connection ...`; consume result notices
from the declared result descriptor rather than inferring an execution. For Claude
history, resolve the materialized config and workspace from the launched
execution descriptor in the start notice, not from the pre-launch session template.

Export to a new directory outside the prepared session. Validate the complete file
inventory, plan and source-plan relation, native-stream hashes, manifest-bound
saved history and remote phases, report, official origin and connection set.
Preserve source bytes and project only INTERNAL SDK assertion files from that
export. The projection may decode a synthetic envelope or copy an already
agent-mock-jsonl source, but it must retain both source and projected hashes. Store
runtime paths independently from exported paths.

The driver performs typed public Session/Client/history operations and retains
native answer handles. It emits result and deliveries. Each policy uses its own
host process, config and run identity; compare policies only after both complete.
Remote-close uses SDK-owned authenticated loopback main/probe forwarding, rejects
extra connections, closes/drains and waits for all owned children.

<a id="story-录制限制"></a>
<a id="story-严格离线回放"></a>

## Completion

Close SDK Sessions/Clients and services, wait for owned peers, read the execution
index and result notices, and always remove every successful prepared session,
including after export/projection failure, business failure and cancellation.
Cleanup uses a separate bounded context. A business completion event is not EOF.
Retain business passed when later receipt/bridge/cleanup validation fails, and
fail the overall run. Every declared connection must have matching
session/execution/source identities, strict full consumption, client EOF, no
native launch, normal process exit and successful owned-resource cleanup. SDK
evidence validation must bind exactly the one execution that used the exported
material. Never loosen matching or change production SDK behavior to make a
fixture pass.

<a id="story-数据格式与存储"></a>
<a id="story-结果与错误"></a>
<a id="story-版本责任与证据"></a>

<a id="审核采用与固定回归门禁"></a>
<a id="测试与依赖"></a>

## Evidence and coverage

Persist tool version/hash, SDK source/build identity, prepared session descriptors,
export inventories and hashes, source/runtime separation, raw and report
projection evidence, SDK results/deliveries, materialization and cleanup evidence
outside recordings. Preserve unknown native fields, number lexemes, IDs and
original source bytes.

Complete coverage has 36 cases across both providers. Source verification,
required/optional policy and current SDK simulation status remain separate.
Required gaps fail; optional business gaps stay visible; source corruption fails
regardless of optional policy. An explicitly selected external synthetic input
cannot promote source qualification or fill real recording coverage. Never
rewrite the source index or discard trusted material after SDK failure.

No published release pin is implied by a local 0.1.0-dev executable. Until an
actual release exists, use explicit absolute AGENT_SDK_TESTIFY_BIN and record its
identity. Do not build legacy tool sources or install official SDK dependencies
from the SDK check entry. Real Agent/model execution and new recording remain
explicit opt-in work, outside default validation.
