package main

import (
	"fmt"
	claudesim "github.com/zoumo/agent-sdk-go/tests/simulation/sdk/claude"
	codexsim "github.com/zoumo/agent-sdk-go/tests/simulation/sdk/codex"
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

func compareDelivery(provider, preserved, suppressed string) error {
	switch provider {
	case "codex":
		return codexsim.CompareDelivery(preserved, suppressed)
	case "claude":
		return claudesim.CompareDelivery(preserved, suppressed)
	default:
		return fmt.Errorf("unsupported provider %q", provider)
	}
}
