# Migrate the public Go package to types

The native data package is now `github.com/zoumo/codex-sdk-go/types`, matching
`claude-agent-sdk-go/types`. This is a breaking source and package identity change.
The old Go package is removed; there is no compatibility alias package.

Replace the import `github.com/zoumo/codex-sdk-go/protocol` with
`github.com/zoumo/codex-sdk-go/types`, then change qualified references such as
`protocol.ThreadStartParams` to `types.ThreadStartParams`. Helpers such as
`Some`, `Null`, `Optional` and `Ptr` also live in `types`.

```go
import (
    codex "github.com/zoumo/codex-sdk-go"
    "github.com/zoumo/codex-sdk-go/types"
)

var opts = codex.StdioOptions{
    Initialize: types.InitializeParams{
        ClientInfo: types.ClientInfo{Name: "my-app", Version: "1"},
    },
}
```

If both SDKs are imported in one file, use explicit aliases such as `codextypes`
and `claudetypes` to distinguish their `types` packages. An explicit alias
`protocol "github.com/zoumo/codex-sdk-go/types"` can reduce the initial caller edit;
it does not restore the old package identity.

For named native data, `reflect.Type.PkgPath()` now returns
`github.com/zoumo/codex-sdk-go/types` instead of the former `/protocol` path.
`reflect.Type.String()` and formatting that includes the declared package name
also use `types`. Update reflection registries, type-name snapshots and persisted
package-qualified identifiers that depended on the former name. Rebuild downstream
consumers; the JSON wire representation is unchanged.

The root `codex` package owns connections and execution. Direct subscription events and subscription data now live in `types`; see [subscription migration](../default-managed-migration.md).
This change preserves CLI 0.162.1's pinned schema, 399 generated types and 750
schema cases. Upstream protocol URLs, JSON tags, schema terminology and error
messages remain unchanged.

The checked-in schema and Go bindings now live under `types/`. The generator
remains `scripts/generate_protocol.py`, and protocol provenance remains under
`docs/protocol/`. Historical evidence retains its original commands, paths and
checksums at the recorded revisions; those old paths are not current imports.
The evidence index's `test_files` entries point to relocated current tests.

From the repository root, run `bash scripts/check_generated.sh` to check generation
and `go test ./examples/publicconsumer/...` to compile the offline consumer example.
