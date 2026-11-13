// Explicit offline Go SDK acceptance entry. No native-agent fallback exists.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
	claudesim "github.com/zoumo/agent-sdk-go/tests/simulation/drivers/claude/go"
	codexsim "github.com/zoumo/agent-sdk-go/tests/simulation/drivers/codex/go"
)

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "--filter-mcp" {
		if err := claudesim.RunMCPFilter(os.Args[2], os.Args[3:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		return
	}
	if len(os.Args) != 3 || os.Args[1] != "--request" {
		fmt.Fprintln(os.Stderr, "use --request /path/to/request.json")
		os.Exit(2)
	}
	if err := runRequest(os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func accept(ctx context.Context, provider, root, raw string, peer driverPeer, mock *mocktest.Session, policy, observations string) error {
	out, err := os.OpenFile(observations, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	var plan struct{ DriverProfile string }
	data, err := os.ReadFile(filepath.Join(root, "plan.json"))
	if err == nil {
		err = json.Unmarshal(data, &plan)
	}
	if err != nil {
		return err
	}
	switch provider {
	case "codex":
		switch plan.DriverProfile {
		case "existing-children":
			err = codexsim.AcceptExistingChildren(ctx, root, mock, raw, codex.ChildDetailPolicy(policy), out)
		case "remote-close":
			if peer.Remote == nil {
				return errors.New("remote endpoint is required")
			}
			main, mainOK := peer.Remote.Connections["main"]
			probe, probeOK := peer.Remote.Connections["probe"]
			if !mainOK || !probeOK {
				return errors.New("remote main and probe endpoints are required")
			}
			err = codexsim.AcceptRemoteClose(ctx, root, main.URL, main.BearerToken, probe.URL, probe.BearerToken)
		case "history", "continuation":
			err = codexsim.AcceptSavedHistory(ctx, mock, raw, codex.ChildDetailPolicy(policy), out)
		default:
			err = codexsim.AcceptFlow(ctx, mock, raw, codex.ChildDetailPolicy(policy), out)
		}
	case "claude":
		switch plan.DriverProfile {
		case "existing-children":
			err = claudesim.AcceptExistingChildren(ctx, mock, root, claude.ChildDetailPolicy(policy), out)
		case "history":
			err = claudesim.AcceptSavedHistory(ctx, mock, root, claudesim.Host{}, claude.ChildDetailPolicy(policy), out)
		default:
			err = claudesim.AcceptFlow(ctx, mock, root, claudesim.Host{}, claude.ChildDetailPolicy(policy), out)
		}
	default:
		err = errors.New("unsupported provider")
	}
	return err
}
