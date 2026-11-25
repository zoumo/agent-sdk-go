package main

import (
	"fmt"

	claudesim "github.com/zoumo/agent-sdk-go/client/claude/tests/simulation"
	codexsim "github.com/zoumo/agent-sdk-go/client/codex/tests/simulation"
)

func validateChildDelivery(provider, root string, background bool) error {
	switch provider {
	case "codex":
		return codexsim.ValidateChildDelivery(root, background)
	case "claude":
		return claudesim.ValidateChildDelivery(root, background)
	default:
		return fmt.Errorf("unsupported provider %q", provider)
	}
}
