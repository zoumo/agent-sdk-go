# Recording scenario configuration

These JSON files define the recording product independently of its executor
language. The design contract is [recording scenarios](../../../harness/sdk/recording-scenarios.md).
Python is the current consumer; another language can consume the same files.

- `index.json` enumerates scenarios.
- `SDK-US-*.json` define goals, initial files, named inputs, ordered semantic
  steps, expected interactions, missing-evidence behavior and limits.
- `bindings/codex.json` and `bindings/claude.json` describe provider-specific
  prompt variants, files, interaction names, answers, configuration and capability
  limits. A binding overrides only its explicit fields. Other values come from
  the common scenario.
- `*.schema.json` are JSON Schema Draft 2020-12 schemas for independent consumers.

For example, `SDK-US-003.json` contains the common flow:

```json
[
  {"op": "session.open"},
  {"op": "input.submit", "input": "input-1"},
  {"op": "event.wait", "event": "execution.progress", "onMissing": "untriggered"},
  {"op": "input.submit", "input": "input-2"},
  {"op": "event.wait", "event": "execution.result", "onMissing": "untriggered"},
  {"op": "session.close"}
]
```

`execution.progress` is a semantic event. A native adapter interprets its
provider's actual events; the common scenario does not refer to a Python class
or a native RPC name. `input-1` and `input-2` refer to the scenario's `inputs` map.
Provider prompt lists, when present, replace input texts in submission order.

Scenario version 2 supports sequential submit/result pairs, one progress barrier followed
by either one supplemental submission or one interrupt, and an optional initial
configuration operation. Unsupported operations, references or order fail before
native launch. There are no embedded scripts, expressions, sleeps or arbitrary
shell commands. Native API implementation and answer authority belong to the
executor's adapter. Declaring a binding does not implement a new provider.

Expected interaction names are semantic, such as `fixture.read`, `fixture.write`
and `question.answer`. Bindings map these to native driver observations. Native
configuration fields and capability notes remain provider-specific; the consumer
must reject unsupported native operations rather than guess a translation.

The Python consumer performs schema and workflow validation, then compiles these
files into a private execution plan. It derives prompts, sequential versus active
submission, interrupt behavior, request order, question answers and initial files
from this configuration. Fixed native tool policies are still implemented by the
adapters; editing configuration does not grant new tool permissions.

The resource ceilings are part of the schema. Lower time/byte budgets are
consumed by the Python worker and proxy; an unsupported expansion is rejected.
A new operation or broader schema requires an executor capability change. An
ordinary prompt edit or supported workflow composition requires only a config
edit and a new preparation, not a Python source edit.

Preparation freezes the JSON definitions, schemas and bindings alongside the
Python source snapshot. Both kinds of files participate in snapshot hashing and
manifest verification. Historical snapshots without JSON files retain their
original Python-only identity. No old capture is regenerated or relabeled.

Inspect the resolved configuration without an SDK or agent:

```sh
python3 tools/recording/agent_recording/bootstrap.py catalog --provider codex --stories SDK-US-003
```

Each v2 `expect` includes `caseId`, one concrete `scope`, and `outOfScope`.
The resolved `case` is frozen in the plan, included in the report and manifest,
and compared during replay. A passed case does not claim full-US acceptance.
Case 009 contains one denied-write input; approval success is a separate branch.
Case 012 requests model configuration only. Missing required requests or progress
remain `untriggered`; other variants belong in `outOfScope`, not runtime gaps.
Historical v1 definitions and driver snapshots keep their original identities.
