# Public native data package rename

Baseline: `df80dc438ebec1c8c3cfee26ca0821e9bbb2f9c8` on main; initial worktree clean.
Local Matt spec and one unblocked ticket are in `.scratch/package-types/`.

The public data package is now `github.com/zoumo/codex-sdk-go/types`.
See the [migration guide](../migrations/protocol-to-types.md) for imports,
reflection identity, aliases when importing both SDKs, and historical evidence.

The 0.162.1 schema is byte-identical: SHA-256
`4f129fdc2bbaebf4b1752dd8f7bfb22180849736d0c064a7ac0052b111e0733a`.
Generation still emits 399 types and 750 schema round-trip cases. Two consecutive
regeneration passes are byte-identical. All pre-existing Go files were compared
to the baseline after only the specified package/import/qualifier rename and
formatting; string literals and JSON tags are unchanged. Upstream source records
are unchanged. Root Event/TreeEvent and connection/execution logic remain in place.

An independent `publicconsumer` module, with a local module replacement, passed
`go test ./...` and `go run .` offline. It compiled the root client's StartThread
signature with native types and checked reflection PkgPath without connecting.
The reusable example is `examples/publicconsumer`; CI runs it on both platforms.

Validation uses mise Go 1.26.9 with an explicit matching GOROOT, GOTOOLCHAIN=local,
GOPROXY=off and GOSUMDB=off. The inherited shell GOROOT pointed at 1.26.1, so the
first compile attempt was discarded and rerun with the matching toolchain.
All three live harness switches are zero. No CLI/model/auth or sibling SDK work
is part of this validation.

Passed local gates:

- `go test -v -count=1 ./...` and `go test -race -v -count=1 ./...`;
  all live tests visibly skipped.
- `go vet ./...` and `go mod verify`.
- `python3 docs/protocol/verify_inventory.py`.
- `bash scripts/check_generated.sh`, plus two byte-identical regeneration passes.
- All six CI gate tests, including committed generation drift rejection.
- `git diff --cached --check` and `actionlint`.
- Independent offline publicconsumer compile/run and old-import audit.

Full unit/race logs and CI-gate logs were retained under the local scratch
feature directory. Hosted CI is checked against the exact pushed commit.
