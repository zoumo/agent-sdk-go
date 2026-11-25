# Native Pi client

[Open](process.go) starts one Pi process per session. [Attach](session.go) takes ownership of two closable streams. Both wait for `get_state` to return a session ID. Use a deadline for that handshake.

Read events with `Receive`. It delivers non-response records in wire order and preserves unknown fields. Each inbound JSONL record must fit within the 64 MiB reader limit. `Close` releases the streams and process while keeping received events available to drain. Use `CloseAndDiscard` to release unread events when you stop consuming them. An owned process gets stdin EOF and has two seconds to exit before the client kills it.

Command IDs correlate concurrent responses. The `Prompt` disposition records whether Pi started, queued, or handled the input. `agent_end` closes one low-level run. Use `agent_settled` to observe the end of automatic work after a run starts. `Abort` returns the native command response. The event stream still carries the run outcome. A `CommandError` preserves the failure response. A `DispatchError` means the outcome is unknown after write admission, which can happen if the waiting context is canceled. Check the session before deciding whether to submit again. [Native operations](commands.go)

Set `ResumeID` to the complete native ID. The launcher checks the ID and workspace in the existing session header, passes the full filename to Pi, and verifies the ID in `get_state`. Set `SessionDir` when Pi settings select custom storage, or give the exact `SessionFile`. Resume requires a persisted session file. [Resume lookup](resume.go)

## Read saved sessions

Set absolute paths for the workspace and the session directory in `HistoryScope`. Use `ListSessions` to discover IDs, `GetSessionInfo` to inspect a header, and `ReadSessionHistory` to read entries in file order. These calls read one directory without starting Pi or migrating older records. If a history page has more entries, pass its `NextOffset` to the next disk read. The live `get_entries` command uses native entry IDs as cursors. [Saved history API](history.go)

## Explicit extensions

Set `Options.ExtensionPaths` to load extensions explicitly with `-e`.
`NoExtensions` disables automatic discovery and leaves explicit paths available.
`Session.Commands` reports the commands, prompt templates, and skills Pi
currently exposes.

Decode an extension UI event with `Event.UIRequest`. Use its original dialog
ID with `Session.RespondUI`. Pi uses a one-way response, so a successful call
confirms the complete frame write. The next native events carry the outcome.

## MCP servers in the unified adapter

Pass the complete list of application-managed MCP servers in
`agent.OpenOptions.MCPServers`. Repeat the list on Resume. Pi 1.6.0 connects
to stdio and Streamable HTTP servers and exposes their tools directly to the
model. HTTP authentication can use literal headers, file-backed values, or
environment variables available to the Pi process.

Pi also reads its user MCP file and trusted project MCP file. A server from
either file takes precedence over a registration with the same name. Pi
treats hyphens and underscores as equivalent in server names. The adapter
rejects duplicate registrations under this rule.

Open and Resume confirm extension initialization. Pi connects MCP servers in
the background. Use Pi's MCP connection status or a successful tool result
to verify that a server is ready. [Pi MCP configuration](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/mcp.md)

## Protocol source

The bindings use the MIT-licensed Pi v1.6.0 protocol at Git commit `faac585884ae2c013a6b1400f3e9e77f10b34bac`. [Pi release](https://github.com/earendil-works/pi/releases/tag/v1.6.0), [MIT license](https://github.com/earendil-works/pi/blob/faac585884ae2c013a6b1400f3e9e77f10b34bac/LICENSE)

| Upstream file | SHA-256 |
| --- | --- |
| `packages/coding-agent/docs/rpc.md` | `54e29df00e080c962df4fbb516dc441a9ce6805287bea61d45f3c99f92078446` |
| `packages/coding-agent/docs/rpc-commands.md` | `2bf20a4e7690db5c3407948efe0b374b444df7707ff7b238e2e80c46025bb440` |
| `packages/coding-agent/docs/json.md` | `f103191816fdcf3627dfc98b89acad8a28eee737697130ddaeb2581131213543` |
| [Native RPC types](https://github.com/earendil-works/pi/blob/faac585884ae2c013a6b1400f3e9e77f10b34bac/packages/coding-agent/src/modes/rpc/rpc-types.ts) | `be06a1d53916e03e9274cce9e4833692b235494e4047f1f7139d7f0147daae1a` |
| [Official RpcClient](https://github.com/earendil-works/pi/blob/faac585884ae2c013a6b1400f3e9e77f10b34bac/packages/coding-agent/src/modes/rpc/rpc-client.ts) | `a34c9eb4937bf3b526128c9a2032cc3295504996a14bf204e4a17e5bb55dcf7f` |
| `packages/coding-agent/docs/session-format.md` | `4f8fc42179b332bcf71fe4c85cc95d914853ff7486d68742954a45316b512a4a` |

The tests in this package use synthetic peers.
