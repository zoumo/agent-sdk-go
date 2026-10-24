// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"fmt"
	"strconv"

	"github.com/zoumo/agent-sdk-go/claude/types"
)

func validateThinkingConfig(t *types.ThinkingConfig) error {
	if t == nil {
		return nil
	}
	switch t.Type {
	case "adaptive":
		if t.BudgetTokens != nil {
			return fmt.Errorf("%w: adaptive budget", ErrUnsupported)
		}
	case "enabled":
		if t.BudgetTokens != nil && *t.BudgetTokens < 0 {
			return fmt.Errorf("%w: negative creation budget", ErrInvalidInput)
		}
	case "disabled":
		if t.BudgetTokens != nil || t.Display != nil {
			return fmt.Errorf("%w: disabled budget/display", ErrUnsupported)
		}
	default:
		return fmt.Errorf("%w: thinking type %q", ErrUnsupported, t.Type)
	}
	if t.Display != nil && *t.Display != "summarized" && *t.Display != "omitted" {
		return fmt.Errorf("%w: thinking display", ErrUnsupported)
	}
	return nil
}

func thinkingArgs(t *types.ThinkingConfig) []string {
	if t == nil {
		return nil
	}
	var args []string
	switch {
	case t.Type == "disabled":
		args = append(args, "--thinking=disabled")
	case t.Type == "enabled" && t.BudgetTokens != nil:
		args = append(args, "--max-thinking-tokens="+strconv.Itoa(*t.BudgetTokens))
	default:
		args = append(args, "--thinking=adaptive")
	}
	if t.Display != nil {
		args = append(args, "--thinking-display="+*t.Display)
	}
	return args
}
