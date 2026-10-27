# Official Python SDK protocol recording

This is a separate Python tool package in the existing repository. It is not a Go
module. Recording does not import or execute this repository's Go SDK. The pinned
[official Codex SDK](https://learn.chatgpt.com/docs/codex-sdk) drives app-server;
the [official Claude SDK](https://code.claude.com/docs/en/agent-sdk/python) drives
stream-json. The Python proxy captures original bidirectional JSON bytes at the
native process boundary. SDK result objects are separate receipts, not wire data.

Use Python 3.11 or later and install `requirements.lock` into an external venv.
`pyproject.toml` also supports an editable installation from this checkout
(`pip install -e tools/recording`), with the `agent-recording` entry point.
Preparation requires the source checkout to retain repository provenance. The bootstrap works without installing the local package:

```sh
python3 -m venv /tmp/agent-recording-venv
/tmp/agent-recording-venv/bin/pip install -r tools/recording/requirements.lock
/tmp/agent-recording-venv/bin/python tools/recording/agent_recording/bootstrap.py prepare \
  --provider claude --output /tmp/new-claude-suite --stories SDK-US-001,SDK-US-004
/tmp/agent-recording-venv/bin/python tools/recording/agent_recording/bootstrap.py record \
  --output /tmp/new-claude-suite
mise exec -- go build -o /tmp/claude-mock ./claude/cmd/mock
/tmp/agent-recording-venv/bin/python tools/recording/agent_recording/bootstrap.py replay \
  --output /tmp/new-claude-suite --peer /tmp/claude-mock
```

`prepare` makes a new absolute directory under `/tmp`, outside Git. It creates a
separate HOME, config, synthetic project, tmp and raw directory for each recordable story.
Use `--stories` to select individual `SDK-US-001` through `SDK-US-019`; default is
all 19, preparing only the live selected paths and printing the other routes. An
offline-only selection is rejected before SDK or authentication access. `--cli`, `--model`, and `--source-config` select an explicit local CLI,
model, or existing configuration directory. No project/session/skill discovery
runs against the source HOME. CLI version checks do not launch a model.

The reviewed pair is `openai-codex==0.161.0` with Codex CLI `0.161.0`, and
`claude-agent-sdk==0.2.174` with Claude CLI `2.1.199`. Unknown CLI or SDK versions
fail before native launch. The SDK versions are independent of historical fixture
versions. Codex's public `CodexClient.thread_start(JsonObject)` overload preserves
`dynamicTools`, which that pinned Python generated parameter model omits. A
strict explicit callback replaces the official client's permissive approval
default. Claude's custom `Transport` is a small locked adapter for this SDK's
internal transport contract. It launches only the reviewed fixed native flags,
without default SDK subprocess option/environment merging.

Only the explicit connection/authentication selection is inherited:

- Codex: model/provider selection, selected provider connection fields,
  `auth.json`, named OpenAI/Codex key variables and the selected provider's
  explicitly named authentication/header environment variables.
- Claude: endpoint and authentication from one source as a unit, selected model
  variables and optional `.credentials.json`. No apiKeyHelper or login runs.
- Both: named HTTP/HTTPS/ALL/NO proxy and SSL certificate variables only.

All subprocess environments are constructed from that allowlist. Runtime SDKs
receive isolated HOME/config, not the caller's full environment. Native tools
are disabled except the selected synthetic fixture tools. Claude Read/Write
permissions require the exact new fixture path/content; other writes and tools
are denied. Codex fixture callbacks operate only on fixed new files. Neither
provider may use shell, external project files or child agents. Copies of
credentials and connection settings are removed after each attempt. Preparation
alone leaves those private copies until `record` or manual deletion of that new
output directory; it never alters source configuration.

Each story has `plan.json`, `attempt.started`, `driver/agent_recording/*.py`
(an immutable source snapshot for new preparations), `report.json`,
`manifest.json`, and, after native launch, `raw/raw.jsonl`, `raw/stderr.raw`,
`raw/transport.json` and `native.started`. All material stays private. Files and
protocols have byte limits; an owned native process group has a deadline and is
terminated/reaped. The proxy rejects unexpected child/hook output. It cannot
prove an administrator hook never ran before an observable event.

The explicit `record` command is live authority; CI refuses it. Each item has one
native launch and no automatic retry. Failed attempts retain evidence. Activity
is bounded to 110 seconds, transport to 120 seconds, protocol to 2 MiB and stderr
to 256 KiB. Each story has at most two fixed user inputs. These limits do not
establish independent billing or provider-request/retry limits.

`replay --peer` verifies capture and saved-source hashes, exports a provider-native
`go-mock-scenario.json`, runs the original official SDK operations against the
selected Go mock, and requires `replay-peer.json` proving full consumption/EOF.
It has no real CLI or authentication fallback. Repeated replay archives prior
reports/receipts under `replays/attempt-*` before creating new outputs. Fixture callback effects use a
fresh replay project. The complete client JSON is matched; only existing native
RPC/control envelope ID correlation is dynamic. Raw captures are never edited.

The optional Go public-Session check consumes SDK-US-001 independently:

```sh
SDK_OFFICIAL_CODEX_ROOT=/tmp/new-codex-suite/SDK-US-001 \
SDK_OFFICIAL_CLAUDE_ROOT=/tmp/new-claude-suite/SDK-US-001 \
  mise exec -- go test ./tests/stories -run TestOfficialPythonRecordings -count=1 -v
```

This check starts a Go mock test subprocess, requires its full-consumption
receipt, and verifies manifest hashes. Codex requests use recorded native start
options and the public Session. Claude requires the exact reviewed Python
initialize envelope before adding Go's `forwardSubagentText` capability, and
changes only Python's `default` stream marker to Go's empty marker. Derived
Claude expectations are labeled synthetic adaptation; the unchanged server data
and original provenance remain recorded. No arbitrary unmatched request is
ignored. Default tests skip this private-source check, and run safety unit tests
without installing SDK dependencies:

```sh
PYTHONPATH=tools/recording python3 -m unittest discover -s tools/recording -p 'test_*.py' -v
```

New scoped cases use `passed` when their required scenario evidence is present.
Each report stores `case.id`, `case.scope` and `case.outOfScope`; unrelated US
branches do not become gaps. Missing in-scope evidence remains `untriggered` or
`failed`. Historical snapshots retain their original `partial` reports. Default
006/007 never launch a model; Codex 009 never grants an unbounded approval. See [workflow](../../docs/record-stories.md)
and [new evidence](../../docs/validation/official-python-recording-2026-10-27.md).
Historical Go-driven recordings use the separate legacy replay path.

## Versioned deterministic scenarios

The design contract is [recording scenarios](../../harness/sdk/recording-scenarios.md).
The language-neutral source is [JSON scenario configuration](scenarios/README.md);
Python loads and interprets it rather than generating the scenario definitions.
Inspect exact prompts, initial files, answers, barriers, input limits, execution
mode, applicable native-story references and public offline test names without SDK installation or configuration access:

```sh
python3 tools/recording/agent_recording/bootstrap.py catalog --provider codex
python3 tools/recording/agent_recording/bootstrap.py catalog --provider claude --stories SDK-US-003,SDK-US-011
python3 tools/recording/agent_recording/bootstrap.py offline
python3 tools/recording/agent_recording/bootstrap.py offline --provider claude --stories SDK-US-015,SDK-US-019
```

`offline` uses the mise-pinned Go compiler, disables live switches and dependency
downloads, and checks that every selected public test actually passed. Its
results are synthetic evidence, including the controlled storage cases; they do
not make new recordings. A missing/skipped test is not accepted as a pass.

New standard preparations use plan version 3. `plan.json` embeds the canonical scenario and
its SHA256; the manifest also binds the complete plan. Raw JSON definitions,
schemas and provider bindings are copied into each driver snapshot and hashed
alongside source code, so replay needs no newer checkout configuration. The same saved driver and
scenario run during capture and replay. Existing version-2 datasets retain their
original driver snapshots and evidence identity.

Selected live paths are 001–004, 011–013, 016, plus Claude 009/010. Scenarios
005/008/014/015/017/018/019 use explicit offline routes rather than unrelated
text generation. Default 006/007 remain blocked; Codex 009 is blocked and Codex
010 uses the offline route. The catalog describes each reason. `record` skips
these modes before reserving an attempt or launching a native process.

The workflow sends the second ordinary input only after a native result. For
003 it sends the second input once at the first native progress barrier; for 016
it interrupts once at that barrier. Missing barriers never cause a sequential
fallback or an extra prompt. Native accept/reject/write-only outcomes remain
separate. This controls application ordering; it does not freeze model scheduling.

Reports include `scenarioSHA256`, `flowTrace` and `branchStatus`. Required tool and
question requests are checked in order, independently of textual output. Missing
requests/barriers produce `untriggered` and stop recording without a retry. A
passed selected branch now sets the case status to `passed`; it does not certify the whole US. Claude 010 also requires
an actual event during the held permission interval; a control acknowledgement
alone leaves that branch untriggered. Only the existing choice-question branch
is live; free-text and other question variants are outside this choice-question case.

Replay compares the semantic flow, branch outcome and scenario hash as well as
native receipts, request/input/result counts and complete Go-peer consumption.
A wire match alone cannot conceal a changed driver workflow. An identical
untriggered run is reported as `expected-untriggered-reproduced`, retaining the
original untriggered result rather than promoting it to a passed branch. Original bytes,
manifests, historical statuses and prior replay reports remain preserved.

The independently selected `--child-case` profile keeps its dedicated version-2
plan and native adapters. Its source, fixed file and role hashes remain bound. New child preparations also
freeze their own `case` scope: native child identity/read or post-root completion
is required, and missing evidence produces `untriggered`. Resource limitations
remain separate from missing scenario evidence. Historical child snapshots retain
their original reports; this profile is not promoted to the default v3 workflow. No child run is
required for catalog, offline verification or the v3 safety tests.

## Scoped case acceptance

Current scenario definitions use version 2; plan version 3 is unchanged. A US is
a requirements index. Each recording validates one case, and its status covers
only the frozen scope. `gaps` contains missing evidence required by that case;
`case.outOfScope` records other US branches. These exclusions never expand tool
permissions. Success does not need a generic full-US `partial` qualifier.

Case 009 is one denied write to `denied.txt`, rather than an allow/deny sequence.
Case 012 requests only model configuration before input. Case 013 records the
configuration response/input order; it does not claim effective thinking values.
Interrupted Claude case 016 requires an acknowledged interrupt and explicit
cancellation terminal reason when the native result has `is_error=true`.

Changed scopes and workflows require new preparations. Saved raw bytes, plans,
source snapshots and historical statuses are never rewritten. Replay compares
case scope, gaps, limitations and branch outcome alongside native receipts and
full Go-peer consumption.
