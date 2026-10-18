# Offline managed-tree example

From the repository root, with the pinned toolchain already installed:

```sh
GOPROXY=off GOSUMDB=off mise exec -- go run ./examples/observe-tree-offline
GOPROXY=off GOSUMDB=off mise exec -- go test ./examples/observe-tree-offline
```

The program starts a copy of **itself** as a synthetic stdio peer with an empty
environment. It never starts Codex, reads credentials, contacts a provider, or
runs a model. Only standard Go build tooling is used; dependencies must already
be cached when GOPROXY is off. `fixture/*` methods are private fixture controls,
not native Codex methods. Any unexpected RPC makes the peer fail.

[expected.txt](expected.txt) is the complete reproducible stdout. The test checks
it byte for byte through the public Client API and actual child-process transport.
The sequence shows root → child → grandchild, an early grandchild delta released
after association, an oversized unknown-source event explicitly delivered as
unresolved, local Close followed by a successful control call, and explicit
history snapshots yielding only Associated. History seeding performs no RPC.
The synthetic source IDs are examples, never IDs fabricated by the SDK.

The example prints a compact view. Applications should retain Native.Params,
Native.Data/DecodeError, Raw, provenance, epoch, parent, and original request
handles as described in the [compatibility matrix](../../docs/subagent-compatibility.md).
A successful fixture is E2 evidence only. Best-effort server auto-attach and
subagent runtime coverage remain unverified; closing observation does not stop
provider work. There is no automatic resume or seamless recovery.
