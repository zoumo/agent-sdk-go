#!/usr/bin/env bash
# Check committed objects, including when the checkout has no working-tree diff.
set -euo pipefail
head=$(git rev-parse --verify "${2:-HEAD}^{commit}")
base=${1:-}
if [[ -z "$base" ]]; then
  if git rev-parse --verify "$head^" >/dev/null 2>&1; then
    base=$(git rev-parse "$head^")
  else
    base=$(git hash-object -t tree /dev/null)
  fi
elif [[ "$base" =~ ^0+$ ]]; then
  # GitHub's initial branch push has an all-zero before SHA.
  base=$(git hash-object -t tree /dev/null)
else
  base=$(git rev-parse --verify "$base^{commit}")
fi
printf 'Checking committed whitespace: %s -> %s\n' "$base" "$head"
git -c core.whitespace=blank-at-eol,blank-at-eof,space-before-tab diff --check "$base" "$head"

# All tracked Go files in the target commit, independent of the working tree.
failed=0
while IFS= read -r -d '' file; do
  case "$file" in
    *.go)
      if ! diff=$(git show "$head:$file" | gofmt -d); then
        printf 'gofmt check failed: %s\n%s\n' "$file" "$diff" >&2
        failed=1
      elif [[ -n "$diff" ]]; then
        printf 'gofmt required: %s\n%s\n' "$file" "$diff" >&2
        failed=1
      fi
      ;;
  esac
done < <(git ls-tree -r --name-only -z "$head")
exit "$failed"
