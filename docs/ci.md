# CI checks and live gate

`Go checks` runs on pushes to `main`, pull requests and manual dispatches.
Feature branch pushes run through PR checks rather than a duplicate push job.
Concurrency cancels superseded runs for the same PR/ref; both matrix jobs still
run independently (`fail-fast: false`). PRs test GitHub's merge commit.

## Offline matrix

Both `ubuntu-latest` and `macos-latest` use exactly Go 1.26.9. Each job checks:

- Committed whitespace over the event range and gofmt for every tracked Go file
  in the checked commit, using `bash scripts/check-changes.sh BASE HEAD`.
- The independent `types` package has no nonstandard dependency beyond itself.
- Complete unit/subprocess and existing root/type compatibility tests with
  `go test -count=1 -timeout 180s ./...`.
- The same suite under `go test -race -count=1 -timeout 300s ./...`, then
  `go vet ./...`.

The event range is PR base to merge commit, or push `before` to current commit.
Manual dispatch defaults to the checked commit's first parent. An initial push
(all-zero `before`) uses the empty tree so all introduced content is checked;
a root commit without a parent also uses the empty tree. Missing nonzero base
objects fail rather than silently skipping checks. Full checkout history makes
these comparisons available. The check reads Git objects, so an empty or dirty
working tree cannot hide committed errors. It does not validate uncommitted
edits; use `git diff --check` and gofmt before committing those.

Tests use `env -i`, an isolated temporary HOME/GOCACHE, `GOTOOLCHAIN=local`,
`GOENV=off`, `GOPROXY=off`, and `GOSUMDB=off`. Go is provisioned before the
offline checks. Action caching stays disabled intentionally. `-count=1`
forces tests to execute even if the compiler reuses build artifacts in the
job's isolated cache. No credentials or real CLI path are forwarded to tests.
Linux-only FIFO tests remain Linux-only; POSIX teardown tests run on both OSes.
No Windows runtime coverage is claimed.

## Live initialization gate

`CLAUDE_GO_LIVE` accepts unset/`0` (skip) or `1` (opt in). Other values fail.
`TestLiveCLIInitialization` displays a `LIVE SKIPPED` reason in a dedicated
verbose CI step. The job's final log and summary also state that no real
Agent/CLI or model calls were made. CI fixes the value to `0`, including in
the replacement test environment, and never provides a live enable input.
The test additionally refuses an enabled gate when `CI` is nonempty.

After separate authorization for the installed CLI, configured account and
its startup effects, an operator may explicitly run:

```sh
CLAUDE_GO_LIVE=1 CLAUDE_GO_LIVE_CLI=/absolute/path/to/claude \
  mise exec go@1.26.9 -- go test -v -count=1 -run '^TestLiveCLIInitialization$' .
```

The CLI path must be absolute; there is no executable search or fallback.
This probe opens and closes the real CLI in a temporary working directory,
with a 30-second initialization context. It inherits the operator's environment
and CLI configuration. It sends no user prompt and requests no model turn.
It verifies initialization/shutdown only, not real-model behavior, history,
permissions or the [full live checklist](live-compatibility.md). Failure output
withholds CLI diagnostics to avoid publishing account details. Neither a skip
nor an initialization pass upgrades any full live compatibility claim.

## Actions provenance and permissions

Official latest releases were checked on 2026-10-18 through the GitHub API;
their `action.yml` files both specify Node 24:

| Action release | Pinned commit |
| --- | --- |
| [checkout v7.0.1](https://github.com/actions/checkout/releases/tag/v7.0.1) | `3d3c42e5aac5ba805825da76410c181273ba90b1` |
| [setup-go v7.0.0](https://github.com/actions/setup-go/releases/tag/v7.0.0) | `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` |

The workflow grants only `contents: read`, disables checkout credential
persistence and uses no `pull_request_target`. Types are handwritten;
there is no generator or generated-file synchronization step.
