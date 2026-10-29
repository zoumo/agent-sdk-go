# Type sources and license review

The 2026-10-18 review covered schema and type organization; these references are
source evidence, not runtime validation. Go codecs and synthetic fixtures are
original. Later local-history algorithms adapt the pinned MIT Python source noted
[below](#local-history-adaptation), with its attribution retained in [NOTICE](../NOTICE).

| Reference | Pinned version | License / use |
| --- | --- | --- |
| [Official Python SDK](https://github.com/anthropics/claude-agent-sdk-python/tree/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab) | `f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab`, CLI source target 2.1.302 | MIT, copyright 2025 Anthropic PBC. `types.py` message/content/system/result definitions reviewed; Python's flattened objects are not the stream-json layout. |
| [Official TypeScript npm package](https://www.npmjs.com/package/@anthropic-ai/claude-agent-sdk/v/0.3.302) | `@anthropic-ai/claude-agent-sdk` 0.3.302 | **Not MIT**: package LICENSE.md says © Anthropic PBC, all rights reserved, use subject to linked legal agreements. `sdk.d.ts` used only to cross-check wire envelopes, result variants and casing. No package contents redistributed. |
| [severity1 Go SDK](https://github.com/severity1/claude-agent-sdk-go/tree/d167fbef06759f127f5623bcf685c05b422de5df) | `d167fbef06759f127f5623bcf685c05b422de5df` | MIT, copyright 2025 John Reilly Pospos. Root aliases and `internal/shared/message.go` inspected for type organization only. |
| [Roasbeef Go SDK](https://github.com/Roasbeef/claude-agent-sdk-go/tree/a886b29cee2478f48251cfaab02142683ee43d6b) | `a886b29cee2478f48251cfaab02142683ee43d6b` | MIT, copyright 2026 Olaoluwa Osuntokun. `messages.go` inspected for nested wire/type organization only. |

Do not label the TypeScript package MIT or apply this repository's license to the
CLI/service. Source adaptations must retain their copyright and permission notice.

Reproducible SHA256 of downloaded reference bytes:

| File | SHA256 |
| --- | --- |
| Python LICENSE | `cebdde8a8fb9ee59e5eaaed19578bf8085aa7047562259c31f38f225c26f6812` |
| TypeScript npm tarball 0.3.302 | `c7d7d61d6b0b7b7bb05cc053f229c5df88f151550ead0624c7393939d0d8e1f1` |
| TypeScript `package/sdk.d.ts` | `26f9a0c91ad64e2020f0d3734932302e3cc09685bddf2b2f9ef60839ce278773` |
| TypeScript `package/LICENSE.md` | `8ce94b9478bb9868f9641f818e06cd722fbe55d4c22e2d2ed11971b20146173a` |
| severity1 LICENSE | `48e373021742f33c8785c2f35837c5abccc3b3ea6f658a61e5f6337538323125` |
| Roasbeef LICENSE | `81fddab6c8190a8205c85e60270199e36296722829bf7e98be1f531e77a6071e` |

The [official user-input documentation](https://code.claude.com/docs/en/agent-sdk/user-input)
was reviewed on 2026-10-18 (mutable page; no stable source-byte pin is claimed). It was used to check full
question-text answer keys and preservation of the question list. The SDK source
pins determine wire nesting/casing. Documentation examples are guidance rather
than proof that any installed CLI accepts this Go implementation. The
fixtures are original synthetic data and do not invoke a model. The TypeScript
package remains **non-MIT**; its contents are not redistributed.

Additional inspected type-organization file bytes (same pinned commits):

| File | SHA256 |
| --- | --- |
| severity1 `internal/shared/message.go` | `a4220bc169d58c9e500f308fda19598b9703a1671a37c5bd17634c4cfe46f216` |
| Roasbeef `messages.go` | `762f11f74d05dcb231084dced650876b2d04be999476ac34a61c9156d05d555c` |

## CLI 2.1.258 and TypeScript 0.3.258 baseline

Read-only inspection on 2026-10-18 identified installed CLI **2.1.258**,
Mach-O arm64, SHA256
`0d036f0b635d239844e6df6996bc05263fe305df505b073c71f79f83668ea35a`.
The [official SDK 0.3.258 archive](https://registry.npmjs.org/@anthropic-ai/claude-agent-sdk/-/claude-agent-sdk-0.3.258.tgz)
manifest names CLI 2.1.258, commit `b3cd543a1f6fcdf4d8fabc0f5e5538d2ee7f38e1`,
build date 2026-09-01. The installed binary hash differs from the manifest's
darwin-arm64 checksum; they are not asserted to be identical builds. A version
probe and static inspection do not establish live subagent behavior. Task/tool association
is gated to the exact native init version 2.1.258, not a minimum version range.

| Fixed file | SHA256 |
| --- | --- |
| 0.3.258 archive | `656cf237bc567cb172a007a0fd5b3958cf960d154c03ab390a755d2c3bdbb398` |
| sdk.d.ts | `b209ed293b63bb92bde59a49fe8a6e3b06f92afe0d1d4987c937a3cfea90b6dc` |
| sdk-tools.d.ts | `a8bb537bb1624e9e68d5aa7c620260027278a9f83ce81943906a9485b06d7c9d` |
| manifest.json | `ec53b2ce3d9f95d2c94c07aacf9202e6df9f79c695b71a1af2b4491081fba60f` |
| LICENSE.md | `8ce94b9478bb9868f9641f818e06cd722fbe55d4c22e2d2ed11971b20146173a` |

In these declarations, resumed local agents are backgrounded (`sdk.d.ts`
5167–5202); completed/async_launched Agent results contain agentId and exclude
remote_launched (`sdk-tools.d.ts` 103–205); AgentInput has no resume parameter
(658–710). Structured user `tool_use_result` differs from model-directed
`tool_result` text (`sdk.d.ts` 5267 onward). Current declarations do not expose
run_id, parent_task_id or message agent_id; newer-schema codecs are not a promise
that CLI 2.1.258 emits those fields. `sdk.d.ts` 1727–1732 describes forwarded
text/thinking, 5011–5019 idle after heldBackResult/background-loop completion,
and 5145–5165 task_progress metadata. Background command stdout is not promised
in real time, and the SDK does not read output files to fill gaps.

Static inspection of the hashed local binary corroborated task registration,
background agent identity, new/resumed registration, foreground registration
and async launch paths. No embedded code was executed for that inspection.
Both TypeScript versions have the same non-MIT licensing boundary. No TypeScript declaration
bundle, binary or implementation is redistributed.

## Python protocol source hashes

[Official source archive](https://github.com/anthropics/claude-agent-sdk-python/archive/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab.tar.gz), SHA256 `07a88363bb6bd3774e79dea95793d8bc14071a2f80dd61f0c457adf2eccb76cc`.

Source file hashes below refer to exact file bytes from that commit. Paths are relative to `src/claude_agent_sdk/`.

| Official source | SHA256 |
| --- | --- |
| [types.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/types.py) | `c06ccef3487b2f5ad81000a72a772b823760db1c32e85693eea57c8b16a93aa2` |
| [client.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/client.py) | `980f956d963338b39f4fd5224e69594eb513bfda37e5b215cf94bb0499ea047c` |
| [query.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/query.py) | `5528bc5bc57992f833f7a1056d1e2847eb71b070766764c84365c2e49a05162e` |
| [__init__.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/__init__.py) | `1582188cfc10ce1eb3892a6e7c6d5639cdb0cca5e9e6ef1a1f5ebf68b75a072b` |
| [_cli_version.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_cli_version.py) | `2fc8a1aad4b2bb7d598143a6edbe79229173c2a4be6f75acec2909f4bd4a6f4b` |
| [_internal/query.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/query.py) | `0562517de949f4431177f204fe84cda3265f495d0eb0a949b9ced96cc5bd8276` |
| [_internal/message_parser.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/message_parser.py) | `febb1aee19e47e03433d89cd4b1f8c5636950e206df77e4f4ca2738b0c900393` |
| [_internal/transport/subprocess_cli.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/_internal/transport/subprocess_cli.py) | `23c0b35acfcff5426255f1f17433e66fc7f8f6d5b8f339e93e085233c569e11c` |

## Python behavioral research references

These source hashes identify research references, not tests executed against Go.
The Go tests are original; no upstream test source or fixtures are redistributed.

| Pinned official test source | Behavior used | SHA256 |
| --- | --- | --- |
| [test_query.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_query.py) | initialization and control envelopes | `2c8ae92d78b46ff2bbd82c1afae4cf7c7ad239f7cfb5e74fe446586e0e7402a2` |
| [test_message_parser.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_message_parser.py) | typed content and message parsing | `99a6712c872eba46e260f8c6e439ef52158453f7326f5a8fba9c4dc79234adee` |
| [test_subprocess_buffering.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_subprocess_buffering.py) | chunk boundaries, Unicode, frame limits | `3bbec7d2893851542fe6107d2402915402f37b0c5d0d64dfdd10630e41d0fac7` |
| [test_close_cancellation.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_close_cancellation.py) | cleanup despite cancellation | `8f0d3266f6d01ada50d04cdea381c2e3988251f7fbdf9c4e17d26bee14689d9a` |
| [test_session_resume.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_session_resume.py) | native resume option semantics | `a19d5c86b447d1bd58b2e53b0d0f40fa43f332989f5f95cd945eb81b083469ce` |
| [test_streaming_client.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_streaming_client.py) | stream lifecycle and interrupt | `68627a1d9f279dbf35b93050fba0c410065d63fdc7e059e9c424774ba7d08383` |
| [test_transport.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_transport.py) | literal launch arguments and subprocess errors | `f5f795d75a3b8cecb6b0ec417cd727f1015c80765dbaead6b02f6a4c92772338` |
| [test_tool_callbacks.py](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/tests/test_tool_callbacks.py) | original-ID allow/deny and updated input | `13fbe491655501a4026723be3f74c90d7007dcf6143fa2d9c7bce7ee5a47db74` |

## Session source evidence

Static inspection on 2026-10-25 used TypeScript SDK 0.3.258 / CLI 2.1.258 and
Python commit `f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab` (CLI source target 2.1.302).
The newer Python source does not establish behavior of the older CLI. These
records preserve source evidence, not a new execution or interoperability pass.
The TypeScript package remains non-MIT; no TypeScript implementation is bundled.

| Additional inspected bytes | SHA256 |
|---|---|
| T `package/sdk.mjs` | `4d9286bd9ca8f802e27c9be2cfa2e0769502dfabb693a6e3d16b62e4fbe3e69a` |
| T `package/package.json` | `a8fb007bc424b11eca6a4f046f5e6bdf078c9f032a4c6f133f9bc5297e686a14` |
| P `_internal/sessions.py` | `d5323963daf711c8bcc684057fd48d0cb786ee04d8329be6dec82444ec505662` |

In the fixed `sdk.mjs`, UTF-8 decoded character offsets locate local launch
(910611), control correlation (942303), `set_model` (938940),
`set_max_thinking_tokens` (939022), recursive child discovery (1108436), and
sidecar parsing (1110695). Verify the file hash before using these offsets.
Initialize acknowledgment and a requested ResumeID do not establish observed
local recovery identity; source inspection found no confirmed no-input identity
handshake. Structured input, configuration acknowledgment and stored discovery
retain their separate evidence limits in the corresponding API guides.

Independent child-history selection was checked against Python SDK 0.2.174,
commit [`0e89b298e771259378a6cd3734e00d323dff5703`](https://github.com/anthropics/claude-agent-sdk-python/tree/0e89b298e771259378a6cd3734e00d323dff5703).
Its [sessions.py](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py#L1367)
contains recursive layout resolution (1367–1476), the independent last-leaf child
chain (1474–1511), list/get (1514–1640), and sidecar/message projection (1643–1740).
The main-chain compaction and parallel-tool recovery at 933–1261 do not apply to
child chains. Go adds explicit scope, resource and integrity checks described in
the [child-history contract](../../harness/contracts/history-query.md#children-claude-独立本地子历史);
this query-source pin does not upgrade the live CLI baseline or prove real child files.

## Local history adaptation

Main-message compaction/parallel-tool recovery and metadata preview selection also
use Python SDK 0.2.174, commit 0e89b298e771259378a6cd3734e00d323dff5703.
The adapted algorithms retain MIT attribution in [NOTICE](../NOTICE).
Official npm 0.3.310 declarations/runtime were inspected separately; the archive
SHA-512 was fo+sr+9Ks73P001f6XKRQlyzfIBAJMgXArLUkH5Tu98NikRDZeGtFQVj23d5/hj2ijpFgzffrr4fHywY9qUkoQ==.
Go's explicit scope, bounded reads, strict corruption errors and change detection
are deliberate differences; no official SDK executes inside the query backend.
