# Offline subagent observation example

From the native `codex/` directory, with the pinned toolchain already installed:

```sh
GOPROXY=off GOSUMDB=off mise exec -- go run ./examples/observe-subagents-offline
GOPROXY=off GOSUMDB=off mise exec -- go test ./examples/observe-subagents-offline
```

It opens one Session and consumes native Event and SDK observation values through
Session.Receive, with no second subscription or request reader.

The program starts a copy of **itself** as a synthetic stdio peer with an empty
environment. It never starts Codex, reads credentials, contacts a provider, or
runs a model. Only standard Go build tooling is used; dependencies must already
be cached when GOPROXY is off. `fixture/*` methods are private fixture controls,
not native Codex methods. Any unexpected RPC makes the peer fail.

[expected.txt](expected.txt) is the complete reproducible stdout. The test checks
it byte for byte through the public Client API and actual child-process transport.
The sequence shows root → child → grandchild membership and parent evidence in
native arrival order. The early grandchild delta and unrelated envelope stay in
bounded diagnostic quarantine; later membership does not backfill them. Session
Close releases the shared attachment references, and a separate raw control call
proves that the caller-owned connection remains usable. No child resume/read or
model execution is performed. The synthetic source IDs are peer-authored.

The compact view omits native payload details. Applications should retain
Received.Raw/Params/Source/Origin, native Event.Data/DecodeError, observation gaps
and the original real request handle. See the
[Session migration guide](../../../docs/session-migration.md).
A successful fixture is E2 evidence only. Best-effort server auto-attach and
subagent runtime coverage remain unverified; closing observation does not stop
provider work. There is no automatic resume or seamless recovery.
