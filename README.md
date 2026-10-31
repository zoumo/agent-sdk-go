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

| Start here | Documentation |
|---|---|
| Use an SDK | [Codex](codex/README.md), [Claude](claude/README.md) |
| Understand the design | [Harness](harness/index.md), [shared terms](GLOSSARY.md) |
| Test, record or inspect evidence | [Offline validation](docs/testing.md), [agent-sdk-testify](tools/agent-sdk-testify/README.md) |
| Migrate or release | [Migration](docs/migration.md), [release checks](docs/testing.md#release-preparation) |
| Check licensing | [Licenses and native attribution](NOTICE) |

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
Simulation catalog availability and source inspection do not establish successful
real recordings. Code integration tests use explicitly synthetic input.
