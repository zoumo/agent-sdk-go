# Proposed independent repair ticket: macOS canonical temp paths

Discovered in ticket04 on macOS arm64, Go 1.26.9, at
`83b2cca42f94c9b9d2f161cdd7034f1224c97887`.
This is a test portability defect, not a demonstrated SDK runtime failure.

Reproduce the ordinary full suite with macOS's default TMPDIR under `/var`:
`mise exec go@1.26.9 -- go test -count=1 -timeout 180s ./...`.
TestOpenUsesLiteralArgumentsAndMatchedInitialize fails its environment isolation
assertion: captured cwd is `/private/var/...`, while HOME/options WorkDir are
`/var/...`. The OS resolves both to the same directory. The test incorrectly
requires byte-identical paths for cwd. HOME should still equal the requested
HOME exactly, and literal argv checks should remain exact.

Repair scope for a separate ticket: make cwd comparison filesystem-aware
(canonical paths or os.SameFile), retain exact environment/argument assertions,
and run full test/race/vet on Mac with default TMPDIR as well as Linux CI.
Do not change SDK WorkDir or permission behavior to satisfy this test.

Workaround used solely for ticket04 validation: canonical isolated TMPDIR under
`/private/tmp`. Full suite, race, vet and existing focused repeated checks pass
there. No fix has been made or claimed in ticket04; parent should schedule this
as an independent repair ticket.

## Resolution

The independent [macOS temp-path repair](mac-temp-path-repair.md) replaces the
cwd text comparison with directory identity checks, preserves literal HOME and
argv assertions, and adds a portable symlink fixture. That report records
default-TMPDIR validation separately from ticket04's historical workaround.
