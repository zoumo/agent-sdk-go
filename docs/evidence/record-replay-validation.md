# Recording ticket validation

Base and prerequisite documentation SHA: `282f85aed93c571d4aaa500751d6786b021897b6`.
Prerequisite CI independently confirmed successful: [39105548229](https://github.com/zoumo/claude-agent-sdk-go/actions/runs/39105548229).

Status: **partial / blocked**. Required successful S1 model/tool recording was not obtained. One real process/turn attempt, no retries, zero children, 2.241444834 seconds. Native usage/cost zero; actual API execution unobserved. The call still counts conservatively as one attempt. S2 stopped on unavailable authentication in the recorder's replacement environment; S3 stopped on unproven child isolation. This does not diagnose normal interactive authentication. Startup hook events were also observed despite isolation flags; future recording requires independently established policy isolation. No credential inspection, auth changes, upgrade, extra turn, resume or child occurred.

Local compiler: Go 1.26.9 via mise. CLI read-only version 2.1.258 and binary SHA-256 are in the [manifest](../../internal/transcript/testdata/real-sanitized/s1-auth-blocked/manifest.json). The original recorder retained HOME/PATH/TMPDIR only; follow-up code passes recognized existing authentication variables opaquely, but was not used for another live attempt. Native is_error handling and the startup-hook refusal were hardened after the one attempt; no production SDK fix was needed.

Validated offline (exit 0):

- `go test -count=1 -timeout 180s ./...`, also under replacement HOME and `env -i` with `CLAUDE_GO_LIVE=0 SDK_REAL_AGENT_E2E=0`.
- `go test -race -count=1 -timeout 300s ./...`; affected replay/guard tests repeated with race after final fixture normalization.
- `go vet ./...`.
- `bash scripts/check-managed-consumer.sh`.
- Independent `types` dependency list contains only its own nonstandard package.
- `go run ./examples/recorded-replay` and existing `go run ./examples/subagents-offline`.
- `python3 scripts/check-transcripts.py`: whitelist/secret canaries, typed numeric/string ID mapping, ambiguous namespace quarantine, normalized timestamp, fixture SHA-256, ordinal and ID-reference checks.
- `git diff --check`; committed formatting/whitespace is checked before push.

Replay checks the real failure's sanitized Raw, native wrapper, error result and subsequent idle through default Open/Send/Receive, with complete-fixture and poison-real-CLI sentinels. Separate synthetic cases exercise multiple controls in flight, responses after notices, reverse permission answer/cancellation, wrong/missing/duplicate IDs, large numeric IDs, EOF, extra input and byte bounds. Existing tree/permission suites retain their synthetic provenance; this ticket does not promote them to recorded coverage.

Cleanup: SDK Close returned nil, original recorder exited 0 (it initially checked transport errors only), recorded CLI PID no longer existed and no owned recorder/proxy command remained. Private raw/observations/stderr, original ID maps and intermediate publication copies were deleted after validation. No raw material is committed. The original temporary recorder executable was removed. Public sample files are under `internal/transcript/testdata/real-sanitized/s1-auth-blocked/`; derived offline assertions and transport metadata are separate from wire data.

The final commit containing this report is the implementation result. GitHub's push workflow runs Linux/macOS offline checks with all live switches zero; final remote SHA and CI URL/status are reported in the execution handoff. A green offline CI does not satisfy the missing successful live S1 criterion.
