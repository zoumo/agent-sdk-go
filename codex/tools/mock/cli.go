package mock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Main implements the thin command. Only stdout carries protocol frames.
// Exit codes: 0 complete scenario, 1 mismatch/incomplete/transport failure,
// 2 invalid configuration, 3 deadline/cancellation, 4 scripted disconnect.
// Scenario selection is --scenario PATH or CODEX_MOCK_SCENARIO; explicit flags
// override the environment. Native startup accepts app-server and --listen
// stdio:// only. Unknown options and WebSocket listeners fail closed.
func Main(ctx context.Context, args []string, in io.ReadCloser, out io.WriteCloser, stderr io.Writer) int {
	path := os.Getenv("CODEX_MOCK_SCENARIO")
	appServer := false
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "app-server" && !appServer:
			appServer = true
		case arg == "--scenario" || arg == "--listen":
			i++
			if i == len(args) {
				fmt.Fprintln(stderr, "codex mock: missing option value")
				return 2
			}
			if arg == "--scenario" {
				path = args[i]
			} else if args[i] != "stdio://" {
				fmt.Fprintln(stderr, "codex mock: only stdio:// is supported")
				return 2
			}
		case strings.HasPrefix(arg, "--scenario="):
			path = strings.TrimPrefix(arg, "--scenario=")
		case arg == "--listen=stdio://":
		default:
			fmt.Fprintln(stderr, "codex mock: unsupported startup argument")
			return 2
		}
	}
	if path == "" {
		fmt.Fprintln(stderr, "codex mock: --scenario or CODEX_MOCK_SCENARIO is required")
		return 2
	}
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(stderr, "codex mock: cannot open scenario")
		return 2
	}
	scenario, err := Load(f)
	_ = f.Close()
	if err != nil {
		fmt.Fprintln(stderr, "codex mock:", err)
		return 2
	}
	if err = Run(ctx, in, out, scenario); err != nil {
		fmt.Fprintln(stderr, "codex mock:", err)
		if errors.Is(err, ErrDisconnected) {
			return 4
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return 3
		}
		return 1
	}
	return 0
}
