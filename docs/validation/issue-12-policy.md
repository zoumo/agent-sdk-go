# Issue 12: fixed Linux proxy profile

This is recorder preparation, not a successful capture. The original Claude
2.1.258 authentication-failure fixture is unchanged. No fresh model result,
permission interaction or saved-content fixture is established by these checks.
The implementation follows [testing](../../harness/testing.md) and
[layout](../../harness/code-layout.md). Scope is [#12](https://github.com/zoumo/agent-sdk-go/issues/12).

## Reviewed identities

Base: `f0e5ddb2ea6bb54d204ef62d21386aafa7d5f2dd` (includes #10).
The selected CLI remains **2.1.258**, not a relaxed version range. The official
npm package `@anthropic-ai/claude-code-linux-x64@2.1.258` was downloaded and its
registry SHA512 integrity checked without running its installer or CLI. Its
native executable is 215473560 bytes, SHA256
`704f1334ac65d3e89e1c6c1d7663293ad786a6166afdb71b5075337df630f976`.
The bounded file-verification ceiling is therefore 256 MiB rather than 128 MiB.
No other build is accepted by this profile, including another platform's build
with the same version string. The old publication schema/version remains pinned.

The approved bridge route selects `codex/gpt-6.1-sol` explicitly, never an
implicit Claude model alias. The separately restored official Magpie CLI
v0.1.1486 has SHA256
`39473230b97fea3622a09b8149a5d4cd898aaafa066f4717234d3cfe69027388`, matching its
[release asset digest](https://github.com/yetone/magpie/releases/tag/v0.1.1486).
Magpie source tag resolves to `b5a78fd8b535afa01d50a0e288edc545819d405e`.
Recorder isolation does not attest the proxy's authentication or routing: the
coordinator must separately establish that short-lived loopback bridge, exact
model route and approved independent account before launching a capture.

## Independent isolation evidence

Static inspection of the selected official native executable's embedded source
established the following profile-specific facts, before any CLI invocation:

- The Linux managed-policy directory selector `hu` uses `/etc/claude-code`;
  its build override `ITn` returns no override. `y1e`/`Un` load the file and
  drop-in directory there. WSL has additional Windows sources and is excluded.
- The remote managed-settings eligibility function `u` returns
  `custom_base_url` for a non-first-party API base before profile/account lookup.
  This profile only permits a canonical `http://127.0.0.1:<port>` with no user
  information, path, query or fragment. It does not disable remote policy for a
  first-party or gateway deployment; those deployments are not accepted.
- Parent-managed settings are read from the embedding host's in-memory slot
  (`dDn`). A fresh standalone executable with the exact SDK transport flags
  receives no parent-managed settings. Arbitrary proxy arguments are rejected.
- Fresh owned HOME/config/tmp and the exact synthetic project exclude stored
  remote policy, user settings, plugins and old history. The native environment
  is replaced, not inherited; only the existing explicitly permitted transport
  variables and PATH are copied opaquely. No auth file is read by preflight.

`InspectIsolation` independently verifies the actual build digest, Linux/amd64
and non-WSL kernel, absence of the entire system managed-policy directory, owned
workspace and endpoint. Its `Isolation` evidence has no public assertion fields;
`PreflightWithIsolation` rechecks actual state. The proxy performs the same
inspection again before its single durable launch claim and Start. Presence of
any managed directory (even empty), dangling link, unknown platform/build or
endpoint, changed synthetic input, nonempty home/config/tmp, or unreadable state
refuses execution. Mandatory controls are never edited, hidden or disabled to
pass. Existing safe-mode/disableAllHooks and post-start hook guards remain
additional defenses, not the evidence used to approve startup.

Current [official hooks documentation](https://code.claude.com/docs/en/hooks)
confirms that lower-level hook settings do not override managed hooks.
[Managed settings documentation](https://code.claude.com/docs/en/managed-settings)
explains the system, remote and host sources. These current pages corroborate
source interpretation; they do not substitute for the pinned build review.

## Financial policy and durable bounded launch

`require-hard-cap` remains the default and refuses when independent monetary and
token enforcement is unavailable. The explicitly selected `accept-uncertainty`
policy reflects approval to use root/input/time/byte boundaries without claiming
a strict dollar/token stop. It removes only that financial requirement. Neither
this enum nor an environment assertion certifies isolation or grants permission.
Native `--max-budget-usd 1` remains a stricter advisory control, not billing proof.
Actual provider generations/retries/cost can remain uncertain and must be reported.

The executable profile is **S1 only**, one new root, one reserved input (within
the authorized maximum of two), zero children, no scene retry. Existing 120 s
aggregate scene deadline, 30 s initialize, 3 s cleanup reservation, 2 MiB wire
and observations and 256 KiB stderr remain stricter than the approved 150 s
activity bound. SDK/root bounds do not imply a provider request count.

`Reserve` occurs before executable probes. The proxy additionally must append a
single `launch` claim with `financial_policy=accept-uncertainty` before Start;
reopening the ledger cannot repeat it, and a direct proxy without a reservation
cannot start. The claim only permits S1 in a single-reservation ledger. Every
Submit still durably appends its dispatch intent first. Failures are not refunded.
Historical ledger reconciliation is the coordinator's responsibility; a new path
never replenishes spent authorization. No old ledger is reset or migrated.

## Remaining capture and publication gates

Authentication location and original independent-login continuity remain under
investigation. Missing paths do not establish logout or expiry. No replacement
login, credential copying, platform auth reuse or old transcript inspection is
part of this code. Magpie's official discovery can refresh the same independent
Codex login in place; it is not read-only. Its HOME and CODEX_HOME parent must be
new isolated trees to avoid discovering sibling accounts. No import or account
switch is needed. This must be separately authorized and verified before use.

After an actual approved capture, require successful error-free result plus idle,
actual exact Read/Write approvals and output file, per-version sanitizer review,
unknown-field quarantine, typed IDs, secret/path/header/email scans, strict
public replay with full Raw/EOF/cleanup checks and independent review before
publishing only sanitized new synthetic-task bytes. S1 does not persist sessions;
SDK-US-019 and other unobserved controls/questions remain blocked/untriggered.
No successful child or complete common capability claim follows from S1.

## Entry-boundary review corrections

Independent Standards and Spec review identified pre-reservation workspace
cleanup, weak endpoint assertions, and direct-proxy cwd/Git/input bypasses.
The corrected proxy fixes `cmd.Dir` to its synthetic project; workspace
revalidation includes Git ancestry; ledger errors before reservation clean the
new owned workspace. Endpoint tests invoke the endpoint validator directly, so
an unrelated platform/digest failure cannot hide a broken endpoint check.

`RootWireGuard` now independently restricts the actual proxy boundary to one
exact initialize, one exact S1 user input, and original correlated Read/Write
permission replies with unchanged input. Unknown controls, permission changes,
unreserved/duplicate inputs, unknown/reused answers, changed tool input and
out-of-policy tools stop forwarding. Each actual input consumes a durable
`dispatch` record after the parent's `turn` intent and the proxy's unique launch
claim. These checks validate native messages without creating answers or
changing recorded bytes. Direct peer invocation does not bypass Drive's input
scope merely by obtaining a launch claim.
