# agent-sdk-go

Native Go SDKs for Codex and Claude in one module and one release stream.

```go
import (
    "github.com/zoumo/agent-sdk-go/agent"
    codex "github.com/zoumo/agent-sdk-go/codex"
    codextypes "github.com/zoumo/agent-sdk-go/codex/types"
    claude "github.com/zoumo/agent-sdk-go/claude"
    claudetypes "github.com/zoumo/agent-sdk-go/claude/types"
)
```

Choose the native package for your agent. The optional `agent` package defines the
provider-neutral live Session contract above those native SDKs; it is not a root
client and does not replace their protocols or histories. Both native `types`
packages and the `agent` core depend only on the Go standard library. Provider
adapters are separate packages. The old module import paths have no compatibility
forwarding packages.

| Start here | Documentation |
|---|---|
| Use an SDK | [Codex](codex/README.md), [Claude](claude/README.md) |
| Understand the common contract | [Unified agent core](agent/doc.go) |
| Understand the design | [Harness](harness/index.md), [shared terms](GLOSSARY.md) |
| Test, record or inspect evidence | [Offline validation](docs/testing.md), [SDK simulation](tests/simulation/drivers/README.md), [external test tool](https://github.com/zoumo/agent-sdk-testify) |
| Migrate or release | [Migration](docs/migration.md), [release checks](docs/testing.md#release-preparation) |
| Check licensing | [Licenses and native attribution](NOTICE) |

Go **1.26** or newer is required. The root `mise.toml` is optional for local
tool management. Formatting and linting use the root Makefile:

```sh
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4
go mod download
make fmt
make lint
bash scripts/check-offline.sh
```

Native contract validation runs offline; real agent/model tests remain disabled.
External consumer CI retrieves the exact
pushed module and dependencies over the network, then compiles public imports,
examples and documentation snippets without invoking a real CLI or model.
Simulation catalog availability and source inspection do not establish successful
real recordings. Code integration tests use explicitly synthetic input.
The offline entry also requires an explicit separately built external tool and the
fixed reviewed corpus in its embedded mock catalog; absent recordings fail the
simulation gate. Material adoption and catalog maintenance belong to the
[external tool](https://github.com/zoumo/agent-sdk-testify).
