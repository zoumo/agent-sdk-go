# Opt-in live compatibility checklist

**Status: NOT RUN.** This is a manual plan, not part of `go test`, CI, or the synthetic fixture suite. It requires separate authorization before accessing an account, installed real CLI, network, or paid model. Do not turn synthetic successes into live checkmarks.

## Record before running

Record UTC date, OS/architecture, Go version, this repository commit, exact CLI version and executable provenance, isolated working directory, relevant CLI settings and permissions, authentication method (never secrets), allowed model, spending limit and safe test files. Confirm the account/service use and any subprocess/tool effects are authorized. Do not publish credentials, private paths, user prompts or sensitive outputs in evidence.

## Required cases

1. Open and initialize. Verify correlation and server information with the recorded CLI version; startup failure must clean up its child.
2. Send a harmless ordinary prompt. Observe assistant content, raw events, result and actual native session ID. Distinguish complete write from CLI acceptance.
3. Close and reopen with the observed native ID. Verify actual retained history without replaying an earlier user message. Requested and observed identities remain distinct.
4. Interrupt active safe work. Match the acknowledgment and separately observe the eventual result. Do not infer rollback.
5. Trigger a harmless permission prompt using controlled fixtures/settings. Retain its handle while unrelated messages continue, then allow original input, allow updated input and deny. Verify original request IDs and actual CLI progress.
6. Exercise AskUserQuestion's actual path. Record whether it arrives as `can_use_tool` or assistant content. Only the former may be answered through this SDK's generic handle. Do not inject an invented tool-result message.
7. Cancel while a permission is waiting. Observe cancellation, reject a late answer, and verify no extra response. Include concurrent cancel/answer and close/answer cases without claiming retraction of bytes already being written.
8. Disconnect after a user/control write but before an acknowledgment. Verify pending calls terminate and report an unknown outcome where appropriate. Never automatically resend.
9. Stall the event consumer under controlled bounded output. Verify explicit overflow, retained accepted-event order and independent terminal signaling.
10. Check failed initialization, process exit, blocked writes and shutdown, including descendants holding pipes on each platform claimed supported. Verify process ownership and reaping.
11. Inspect bounded stderr diagnostics without automatically exposing secrets. Record useful error classification and redact sensitive content before sharing.

For each case record PASS/FAIL/BLOCKED, exact command or test harness revision, observable wire/behavior evidence, limitations and redacted artifact location. Mark a CLI version runtime-validated only after the required cases pass; failures and incomplete coverage remain explicit. None of this is a production-safety or all-feature certification.
