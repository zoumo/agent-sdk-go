# agent-sdk-go

Native Go SDKs for Codex and Claude in one module and one release stream.

```go
import (
    codex "github.com/zoumo/agent-sdk-go/codex"
    codextypes "github.com/zoumo/agent-sdk-go/codex/types"
    claude "github.com/zoumo/agent-sdk-go/claude"
    claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
)
```

Choose the native package for your agent. There is no root client or shared public
protocol. Both `types` packages depend only on the Go standard library. The old
module import paths have no compatibility forwarding packages.

- [Codex API, examples, protocol inventory and coverage](codex/README.md)
- [Claude API, examples, native types and coverage](claude/README.md)
- [Architecture](harness/overview.md)
- [SDK common design and onboarding](harness/sdk/index.md)
- [Engineering standards](harness/architecture.md) — [testing](harness/testing.md), [code layout](harness/code-layout.md)
- [Migration and source history](docs/migration.md)
- [Offline validation](docs/testing.md) and [pure public API consumer](tests/consumers/codex-public/main.go)
- [Offline mock CLIs and programmable protocol peers](docs/mock.md)
- [Recorded test data: Codex tools/questions, Claude authentication failure, and synthetic fixtures](docs/recordings.md)
- [Common SDK user stories](harness/sdk/user-stories.md) and [native acceptance evidence](docs/validation/stories/index.md)
- [Shared terms](harness/glossary.md)
- [Release preparation and consumer checks](docs/releasing.md)
- [Licenses and native attribution](NOTICE)

Go **1.26.9** is pinned in the root `mise.toml`:

```sh
mise install
mise exec -- go mod download
mise exec -- bash scripts/check-offline.sh
```

Native contract validation runs offline; real agent/model tests remain disabled.
External consumer CI retrieves the exact
pushed module and dependencies over the network, then compiles public imports,
examples and documentation snippets without invoking a real CLI or model.
Historical recordings and source inspections have separate provenance; they do
not establish current live agent compatibility.
