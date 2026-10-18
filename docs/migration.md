# Migration

Replace imports of `github.com/zoumo/codex-sdk-go` with
`github.com/zoumo/agent-sdk-go/codex`, and imports of
`github.com/zoumo/claude-agent-sdk-go` with
`github.com/zoumo/agent-sdk-go/claude`. Apply the same prefix substitution to
`/types`. Update your dependency to the single module
`github.com/zoumo/agent-sdk-go`. Old import paths are not forwarded.

The native source roots moved under `codex/` and `claude/`. Package-local examples
and scripts retain package-relative paths: run their documented commands from the
corresponding package directory. The root `mise.toml` supplies the compiler to both.
Root checks and CI run from the repository root. There is one `go.mod` and `go.sum`.

The migration merges these complete source histories as parents, preserving commit
objects and original author information:

| Native SDK | Imported source main |
| --- | --- |
| [Codex](https://github.com/zoumo/codex-sdk-go) | `0719539580dce97726aba7ddb56c7c6ae0c24362` |
| [Claude](https://github.com/zoumo/claude-agent-sdk-go) | `72041470958a0d9f2f8e6ff5c0086a66ef193745` |

Historical evidence documents and recording manifests remain source-repository
records. Their original commit URLs, commands, timestamps and hashes are retained;
paths in those historical records are relative to the original native root.
Current validation uses the migrated paths. Native LICENSE and NOTICE files remain
with each package, including upstream protocol and third-party attribution.
