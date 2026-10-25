# Release preparation

The planned first version is **v0.1.0-rc.1** for the single module
`github.com/zoumo/agent-sdk-go`. No tag or GitHub release is created by these
instructions or by CI. A separate publishing decision is required after the
exact `main` commit passes its checks.

## Required checks

1. Confirm `origin/main` equals the intended release commit and both recorded
   source commits remain ancestors; preserve the existing target history.
2. Run the pinned Go 1.26.9 compiler through mise. Require all three Offline
   jobs (`ubuntu-24.04`, `macos-15`, `macos-26`) for offline checks, race, vet,
   schema/inventory generation, independent
   native types, sanitized recordings, and examples to succeed on that exact SHA.
3. Require all three external-consumer CI jobs on the same SHA (the same
   Ubuntu, macOS 15 and macOS 26 matrix), for six required push/manual or
   same-repository PR jobs. They create temporary
   modules outside the checkout, retrieve the module from the new repository
   with a fresh module cache, and verify the retrieved commit before compiling
   all four public imports, example code and documented configuration snippets.
   They execute synthetic configuration/public-consumer checks, SDKs against
   built mock CLIs, and the native history examples. Repository/dependency
   retrieval needs network access; no real agent CLI or model is invoked.
4. Review [version notes](../CHANGELOG.md), [migration](migration.md),
   [shared stories](../harness/contracts/stories/index.md), [relationship contracts](../harness/contracts/lineage.md),
   [shared terms](../harness/glossary.md), [licenses](../LICENSE) and
   [attribution](../NOTICE). Preserve native package evidence and recording
   provenance. Record actual commit SHA, CI run URLs and limitations in the
   release review rather than committing a self-referential SHA.
5. Resolve any failed or skipped required gate before publishing. Same-repository
   PR consumer checks retrieve the published head SHA and check out that same
   source; Offline jobs test the PR merge checkout. Fork PR consumer checks remain
   excluded. A synthetic PR merge SHA is never used for module retrieval. Require
   the resulting exact `main` SHA's six push checks again after squash/merge.

To reproduce external consumer verification after pushing the intended commit:

```sh
mise exec -- bash scripts/check-consumers.sh "$(git rev-parse HEAD)"
```

The command accepts a full 40-character pushed SHA. It uses no `replace` or
`go.work`, discards its independent temporary module/cache afterward, and prints
the verified SHA. It cannot validate an unpushed local commit. No credentials
are written by the script.

## Publication boundary

One repository version covers `codex`, `codex/types`, `claude` and `claude/types`.
There is no root generic client or compatibility module for either previous
import path. Consumers must update imports explicitly; see
[migration](migration.md). The old repositories remain intact.

Real agent/model tests are deliberately disabled for this migration. Source
inspection, synthetic/offline contracts and historical sanitized recordings
remain distinct forms of evidence. Current successful native subagent
interoperability is **unverified**. A green CI run does not change that status.

Only after release review may an authorized publisher create the selected tag
and release. This implementation leaves both uncreated.
