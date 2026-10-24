# Compatibility: Go versus the official Claude Agent SDK

This Go SDK implements a small local-process client. **It is not an API-compatible
replacement for the official TypeScript SDK. No CLI version is fully live-validated.**

The new Session request path is documented in [Session requests](session-requests.md).
Its approvals use Receive and support permission updates and deny interruption;
the callback/default-denial and unsupported-update entries below describe the
temporary legacy Client path before that migration. Both paths now use the runtime
`claude.PermissionAnswer`; Session rejects unsupported answer options before
dispatch and retains a still-live handle on NotSent. Offline coverage is not live
CLI validation.

The largest gaps are:

- **Input control:** `Send(ctx, text)` accepts text only. There is no public
  `priority` (`now`/`next`/`later`), caller `uuid`, `shouldQuery`, content-block send,
  or typed steer/queue/append-without-query API. Serialized pipe writes do not
  select when the CLI executes a message.
- **Configuration:** Go exposes path, working directory, model, resume ID,
  environment, local timeouts/limits and a permission handler. Official tool
  selection, prompts, budgets, thinking/effort, permission modes, sandbox,
  settings sources, agents, skills, plugins and session-store options are absent.
- **Runtime controls:** apart from interrupt and close, official `Query` controls
  and queries have no Go equivalent: model/settings changes, task stop/backgrounding, context/usage queries, file rewind/read state and MCP management.
- **Callbacks:** Go can answer actual `can_use_tool` requests. Hook execution,
  SDK-hosted MCP, MCP elicitation and request_user_dialog have no response API.
  Permission rule updates and deny-with-interrupt are also absent.
- **Sessions:** native resume is available; listing, loading transcripts, fork,
  rename/tag/delete, storage adapters and restored pending work are absent.
- **Observation:** many messages have typed views, others only Raw. Automatic
  child association is an SDK convenience, not official agent configuration or
  verified complete child forwarding. Reconnect/replay are not implemented.

## Versions, sources and status meanings

| Reference | Scope |
| --- | --- |
| **T: TypeScript SDK 0.3.258** | Primary comparison: [official versioned declarations](https://unpkg.com/@anthropic-ai/claude-agent-sdk@0.3.258/sdk.d.ts), [npm archive](https://registry.npmjs.org/@anthropic-ai/claude-agent-sdk/-/claude-agent-sdk-0.3.258.tgz). Its manifest names **CLI 2.1.258**. Inventories below cover its 28 Query methods, 65 Options fields, 26 runtime exports, 13 user-message fields and 39 output union members. |
| **P: Python f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab** | Separate [client source](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/client.py) and [types](https://github.com/anthropics/claude-agent-sdk-python/blob/f93e29cc951cd5fddcaaed9f3c49c3c38dedd6ab/src/claude_agent_sdk/types.py), targeting **CLI 2.1.302**. It is not matching-version TypeScript parity. |
| **Newer schema reference: TS 0.3.302** | Some optional Go task/run/lineage fields were informed by these declarations. Their codecs do not prove CLI 2.1.258 emits those fields. |
| **Go implementation** | [Client/Options](../client.go), [permissions](../permissions.go), [native types/decoder](../types/events.go), [JSON codecs](../types/json.go), [derived events](../derived_events.go). Go **1.26.9**, standard library only. |

**Supported** means the specified narrow Go operation exists. **Partial** means
only the listed subset exists. **Unsupported** means no public implementation.
These statuses do not imply live verification. **Offline** identifies original
synthetic/replay tests; **live** describes only bounded runtime observations.
A dash means no implemented feature to test, not an offline pass. Raw reception,
JSON construction and inherited CLI configuration never count as a response,
control or configuration API. There is no public generic `Call`/raw-write escape
hatch. Pins, byte hashes and license boundaries are in [type sources](type-sources.md).

The comparison inventories the **main package**; separate browser, bridge and
extract subpaths are listed below. Declared private wire operations are kept
separate from public Query methods.

Declaration locations in T `sdk.d.ts`: `Options` 1396–2255, `Query`
2582–2911, `SDKUserMessage` 5267–5316, `SDKMessage` union 4603,
`SDKControlRequestInner` 4280, `HookEvent`/`HookInput` 854–875,
`AgentDefinition` 38–101. The inventory is pinned to these bytes, not a claim
about future releases.

## Core capability matrix

Source symbols in the last column refer to T unless marked P. Each inventory
below expands this matrix so omitted configuration and read-only support are visible.

| Official capability | Go status and entrypoint | Exact gap or limit | Offline verification | Live verification | Official source |
| --- | --- | --- | --- | --- | --- |
| Start an interactive local session | Supported: `Open` | One owned stdio process; no official query wrapper, custom process/transport or remote attachment | [client tests](../client_test.go) | Bounded initialization/reply observations only | `query`, `Options` |
| Send text / stream multi-turn input | Partial: `Client.Send` | Complete local write only; text, root parent and empty session field are fixed; no structured/multimodal send | [client tests](../client_test.go) | Ordinary text observed | `query`, `Query.streamInput`, `SDKUserMessage` |
| Schedule/identify/append input | Unsupported | No `priority`, caller `uuid`, `shouldQuery:false`, typed steer or queue operation; CLI behavior is not selected or acknowledged by Go | No scheduler contract tests | Unverified | `SDKUserMessage` |
| Consume messages and partial output | Partial: `Receive`, `ParseEvent` | One consumer; typed subset plus Raw; no dedicated iterator-per-turn helper; matching control replies consumed internally | [receive tests](../receive_test.go), [codec tests](../types/json_test.go) | Bounded 2.1.258 frames; no full message-union validation | `Query` async generator, `SDKMessage` |
| Resume native history | Partial: `Options.ResumeID`, `SessionID`, `RequestedResumeID` | Fresh process only; native persistence must exist; no fork/truncation/history queries or automatic resend | [resume tests](../resume_interrupt_test.go) | One retained-history continuation observed | `Options.resume` |
| Interrupt active work | Supported: `Interrupt` | Correlated acknowledgment only; discards `still_queued` receipt, cannot select `cancel_queued`; no rollback/task-terminal guarantee | [interrupt tests](../resume_interrupt_test.go) | Pending-question cancellation observed; active-tool rollback unverified | `Query.interrupt` |
| Per-task stop and backgrounding | Unsupported | Native task/status reception does not implement `stopTask` or foreground-task backgrounding via `backgroundTasks` | Decoder/observer only | Unverified | `Query.stopTask`, `Query.backgroundTasks` |
| Tool approval and questions | Partial: `PermissionHandler`, original `PermissionRequest.Answer`, `AskUserQuestionInput.WithAnswers` | Real `can_use_tool` only; no handler means deny; no rule persistence, deny interrupt, dialog or elicitation response | [permission tests](../permissions_test.go), [question tests](../question_interaction_test.go) | Allow/update/deny, delayed question and late-answer rejection observed with limits below | `CanUseTool`, `PermissionResult`, `Options` callbacks |
| Set tool access/sandbox/policy | Unsupported | Permission callback sees only requests the CLI actually prompts for; it is not a sandbox or gate on auto-approved tools | Default-deny path only | Policy parity unverified | `Options.tools`, `permissionMode`, `sandbox`, settings |
| Agent definition and task execution control | Unsupported | No `agent`, `agents`, task budgets, per-task stop advertisement or delegation configuration | No configuration path | Unverified | `Options.agent`, `agents`, `taskBudget`, `perTaskStopAffordance` |
| Child output / relationship observation | Partial: automatic forwarding, `Options.Subagents`, derived notifications | Conservative association/creation only; no child process attach; missing output cannot be recovered; exact 2.1.258 creation gate | [subagent matrix](subagent-compatibility.md) | All subagent capabilities unverified | `Options.forwardSubagentText`, task/message types |
| Session idle and background snapshots | Partial: `Event.System.State` / `Tasks` | Emission may require CLI opt-in; snapshots replace sets, not terminal edges; root result does not mean all children finished | [terminal tests](../tree_terminal_test.go) | Successful/background paths unverified | Session-state/background message variants |
| Hooks / custom tools / SDK-hosted MCP | Unsupported | Events may decode, but no callback registration, MCP server/tool host or success reply to these requests | Unsupported-control errors tested | Unverified | `hooks`, `HookCallback`, `tool`, `createSdkMcpServer` |
| MCP connect/reconfigure/status | Unsupported | Init MCP metadata is read-only; inherited CLI MCP configuration is not Go management support | No management path | Unverified | `mcpServers`, Query MCP methods |
| Runtime settings/model/thinking changes | Unsupported | `Options.Model` is launch-only; no corresponding setters or refresh | No control path | Unverified | Query settings/model methods |
| Budgets / structured output / thinking policy | Unsupported configuration; partial output data | Usage/structured output/raw thinking can be read if emitted; no budget enforcement, schema request or effort setting | [codec tests](../types/json_test.go) | No policy coverage | `Options`, result/content types |
| File checkpoint/rewind and native read helpers | Unsupported | Received tool blocks are not `readFile`, `rewindFiles` or `seedReadState` APIs | No control path | Unverified | Query file methods, `enableFileCheckpointing` |
| Session discovery/storage administration | Unsupported | Observed IDs and verified association seeds do not list/load/fork/store/delete sessions | Resume/history-seed tests only | No administration coverage | Session helper exports, `sessionStore` |
| Account/model/command/agent/context/usage discovery | Unsupported | System-init metadata and result usage are read-only subsets; initialization response is not publicly returned | Consumed response correlation tested | No discovery coverage | Query discovery methods |
| Cancel waits, close and reap owned process | Supported: contexts, `Close`, `Done`, `Err` | Receive cancellation only cancels its wait; callbacks ignoring context can outlive Close; platform limits below | [lifecycle tests](../lifecycle_unix_test.go), [overload tests](../overload_test.go) | No complete live lifecycle certification | `Query.close`, `Options.abortController`; P transport lifecycle |
| Errors and diagnostics | Partial: `Error`, `ControlError`, `PermissionError`, `ResourceLimitError` | Go classifications differ from official types; no automatic retry; result `is_error` must be inspected separately from Go transport errors | [client tests](../client_test.go), [replay](../recorded_replay_test.go) | Authentication failure recorded; other cases bounded/unverified | `AbortError`, result/error variants |
| Reconnect / durable delivery | Unsupported | No reconnect/reinitialize, cursor, pending-request redelivery or cross-epoch exactly-once; ResumeID is not transport reconnection | Failure/no-retry paths tested | Unverified | T `Query.reinitialize`, `Transport` interface and browser transport; P custom `Transport` is separate |

## All Query methods (28)

`Query` is an async generator of official messages. Go instead uses `Open`,
`Send`, `Receive`, `Close`; API shapes and lifecycle semantics differ.

| Official method(s) | Go status / entrypoint | Missing behavior |
| --- | --- | --- |
| `interrupt` | Supported: `Interrupt` | Acknowledgment only; no official queued-message receipt or rollback guarantee |
| `close` | Supported: `Close` | Go returns cleanup errors and owns process reaping |
| `streamInput` | Partial: repeated `Send` | No full `SDKUserMessage` async iterable or send controls |
| `setPermissionMode`, `setMcpPermissionModeOverride` | Unsupported | No dynamic global/per-server permission policy |
| `setModel`, `setMaxThinkingTokens` | Unsupported | Model only at Open; no runtime thinking setter |
| `applyFlagSettings`, `updateSettings`, `reinitialize` | Unsupported | No live flag/settings update or reinitialization |
| `initializationResult`, `supportedCommands`, `supportedModels`, `supportedAgents`, `accountInfo` | Unsupported | No getters for initialization response or inventories; system events are only partial metadata |
| `mcpServerStatus`, `reconnectMcpServer`, `toggleMcpServer`, `setMcpServers` | Unsupported | No MCP status/reconnect/toggle/replacement API |
| `getContextUsage`, `usage_EXPERIMENTAL_MAY_CHANGE_DO_NOT_RELY_ON_THIS_API_YET` | Unsupported | Result Usage is not a context/account usage query |
| `readFile`, `rewindFiles`, `seedReadState` | Unsupported | No native file-cache/checkpoint control |
| `reloadPlugins`, `reloadSkills` | Unsupported | No refresh operation |
| `stopTask`, `backgroundTasks` | Unsupported | No targeted stop or foreground-task backgrounding operation |

Source: T `Query` declaration. Supported rows use the core matrix's offline/live
boundaries; every unsupported method has no implementation verification.

## All Options fields (65)

The following lists every field in T `Options`; grouped names share the same Go
status and gap. **No unlisted CLI setting is implicitly exposed through Env.**
CLI-inherited settings may still affect execution, outside Go's typed API.

| Official field(s) | Go status / actual configuration | Exact difference |
| --- | --- | --- |
| `cwd`, `env`, `model`, `resume`, `pathToClaudeCodeExecutable` | Supported scoped mapping: `WorkDir`, `Env`, `Model`, `ResumeID`, `CLIPath` | Literal startup fields; non-nil Env replaces environment; no CLI download/version negotiation |
| `canUseTool` | Partial: `PermissionHandler` | Deferred single-use original handle; answer return value does not respond; response subset below |
| `abortController` | Partial: caller contexts and `Close` | Startup context does not own session lifetime; Receive context cancels wait only |
| `includePartialMessages`, `forwardSubagentText`, `permissionPromptToolName` | Partial: fixed startup behavior | Always partial messages, forwarding true, permission prompt tool stdio; no caller selection |
| `stderr` | Partial: bounded `Error.Stderr` | No streaming stderr callback |
| `executable`, `executableArgs`, `extraArgs`, `spawnClaudeCodeProcess` | Unsupported | No runtime/argv extension or process-factory injection; CLIPath selects executable only |
| `enableFileCheckpointing` | Unsupported | No checkpoint opt-in; no rewind operation |
| `additionalDirectories`, `tools`, `allowedTools`, `disallowedTools`, `toolAliases`, `toolConfig` | Unsupported | No typed directory/tool list, aliases or tool policy/config |
| `agent`, `agents`, `taskBudget`, `perTaskStopAffordance`, `agentProgressSummaries` | Unsupported | Observation does not register agents, budgets, affordances or summaries |
| `continue`, `forkSession`, `sessionId`, `resumeSessionAt`, `resumeDropsTurn`, `persistSession`, `sessionStore`, `sessionStoreFlush`, `loadTimeoutMs` | Unsupported | Resume ID only; no continue/fork/chosen-ID/truncation/persistence adapter/load policy |
| `hooks`, `includeHookEvents` | Unsupported configuration | Hook frames may remain Raw; no hook callbacks or emission choice |
| `onElicitation`, `onUserDialog`, `supportedDialogKinds` | Unsupported | No callback, renderer capability advertisement or response |
| `mcpServers`, `strictMcpConfig` | Unsupported | No MCP configuration/strictness/hosting |
| `thinking`, `effort`, `maxThinkingTokens`, `fallbackModel`, `maxTurns`, `maxBudgetUsd`, `outputFormat`, `betas` | Unsupported | No generation policy, fallback, budget, output-schema or beta configuration |
| `permissionMode`, `planModeInstructions`, `allowDangerouslySkipPermissions`, `sandbox` | Unsupported | No typed permission/plan/bypass/sandbox selection; callback is not confinement |
| `settings`, `managedSettings`, `settingSources`, `systemPrompt`, `skills`, `plugins` | Unsupported | No typed settings, prompt, skill or plugin loading/configuration |
| `promptSuggestions`, `title`, `debug`, `debugFile` | Unsupported | No suggestion/title/debug controls |

Official defaults differ from Go's fixed startup: T's forwarding is off unless
requested; Go always sends true. T offers optional partial messages; Go always
requests them. Go omits `perTaskStopAffordance`: the pinned CLI contract says an
interrupt then also kills background tasks, rather than advertising a host
per-task stop UI. This is source-described behavior, **not live-verified Go
background cancellation**. Go cannot opt into the alternate affordance.

Source: T `Options`; Go [Options/defaults](../client.go), [data-only Options](../types/options.go)
and [conversion](../options.go). Startup mappings and fixed argv are offline-tested
in [client tests](../client_test.go). Live verification is limited to the listed
2.1.258 flows; it does not validate each option. `Subagents`, `Limits` and local
timeouts are Go-specific bounds, not missing official configuration equivalents.
`Options.Types()` omits callbacks and Subagents; Env retains normal slice sharing.

### Agent definitions and tool configuration

All 16 fields of T `AgentDefinition` are **unsupported Go configuration**:
`description`, `tools`, `disallowedTools`, `prompt`, `model`, `mcpServers`,
`criticalSystemReminder_EXPERIMENTAL`, `skills`, `initialPrompt`, `maxTurns`,
`background`, `memory`, `effort`, `permissionMode`, `observer`, `observerMessage`.
`Options.Subagents` is bounded observation/history configuration, not a substitute
for an agent definition. Native Agent/Task tool input/output can be inspected,
but Go has no public direct-spawn or tool-definition registration API. Official
MCP config supports stdio/SSE/HTTP and SDK-hosted servers; none is configured or
hosted through this Go API. These unsupported paths have no offline/live
implementation verification.

## All user-message input fields (13)

T `SDKUserMessage` has both input and emitted-user-message uses. A decoder field
must not be mistaken for something `Send` can supply.

| Official field(s) | Go send support | Go receive/codec support |
| --- | --- | --- |
| `type`, `message` | Fixed user type/role and caller text string only | Typed `Event.Type`, `Event.User`; content text/blocks; unknown blocks Raw |
| `parent_tool_use_id`, `session_id` | Fixed null parent and empty session string | Typed native envelope IDs; no inferred parent added |
| `priority` | Unsupported | Raw only |
| `uuid` | Unsupported caller UUID | Typed `Event.UUID`; receive identity is not send idempotency |
| `shouldQuery` | Unsupported | Raw only; no append-without-query send |
| `origin` | Unsupported send field | Typed `Event.Origin` native object |
| `isSynthetic`, `tool_use_result` | Unsupported send fields | `Event.IsSynthetic`, `ToolUseResult`; extra fields retained Raw |
| `timestamp`, `subagent_type`, `task_description` | Unsupported send fields | Timestamp/task description Raw; `Event.SubagentType` typed |

The 13 input fields exclude the additional replay-variant field `isReplay`,
which Go decodes as `Event.IsReplay`.
Go marshaling a standalone data value does not provide an arbitrary-message send
operation. Assistant content, tool outputs and received metadata cannot create
permission-answer authority.

T declares `priority` values but does not specify their scheduling/default rules
in `SDKUserMessage`. Go omits `priority` entirely: it neither selects now/next/later
nor promises a default steer/queue policy. T documents `shouldQuery:false` as
appending without an assistant turn and merging into the next querying message;
Go cannot request that behavior. A completed Send means bytes written, not CLI
acceptance, scheduled execution, UUID deduplication or exactly-once delivery.

## Output message variants and decoding

All **39 members** of T `SDKMessage` are accounted for below. **Partial** means
selected typed fields plus Raw; **Raw only** is unsupported dedicated decoding,
not full feature support. Unknown valid fields/types survive on received native
`Event.Raw`. Native `Event` has no SDK relationship wrapper or invented metadata.

| Official variant(s) | Go decoder / status | Response or configuration boundary |
| --- | --- | --- |
| `SDKAssistantMessage`, `SDKUserMessage`, `SDKUserMessageReplay` | Partial: `Event.Assistant` / `User`, content, native envelope | No arbitrary content/replay send API |
| `SDKResultMessage` | Partial: `Event.Result`, error/status, usage/modelUsage, structured output | No schema/budget request or inferred all-done; inspect `IsError` |
| `SDKSystemMessage` | Partial: `Event.System` init/session/tool/model metadata | No initialization-response getter or matching configuration APIs |
| `SDKPartialAssistantMessage` | Partial: `Event.Stream`, common delta, raw inner event | Always requested; cannot toggle partial messages |
| `SDKCompactBoundaryMessage` | Partial: `Event.System.CompactMetadata` Raw | No compaction policy/control API |
| `SDKTaskStartedMessage`, `SDKTaskUpdatedMessage`, `SDKTaskProgressMessage`, `SDKTaskNotificationMessage` | Partial: system task IDs/status/patch/progress/usage; selected newer fields | No start/stop/task-budget API; optional newer fields are not current-version guarantees |
| `SDKSessionStateChangedMessage`, `SDKBackgroundTasksChangedMessage` | Partial: `Event.System.State`, raw task entries | No emission opt-in setter or task-backgrounding control; Env can carry CLI-specific opt-in |
| `SDKRateLimitEvent`, `SDKConversationResetMessage` | Partial: `Event.RateLimit`, `Event.Reset` | No account usage query, retry policy or reset operation |
| `SDKStatusMessage` | Partial: `Event.System.Status` and Raw | No status-setting or compaction control |
| `SDKAPIRetryMessage`, `SDKControlRequestProgressMessage`, `SDKModelRefusalFallbackMessage`, `SDKModelRefusalNoFallbackMessage`, `SDKLocalCommandOutputMessage` | Raw-only dedicated payloads | Seeing retries/refusals/commands does not expose policy or command execution APIs |
| `SDKHookStartedMessage`, `SDKHookProgressMessage`, `SDKHookResponseMessage`, `SDKPluginInstallMessage` | Raw-only dedicated payloads | No hook callbacks/plugin installation API |
| `SDKToolProgressMessage`, `SDKAuthStatusMessage`, `SDKThinkingTokensMessage`, `SDKWorkerShuttingDownMessage`, `SDKCommandsChangedMessage`, `SDKNotificationMessage` | Raw-only dedicated payloads | No auth, thinking setter, worker or command administration |
| `SDKFilesPersistedEvent`, `SDKToolUseSummaryMessage`, `SDKMemoryRecallMessage`, `SDKElicitationCompleteMessage`, `SDKPermissionDeniedMessage`, `SDKPromptSuggestionMessage`, `SDKMirrorErrorMessage`, `SDKInformationalMessage` | Raw-only dedicated payloads | No corresponding persistence, memory, elicitation, suggestions or mirroring APIs |

Source: T `SDKMessage` union and member declarations; Go [decoder](../types/events.go),
[extended views](../types/messages.go) and [JSON contract](message-json.md).
Text, thinking, redacted thinking, tool use/result and server-tool blocks have
selected typed views; images/documents and other blocks remain Raw. Server-tool
content, usage and patches are partly opaque. Codecs are handwritten, with no
automatic upstream synchronization. [Codec tests](../types/json_test.go) and
[receive tests](../receive_test.go) verify the implemented subset offline;
unrecognized variants are not separately live-validated.

## Callbacks and control responses

| Official callback/control surface | Go status / entrypoint | Missing response capability and verification |
| --- | --- | --- |
| `CanUseTool`: tool name, input, AbortSignal and prompt metadata | Partial: `PermissionHandler`, `RequestID`, `ToolUseID`, `ToolName`, `Input`, `Metadata`, `Raw`, `Context`, `Epoch` | Original IDs/input are immutable; metadata is an owned snapshot; bounded pool/default deny; offline and bounded live approval coverage |
| `PermissionResult` allow `updatedInput` | Supported subset: `PermissionAnswer{Behavior:PermissionAllow, UpdatedInput:...}` | nil preserves input; explicit object replaces it; single reserved attempt; offline and bounded live replacement coverage |
| `PermissionResult` deny `message` | Supported subset: `PermissionDeny`, `Message` | No additional interruption requested; offline and bounded live denial coverage |
| `updatedPermissions`, `toolUseID`, `decisionClassification`, deny `interrupt` | Unsupported runtime answer fields | Metadata/unknown JSON retention does not apply permission rules or add decision controls |
| AskUserQuestion via can_use_tool | Partial: typed input/questions/options/answers, `WithAnswers`, original `Answer` | Host collects answers; full-question-text keys; assistant tool_use alone has no responder; offline and bounded live coverage |
| `HookCallback`, `HookInput`, `HookJSONOutput` | Unsupported | No hook matcher registration or callback output; `initialize.hooks` is null |
| `onElicitation`, `ElicitationResult` | Unsupported | No form/URL elicitation callback, decision or submitted content response |
| `onUserDialog`, supported dialog kinds, `UserDialogResult` | Unsupported | No renderer/settlement; Go does not advertise kinds |
| `tool` / `createSdkMcpServer`, MCP JSON-RPC | Unsupported | No hosted server or MCP request handler; no successful `mcp_message` response |
| `stderr`, `spawnClaudeCodeProcess` callbacks | Unsupported callback contracts | Bounded diagnostic tail and fixed process ownership only |
| `SDKControlResponse` success/error, cancellation | Partial data codec: `Event.Response`, `ControlResponse`, `Cancel` | Runtime consumes matched outbound responses; only original permission handles can answer permission requests; unsupported incoming controls receive errors |

Source: T callback/control declarations; Go [permission runtime](../permissions.go),
[detached permission codecs](../types/permission_data.go), [question codecs](../types/questions.go).
Unknown/completed cancellation IDs are observable; cancelled/used/closed handles
cannot be reused. Failed reserved answers are not retried and can terminate the
connection; already authorized bytes cannot be retracted. These safety rules
are covered by [cancellation tests](../permission_cancellation_test.go) and
[Linux write-barrier tests](../permission_cancellation_linux_test.go), not a claim
of every official callback feature.

### Hook event and response inventory

All 33 T `HookEvent` values and corresponding `HookInput` variants have
**unsupported Go callback registration and responses**:
`PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `Notification`,
`UserPromptSubmit`, `UserPromptExpansion`, `SessionStart`, `SessionEnd`, `Stop`,
`StopFailure`, `SubagentStart`, `SubagentStop`, `PreCompact`, `PostCompact`,
`PreModelSwitch`, `PostModelSwitch`, `PermissionRequest`, `PermissionDenied`,
`Setup`, `TeammateIdle`, `TaskCreated`, `TaskCompleted`, `Elicitation`,
`ElicitationResult`, `ConfigChange`, `WorktreeCreate`, `WorktreeRemove`,
`InstructionsLoaded`, `CwdChanged`, `FileChanged`, `DirectoryAdded`, `MessageDisplay`.
No matcher, callback timeout or async hook result is implemented. Sync outputs
`continue`, `suppressOutput`, `stopReason`, `decision`, `systemMessage`,
`terminalSequence`, `reason`, `hookSpecificOutput` are not Go response controls.
Hook lifecycle event reception remains Raw-only as listed above.

### Question and permission data boundaries

T's built-in AskUserQuestion input exposes `questions`, `answers`, `annotations`,
`metadata`; output exposes `questions`, `answers`, `response`, `annotations`,
`afkTimeoutMs` (see [pinned tool declarations](https://unpkg.com/@anthropic-ai/claude-agent-sdk@0.3.258/sdk-tools.d.ts)).
Go types questions/header/options/multiSelect, option label/description/preview,
answers and general response; annotations/metadata/timeout remain unknown Raw.
The pinned official input has string-valued answers, and its output describes
comma-separated multi-select strings. Go also accepts string arrays for
multi-select: this exceeds the pinned TypeScript schema and remains an unverified
extension. Go's typed Response is not a field of T's input (it is present in T's output).
Go validates structure and full question-text answer keys, not all official UI
constraints (such as question/option counts). String/array answer codecs and
WithAnswers are data helpers; actual acceptance still depends on the CLI.

T permission updates can add/replace/remove rules, set mode or add/remove
directories. Go exposes no corresponding runtime update, destination selection,
`toolUseID` override, classification or deny interrupt. Detached codecs preserve
unknown data but do not gain those authorities. Elicitation form/URL schema,
accept/decline/cancel content and user-dialog payload/settlement are likewise
unsupported, distinct from can_use_tool questions.

### Wire control inventory (35 request variants)

This inventories T `SDKControlRequestInner`, **not additional public Query
methods**. A private declaration or a bundled JavaScript member does not make a
Go or public typed TypeScript API. Each named request below has the exact Go
status shown; unsupported controls have no feature verification.

| Native request variant(s) | Go status / runtime behavior |
| --- | --- |
| `SDKControlInitializeRequest` | Partial: Open sends hooks null and forwardSubagentText true; no other init configuration or public response getter |
| `SDKControlInterruptRequest` | Partial: Interrupt sends subtype only; no cancel_queued selection or interrupt receipt API |
| `SDKControlPermissionRequest` | Partial incoming can_use_tool: original PermissionRequest answer only |
| `SDKControlSetPermissionModeRequest`, `SDKControlSetModelRequest`, `SDKControlSetMaxThinkingTokensRequest`, `SDKControlApplyFlagSettingsRequest`, `SDKControlGetSettingsRequest`, `SDKControlUpdateSettingsRequest` | Unsupported: no public request operation |
| `SDKControlRenameSessionRequest`, `SDKControlSetColorRequest`, `SDKControlRegisterRepoRootRequest` | Unsupported: no session appearance/root-registration control |
| `SDKControlMcpStatusRequest`, `SDKControlMcpCallRequest`, `SDKControlMcpMessageRequest`, `SDKControlMcpSetServersRequest`, `SDKControlMcpReconnectRequest`, `SDKControlMcpToggleRequest` | Unsupported: no RPC host/call or server administration |
| `SDKControlGetContextUsageRequest`, `SDKControlGetSessionCostRequest`, `SDKControlListModelsRequest`, `SDKControlGetUsageRequest`, `SDKControlGetBinaryVersionRequest` | Unsupported: no query operation; result usage/version metadata is read-only |
| `SDKControlFileSuggestionsRequest`, `SDKControlRewindFilesRequest`, `SDKControlReadFileRequest`, `SDKControlSeedReadStateRequest` | Unsupported: no file/cache control |
| `SDKControlReloadPluginsRequest`, `SDKControlReloadSkillsRequest` | Unsupported: no refresh operation |
| `SDKControlCancelAsyncMessageRequest`, `SDKControlStopTaskRequest`, `SDKControlBackgroundTasksRequest` | Unsupported: no pending-message cancellation by UUID, targeted task stop or foreground-task backgrounding |
| `SDKHookCallbackRequest`, `SDKControlElicitationRequest`, `SDKControlRequestUserDialogRequest` | Unsupported: no success responder; unknown incoming requests receive unsupported errors |

Go's generic incoming `ControlRequest` retains nested request Raw; matched
outbound `SDKControlResponse` success/error is consumed internally. Detached
response codecs support subtype/request_id/response or error; the official
`pending_permission_requests` and `pending_user_dialog_requests` fields remain
Raw without reinitialize/recovery. `control_cancel_request` invalidates a real
handle; progress messages do not create a new operation. No generic public
control writer bypasses these boundaries.

## Top-level runtime exports (26)

This inventory covers runtime entrypoints, query configuration, message variants
and callback/control contracts. It does not claim field parity for every exported
Settings, helper-result or third-party Messages API type alias. Exported constants
are not counted as implemented actions.

| Official export(s) | Go status / difference |
| --- | --- |
| `query` | Partial: Open/Send/Receive lifecycle; no exact query helper signature |
| `AbortError` | Partial: Go context errors / `ErrUnknownOutcome` / runtime error types, not the official class |
| `tool`, `createSdkMcpServer` | Unsupported: no custom tool/MCP host |
| `listSessions`, `getSessionInfo`, `getSessionMessages`, `getSubagentMessages`, `listSubagents`, `forkSession`, `renameSession`, `tagSession`, `deleteSession` | Unsupported: no native session/transcript administration helpers |
| `InMemorySessionStore`, `importSessionToStore`, `foldSessionSummary` | Unsupported: no session-store adapter/import/folding contract |
| `resolveSettings`, `startup`, `filterEscalatingDefaultMode` | Unsupported: no equivalent settings/startup-policy helper |
| `EXIT_REASONS`, `HOOK_EVENTS`, `ORG_POLICY_LIMIT_PREFIXES`, `SYSTEM_PROMPT_DYNAMIC_BOUNDARY`, `USAGE_LIMIT_ERROR_PREFIXES`, `USAGE_TRANSITION_PREFIXES`, `USAGE_WARNING_PREFIXES` | Unsupported official constant sets; unrelated Go constants/types are not aliases |

Source: T runtime declarations. No session-store or helper behavior is validated
by merely decoding its resulting event shape.

### Separate package subpaths

The same official package also exposes distinct entrypoints, all **unsupported
in Go**; they are not main-package Query methods:

| Entry point | Official runtime exports | Go gap |
| --- | --- | --- |
| `./browser` ([declarations](https://unpkg.com/@anthropic-ai/claude-agent-sdk@0.3.258/browser-sdk.d.ts)) | `query`, `createSdkMcpServer`, `tool`, `G4_TEXT_ENVELOPE_ARM_PORTED`, `getSseDropCounts` | No browser SSE/WebSocket transport, browser tool/MCP host or drop counters |
| `./bridge` ([declarations](https://unpkg.com/@anthropic-ai/claude-agent-sdk@0.3.258/bridge.d.ts)) | `attachBridgeSession`, `createCodeSession`, `fetchRemoteCredentials`, `isCredentialsFailure`, `isCredentialsRejection`, `isCreateSessionFailure` | No remote-session attachment/creation/credentials or corresponding type guards |
| `./extract` ([declarations](https://unpkg.com/@anthropic-ai/claude-agent-sdk@0.3.258/extractFromBunfs.d.ts)) | `extractFromBunfs` | No packaged-binary extraction helper |

The T `Transport` export is an interface, not an `Options.transport` injection
field. T `Query.reinitialize` can recover pending permission/dialog requests;
Go has no equivalent recovery or redelivery contract. Browser/bridge networking
must not be inferred from the root process client.

## Separately versioned Python comparison

P exposes `ClaudeSDKClient.connect`, `query`, `receive_messages`,
`receive_response`, `disconnect`, `interrupt`, `set_permission_mode`, `set_model`,
`rewind_files`, `reconnect_mcp_server`, `toggle_mcp_server`, `stop_task`,
`get_mcp_status`, `get_context_usage`, `get_server_info`, and async context-manager
methods. Go maps only local startup/text input/stream/close, interrupt and
limited native resume/permissions. There is no receive-until-result helper,
dynamic setter, file/MCP/task control, context query or server-info getter.
P also accepts a custom `Transport`; Go does not. Python's flattened messages
are not Go wire envelopes. These differences do not establish that P and T
have identical methods, defaults or CLI version availability.

## Bounded runtime coverage

The retained local observations from 2026-10-18 used macOS arm64, Go 1.26.9,
CLI **2.1.258** and configured model `theta/GLM-5.3[1m]`. Eight test inputs
observed ordinary replies, one fresh-process history continuation, delayed
AskUserQuestion answers (single choice, one selected multi-select label and free
text), Write unchanged/replaced allow, Write deny, and pending-question interrupt
with cancellation and rejected late answer. The 655 captured frames round-tripped
semantically. Private captures are not checked-in replay fixtures or independent
provider-request/billing verification.

The tool scenarios deliberately ended `error_max_turns` / `is_error:true` under
a one-model-turn limit. They demonstrate named tool behavior, not completed
successful dialogues. Untested live: CLI 2.1.302, `{}` replacement, multi-label or
joined-string selections, unknown question fields, auto-approved paths,
close/answer or cancel/answer races, ambiguous disconnects, deliberate overflow,
unsupported controls and descendant escape. All subagent runtime capability
remains unverified. No full version certification or official endorsement follows.

The checked-in [recorded replay](record-replay.md) contains a CLI authentication
failure, not successful model/tool/child output. Its result has subtype success
with `is_error:true` and later idle; neither subtype nor idle proves successful
work. Initialization acceptance does not prove forwarding. The recording also
observed startup hooks despite isolation flags, so the recorder guard does not
prove a hook-free launch; successful recording remains blocked as documented.

## Resource and shutdown contract

These are Go bounds, not official feature parity. Zero selects defaults;
negative/above-ceiling values fail before spawn. Accepted events drain before
terminal errors. Event consumption backlog grows dynamically and does not block the stdout reader, control replies or permission dispatch. It is limited by available machine memory: sustained imbalance can cause OOM, and crash delivery is not guaranteed. Other queue bounds are not a fixed total heap/RSS guarantee.

| Go Limits field | Default | Ceiling | Behavior |
| --- | ---: | ---: | --- |
| `EventBuffer` | Ignored | 65,536 (validation only) | Deprecated; dynamic FIFO backlog, no capacity allocation |
| `FrameBytes` | 4 MiB | 64 MiB | Incoming excess terminal; local Send excess rejected before write |
| `PendingControls` | 32 | 65,536 | Local excess rejected before write |
| `ControlBuffer` | 32 each | 65,536 each | Separate automatic-response/handler queues; overflow terminal |
| `StderrBytes` | 8 KiB | 16 MiB | Newest tail only; no automatic logging |
| `PendingPermissions` | 128 | 65,536 | Unanswered incoming controls; overflow terminal |
| `PermissionWorkers` | 4 | 1,024 | Bounded callbacks; ignoring cancellation retains worker |
| `PermissionHistory` | 4,096 | 1,048,576 | Distinct request/cancel IDs; no eviction; overflow terminal |

Initialization defaults to 60 seconds; shutdown to 3 seconds shared across
drain/terminate/kill. `Done` does not need event queue space. `Err` can gain final
exit/protocol diagnostics during cleanup; `Close` waits for cleanup. Pre-write
cancellation/rejection does not inherit another write's unknown outcome.
A started failed write may be unknown and closes the connection; a control wait
cancelled after a completed write is unknown without necessarily closing it.
No operation automatically resends. See [client](../client.go) and [errors](../errors.go).

POSIX builds use an owned process group (Linux, macOS, FreeBSD, OpenBSD, NetBSD,
DragonFly). Linux/macOS have offline lifecycle coverage; other runtimes are
unverified. Same-group descendants are signalled, including pipe holders;
escaped process groups are outside the guarantee. Non-POSIX builds kill only
the direct child. Go cannot forcibly stop arbitrary callback goroutines, and
OS scheduling/uninterruptible kernel waits can exceed a shutdown budget.
Additional graph/run/dedup/creation/attribution limits are in [subagent bounds](subagent-events.md).

## Reproduce the offline checks

[CI](ci.md) runs Linux/macOS unit/subprocess, race, vet, independent consumer,
offline examples and transcript validation with live flags disabled. Fixtures
replace HOME/environment; examples that need a real CLI are compiled only.
The live initialization probe skips by default. The [manual live checklist](live-compatibility.md)
is separate from these commands.

```sh
mise exec go@1.26.9 -- env CLAUDE_E2E=0 SDK_REAL_AGENT_E2E=0 go test -count=1 -timeout 180s ./...
mise exec go@1.26.9 -- env CLAUDE_E2E=0 SDK_REAL_AGENT_E2E=0 go test -race -count=1 -timeout 300s ./...
mise exec go@1.26.9 -- go vet ./...
mise exec go@1.26.9 -- bash scripts/check-managed-consumer.sh COMMIT_OR_VERSION
python3 scripts/check-transcripts.py
```

The independent consumer compiles the documented Go comparison example, keeping
the public API and documentation connected. Synthetic/replay success does not
validate official SDK tests, all union variants, configured accounts or models.
