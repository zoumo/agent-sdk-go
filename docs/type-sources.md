# Type sources and license review (tickets02–03)

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

## Ticket03 revalidation

The same pinned Python `types.py` control schemas and `_internal/query.py`
permission conversion were fetched again on 2026-10-18; byte hashes match the
compatibility baseline. The pinned TS `sdk.d.ts`, npm archive and LICENSE.md
were fetched again and match the hashes above. Python and both reference Go
licenses were independently fetched again and match the above hashes and
copyright holders. severity1 `internal/shared/message.go` and Roasbeef
`messages.go` were inspected for type organization only. No third-party lifecycle
code, generated schemas, package dependency or source text was copied.

The [official user-input documentation](https://code.claude.com/docs/en/agent-sdk/user-input)
was reviewed via web retrieval on 2026-10-18 (mutable page; direct shell fetch
returned HTTP 403, so no file-byte hash is claimed). It was used to check full
question-text answer keys and preservation of the question list. The SDK source
pins determine wire nesting/casing. Documentation examples are guidance rather
than proof that any installed CLI accepts this Go implementation. The new
fixtures are original synthetic data and do not invoke a model. The TypeScript
package remains **non-MIT**; its contents are not redistributed.

Additional inspected type-organization file bytes (same pinned commits):

| File | SHA256 |
| --- | --- |
| severity1 `internal/shared/message.go` | `a4220bc169d58c9e500f308fda19598b9703a1671a37c5bd17634c4cfe46f216` |
| Roasbeef `messages.go` | `762f11f74d05dcb231084dced650876b2d04be999476ac34a61c9156d05d555c` |
