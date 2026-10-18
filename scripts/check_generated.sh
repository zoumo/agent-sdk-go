#!/usr/bin/env bash
set -euo pipefail
# No upstream download. Reproduce the pinned checked-in schema closure only.
python3 scripts/generate_protocol.py
gofmt -w protocol/types_generated.go protocol/schema_generated_test.go
git diff --exit-code -- protocol/schema.json protocol/types_generated.go protocol/schema_generated_test.go
