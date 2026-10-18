// SPDX-License-Identifier: Apache-2.0
package mock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	replay "github.com/zoumo/agent-sdk-go/claude/tools/replay"
)

// ScenarioEnvironment supplies the scenario path without modifying SDK options.
const ScenarioEnvironment = "CLAUDE_MOCK_SCENARIO"

// CLI runs the same peer as Run. The scenario path is supplied explicitly by the
// command's environment; no HOME, account configuration or executable is read.
// Exit codes: 0 complete, 1 mismatch/incomplete/I/O, 2 usage/invalid scenario,
// 3 timeout/cancellation, 4 intentional scenario disconnect. Diagnostics go only
// to stderr. SDK-native startup flags are validated but do not alter the script.
func CLI(ctx context.Context, args []string, scenarioPath string, input io.ReadCloser, output io.WriteCloser, stderr io.Writer) int {
	fail := func(code int, err error) int { fmt.Fprintln(stderr, "claude mock:", err); return code }
	if err := ValidateArgs(args); err != nil {
		return fail(2, err)
	}
	if scenarioPath == "" {
		return fail(2, fmt.Errorf("set %s to a versioned scenario file", ScenarioEnvironment))
	}
	file, err := os.Open(scenarioPath)
	if err != nil {
		return fail(2, err)
	}
	scenario, loadErr := Load(file)
	closeErr := file.Close()
	if loadErr != nil {
		return fail(2, loadErr)
	}
	if closeErr != nil {
		return fail(2, closeErr)
	}
	if err := Run(ctx, input, output, scenario); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return fail(3, err)
		}
		if errors.Is(err, replay.ErrDisconnected) {
			return fail(4, err)
		}
		return fail(1, err)
	}
	return 0
}

// ValidateArgs accepts exactly the public SDK's stream-json startup flags plus
// optional --model=value and --resume=value. Duplicate or unknown flags fail.
// Empty arguments are accepted for direct shell/library use.
func ValidateArgs(args []string) error {
	values := map[string]string{"--output-format": "stream-json", "--input-format": "stream-json", "--permission-prompt-tool": "stdio"}
	flags := map[string]bool{"--verbose": true, "--include-partial-messages": true}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, joined := strings.Cut(arg, "=")
		if seen[name] {
			return fmt.Errorf("duplicate startup flag %s", name)
		}
		seen[name] = true
		if want, ok := values[name]; ok {
			if !joined {
				i++
				if i >= len(args) {
					return fmt.Errorf("missing value for %s", name)
				}
				value = args[i]
			}
			if value != want {
				return fmt.Errorf("unsupported %s value", name)
			}
		} else if flags[name] && !joined {
		} else if (name == "--model" || name == "--resume") && joined && value != "" {
		} else {
			return fmt.Errorf("unsupported startup argument %s", arg)
		}
	}
	return nil
}
