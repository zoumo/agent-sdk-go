SHELL := bash

GO ?= go
GOLANGCI_LINT ?= golangci-lint

.PHONY: fmt fmt-check lint test check

fmt:
	$(GOLANGCI_LINT) fmt

fmt-check:
	$(GOLANGCI_LINT) fmt --diff

lint:
	$(GOLANGCI_LINT) run ./...

test:
	GO="$(GO)" bash scripts/check-offline.sh race

check: lint fmt-check test
