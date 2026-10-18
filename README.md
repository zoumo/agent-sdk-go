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
- [Architecture](docs/architecture.md)
- [Migration and source history](docs/migration.md)
- [Offline validation](docs/testing.md)
- [Licenses and native attribution](NOTICE)

Go **1.26.9** is pinned in the root `mise.toml`:

```sh
mise install
mise exec -- go mod download
mise exec -- bash scripts/check-offline.sh
```

All CI checks are offline. Historical recordings and source inspections have
separate provenance; they do not establish current live agent compatibility.
