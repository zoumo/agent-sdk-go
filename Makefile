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
	$(GO) test -race -p=1 -count=1 -timeout=600s ./...

check: lint fmt-check test
