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
	CODEX_E2E_STDIO=0 CODEX_E2E_INTERACTIONS=0 CODEX_E2E_WEBSOCKET=0 \
		CLAUDE_E2E=0 SDK_REAL_AGENT_E2E=0 PI_E2E_CLI= AGENT_SDK_TEST_PI_CLI= \
		$(GO) test -race -p=1 -count=1 -timeout=600s ./...

check: lint fmt-check test
