SHELL := bash

GO ?= go
GOLANGCI_LINT ?= golangci-lint
PYTHON ?= python3
REPLAY_BIN ?= .artifacts/replay/bin/agent-replay
REPLAY_BUNDLE ?= .artifacts/replay/bundle

.PHONY: fmt fmt-check lint test check
.PHONY: replay-build replay-bundle replay-release replay-check-package replay-check-consumer

fmt:
	$(GOLANGCI_LINT) fmt

fmt-check:
	$(GOLANGCI_LINT) fmt --diff

lint:
	$(GOLANGCI_LINT) run ./...

test:
	$(GO) test -race -p=1 -count=1 -timeout=600s ./...

check: lint fmt-check test

replay-build:
	GO="$(GO)" bash scripts/replay/build.sh "$(REPLAY_BIN)"

replay-bundle:
	GO="$(GO)" $(PYTHON) scripts/replay/release.py --bundle --allow-untagged --output "$(REPLAY_BUNDLE)"

replay-release:
	@test -n "$(OUTPUT)" || { echo 'usage: make replay-release OUTPUT=/absolute/new-dist [SDK_TAG=vX.Y.Z]' >&2; exit 2; }
	GO="$(GO)" $(PYTHON) scripts/replay/release.py --output "$(OUTPUT)" $(if $(SDK_TAG),--tag "$(SDK_TAG)")

replay-check-package:
	@test -n "$(OUTPUT)" || { echo 'usage: make replay-check-package OUTPUT=/absolute/dist' >&2; exit 2; }
	GO="$(GO)" $(PYTHON) tests/replay/release/check_installed.py "$(OUTPUT)"

replay-check-consumer:
	@test -n "$(SDK_TAG)" || { echo 'usage: make replay-check-consumer SDK_TAG=vX.Y.Z' >&2; exit 2; }
	GO="$(GO)" PYTHON="$(PYTHON)" bash scripts/replay/check-consumer.sh "$(SDK_TAG)" "$$(git rev-parse HEAD)"
