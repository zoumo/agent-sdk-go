# Ticket02 type sources and license review

Reviewed 2026-10-18 UTC. These are schema/organization references, not runtime
validation. The Go codecs and synthetic fixtures are original implementation;
no third-party implementation, test fixture or lifecycle code is copied, adapted,
vended or added as a dependency. Repository code remains Apache-2.0.

| Reference | Pinned version | License / use |
| --- | --- | --- |
| [Official Python SDK](https://github.com/anthropics/claude-agent-sdk-python/tree/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab) | `f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab`, CLI source target 2.1.302 | MIT, copyright 2025 Anthropic PBC. `types.py` message/content/system/result definitions reviewed; Python's flattened objects are not the stream-json layout. |
| [Official TypeScript npm package](https://www.npmjs.com/package/@anthropic-ai/claude-agent-sdk/v/0.3.302) | `@anthropic-ai/claude-agent-sdk` 0.3.302 | **Not MIT**: package LICENSE.md says © Anthropic PBC, all rights reserved, use subject to linked legal agreements. `sdk.d.ts` used only to cross-check wire envelopes, result variants and casing. No package contents redistributed. |
| [severity1 Go SDK](https://github.com/severity1/claude-agent-sdk-go/tree/d167fbef06759f127f5623bcf685c05b422de5df) | `d167fbef06759f127f5623bcf685c05b422de5df` | MIT, copyright 2025 John Reilly Pospos. Root aliases and `internal/shared/message.go` inspected for type organization only. |
| [Roasbeef Go SDK](https://github.com/Roasbeef/claude-agent-sdk-go/tree/a886b29cee2478f48251cfaab02142683ee43d6b) | `a886b29cee2478f48251cfaab02142683ee43d6b` | MIT, copyright 2026 Olaoluwa Osuntokun. `messages.go` inspected for nested wire/type organization only. |

No MIT code or substantial source text is included, so this ticket adds no copied
source license header. If later tickets copy/adapt MIT source, retain the actual
copyright and full permission notice alongside that material. Do not label the
TypeScript package MIT or apply this repository's license to the CLI/service.
`NOTICE` records research provenance; the original official Python source-file
hashes remain in [compatibility.md](compatibility.md).

Reproducible SHA256 of downloaded reference bytes:

| File | SHA256 |
| --- | --- |
| Python LICENSE | `cebdde8a8fb9ee59e5eaaed19578bf8085aa7047562259c31f38f225c26f6812` |
| TypeScript npm tarball 0.3.302 | `c7d7d61d6b0b7b7bb05cc053f229c5df88f151550ead0624c7393939d0d8e1f1` |
| TypeScript `package/sdk.d.ts` | `26f9a0c91ad64e2020f0d3734932302e3cc09685bddf2b2f9ef60839ce278773` |
| TypeScript `package/LICENSE.md` | `8ce94b9478bb9868f9641f818e06cd722fbe55d4c22e2d2ed11971b20146173a` |
| severity1 LICENSE | `48e373021742f33c8785c2f35837c5abccc3b3ea6f658a61e5f6337538323125` |
| Roasbeef LICENSE | `81fddab6c8190a8205c85e60270199e36296722829bf7e98be1f531e77a6071e` |

The first implementation was made from clean main after ticket01; the unavailable
WIP and its fixtures were not used. Mock fixtures assert this documented subset
and do not establish parity with every field in either official SDK.
