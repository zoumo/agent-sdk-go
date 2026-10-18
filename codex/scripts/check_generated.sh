#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
# No upstream download. Reproduce the pinned checked-in schema closure only.
python3 scripts/generate_protocol.py
gofmt -w types/types_generated.go types/schema_generated_test.go
git diff --exit-code -- types/schema.json types/types_generated.go types/schema_generated_test.go
