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
verify and mock. The SDK consumes processes, never a cross-repository Go library.

<a id="story-官方-sdk-仿真测试"></a>
<a id="story-录制生命周期"></a>
<a id="story-场景配置"></a>

<a id="内置材料与下游直接使用"></a>

## Process seam

Read mock list/describe schemaVersion 1, preserve full catalog metadata and reject
unsupported descriptor/assertion versions. Use describe assertionInputs for
ordered native operations, original handles, expected facts, reports and frozen
history. Do not read the tool recording layout or replay captured client frames
as a substitute for public SDK operations.

Prepare separately for preserve and suppress. Before starting each SDK host,
apply returned env and workspace. Select declared connection commands or Claude's
no-argument launcher. The immutable source contract and originalBase64 values
remain unchanged. Any transitional fixture files are INTERNAL SDK projections
from describe, not tool paths. Store runtime paths independently.

The driver performs typed public Session/Client/history operations and retains
native answer handles. It emits result and deliveries. Each policy uses its own
host process, config and run identity; compare policies only after both complete.
Remote-close uses SDK-owned authenticated loopback main/probe forwarding, rejects
extra connections, closes/drains and waits for all owned children.

<a id="story-录制限制"></a>
<a id="story-严格离线回放"></a>

## Completion

Close SDK Sessions/Clients and services, wait for owned peers, and always finish
every successful prepare, including business failure and cancellation. Cleanup
uses a separate bounded context. A business completion event is not EOF. Retain
business passed when later receipt/bridge/cleanup validation fails, and fail the
overall run. Every declared connection must have matching run/source identities,
strict full consumption, client EOF, no native launch, normal process exit and
successful owned-resource cleanup. Never loosen matching or change production
SDK behavior to make a fixture pass.

<a id="story-数据格式与存储"></a>
<a id="story-结果与错误"></a>
<a id="story-版本责任与证据"></a>

<a id="审核采用与固定回归门禁"></a>
<a id="测试与依赖"></a>

## Evidence and coverage

Persist tool version/hash, SDK source/build identity, immutable describe/plan
identities, all prepared descriptors and source hashes, SDK results/deliveries,
materialization, finish and cleanup reports outside recordings. Preserve unknown
native fields, number lexemes, IDs and original source bytes.

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
