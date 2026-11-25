# Type sources and license review

The Go client uses the following sources for protocol types, wire semantics and
history queries. The SDK owns its codecs and synthetic fixtures. Adapted
history algorithms retain the upstream MIT notice. [Attribution](../NOTICE)

| Reference | Pinned version | License / use |
| --- | --- | --- |
| [Official Python SDK](https://github.com/anthropics/claude-agent-sdk-python/tree/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab) | `f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab`, CLI source target 2.1.302 | MIT, copyright 2025 Anthropic PBC. `types.py` message/content/system/result definitions reviewed; Python's flattened objects are not the stream-json layout. |
| [Official TypeScript npm package](https://www.npmjs.com/package/@anthropic-ai/claude-agent-sdk/v/0.3.302) | `@anthropic-ai/claude-agent-sdk` 0.3.302 | **Not MIT**: package LICENSE.md says © Anthropic PBC, all rights reserved, use subject to linked legal agreements. `sdk.d.ts` used only to cross-check wire envelopes, result variants and casing. No package contents redistributed. |
| [severity1 Go SDK](https://github.com/severity1/claude-agent-sdk-go/tree/d167fbef06759f127f5623bcf685c05b422de5df) | `d167fbef06759f127f5623bcf685c05b422de5df` | MIT, copyright 2025 John Reilly Pospos. Root aliases and `internal/shared/message.go` inspected for type organization only. |
| [Roasbeef Go SDK](https://github.com/Roasbeef/claude-agent-sdk-go/tree/a886b29cee2478f48251cfaab02142683ee43d6b) | `a886b29cee2478f48251cfaab02142683ee43d6b` | MIT, copyright 2026 Olaoluwa Osuntokun. `messages.go` inspected for nested wire/type organization only. |

Do not label the TypeScript package MIT or apply this repository's license to the
CLI/service. Source adaptations must retain their copyright and permission notice.

### Session startup permissions

Claude Code `2.1.293` reports the startup permission mode in
`initialize.response.current_permission_mode`. The read-only `get_settings`
control reports merged Bash sandbox configuration at `response.effective.sandbox`.
The Go adapter checks the effective values when opening or resuming a Session.
[Permission presets](../../../docs/adr/0005-session-permission-presets.md) ·
[Claude sandboxing](https://code.claude.com/docs/en/sandboxing)

Reproducible SHA256 of downloaded reference bytes:

| File | SHA256 |
| --- | --- |
| Python LICENSE | `cebdde8a8fb9ee59e5eaaed19578bf8085aa7047562259c31f38f225c26f6812` |
| TypeScript npm tarball 0.3.302 | `c7d7d61d6b0b7b7bb05cc053f229c5df88f151550ead0624c7393939d0d8e1f1` |
| TypeScript `package/sdk.d.ts` | `26f9a0c91ad64e2020f0d3734932302e3cc09685bddf2b2f9ef60839ce278773` |
| TypeScript `package/LICENSE.md` | `8ce94b9478bb9868f9641f818e06cd722fbe55d4c22e2d2ed11971b20146173a` |
| severity1 LICENSE | `48e373021742f33c8785c2f35837c5abccc3b3ea6f658a61e5f6337538323125` |
| Roasbeef LICENSE | `81fddab6c8190a8205c85e60270199e36296722829bf7e98be1f531e77a6071e` |

For `AskUserQuestion`, the native response uses full question text as keys. The
TypeScript 0.3.302 `sdk-tools.d.ts` declares string answer values. The Go client
joins multi-select answers with commas. The declaration file has SHA256
`25fa8caa18dacde752ebede11d55dccfc5f8c6ff011478cf92da49420e2690dc`.
[Answering permission and question requests](../README.md#actual-permission-and-question-requests)

Additional inspected type-organization file bytes (same pinned commits):

| File | SHA256 |
| --- | --- |
| severity1 `internal/shared/message.go` | `a4220bc169d58c9e500f308fda19598b9703a1671a37c5bd17634c4cfe46f216` |
| Roasbeef `messages.go` | `762f11f74d05dcb231084dced650876b2d04be999476ac34a61c9156d05d555c` |

## Context-only input

The TypeScript SDK's `SDKUserMessage.shouldQuery` field controls whether the
input starts an assistant turn. The unified Claude adapter uses `false` for
context-only input. The reference declaration in `@anthropic-ai/claude-agent-sdk`
0.3.258, `sdk.d.ts`, has SHA256
`b209ed293b63bb92bde59a49fe8a6e3b06f92afe0d1d4987c937a3cfea90b6dc`.
[Context injection contract](../../../agent/docs/session.md#context-inject)

## CLI 2.1.293 and TypeScript 0.3.293 relationship profile

The [official TypeScript SDK 0.3.293 archive](https://registry.npmjs.org/@anthropic-ai/claude-agent-sdk/-/claude-agent-sdk-0.3.293.tgz)
identifies Claude Code 2.1.293, revision `3abc54a9d60b4d12c627afad22d6e5f58a6199d2`.

| Fixed file | SHA256 |
| --- | --- |
| 0.3.293 archive | `395bfbda4294c6334fa18be3706a46ff31851b4f4a6f74ba11ac96c9d97ced9f` |
| sdk.d.ts | `bbfe75f8b5d29109d282d8b12d990f006744be11b53f86ce1b71c67824716254` |
| sdk-tools.d.ts | `d850d83ecd9e5f92be6e1d98b54b228e68ce0cc32767391f6e20573a4870e024` |
| manifest.json | `ce867df78920b885ffcd8ddeb5b12f867bbb34f8a7dd85d42740d48b4c6ef351` |

`SDKAssistantMessage.agent_id` (`sdk.d.ts` 3647–3651) and
`SDKUserMessage.agent_id` (6360–6364) explicitly name the subagent task_id,
unchanged across resume. `SDKPartialAssistantMessage` (5512 onward) does not
declare agent_id; its forwarded parent_tool_use_id remains the reviewed alias.
`SDKTaskStartedMessage.parent_task_id` (6115–6119) names the launching task,
can refer to foreground or ended parents, and instructs readers to treat
unknown parents as no parent. This SDK therefore requires an already confirmed
parent member and does not fabricate a main-session edge from absence.
Task run_id identifies a run, not a new child; this SDK retains arrival order
and does not add a current-run selection or event reordering mechanism.
Background task snapshots do not independently create members or parent edges.

The SDK applies these semantics across its [supported stable CLI versions](../README.md#compatibility).

The Claude recording for SDK-CASE-007 contains `checking` and `done` notifications
with `tool_use_id` for `system/permission_check_status`. Its manifest binds the
source recording and raw bytes. [Recording identity](../../../replay/data/claude/SDK-US-002/SDK-CASE-007/a2a64565e1bab3dffe9b6081/material.json)

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

## Standalone MCP elicitation

In the fixed [TypeScript SDK 0.3.293 archive](https://registry.npmjs.org/@anthropic-ai/claude-agent-sdk/-/claude-agent-sdk-0.3.293.tgz),
`package/core.d.ts` re-exports `package/sdk.d.ts`. The declarations for
`SDKControlElicitationRequest` and `SDKControlCancelRequest` start at lines
3916 and 3905 of `sdk.d.ts`. `ElicitationResult` at line 743 uses the MCP
`ElicitResult` type. The SDK's `onElicitation` example handles omitted mode as
form. In `package/sdk.mjs`, initialization sends no elicitation-specific
field. The control handler wraps the callback result in the success response
for the outer request ID. The cancellation handler uses that same ID to abort
the callback. The optional URL elicitation ID identifies the external flow.

See the [SDK acceptance cases](../../../docs/sdk/validation-cases.md) and
[recording index](../../../replay/data/index.json) for coverage.

## Local history adaptation

The local history reader adapts main-message compaction, parallel-tool recovery
and metadata preview selection from the MIT-licensed Python SDK 0.2.174,
revision `0e89b298e771259378a6cd3734e00d323dff5703`. Child queries use the
independent last-leaf child chain. The Go reader checks query scope, file limits,
integrity and changes during reads. [Upstream algorithm](https://github.com/anthropics/claude-agent-sdk-python/blob/0e89b298e771259378a6cd3734e00d323dff5703/src/claude_agent_sdk/_internal/sessions.py#L1367) ·
[Child-history contract](../../docs/history-query.md#children-claude-独立本地子历史) ·
[MIT notice](../NOTICE)
