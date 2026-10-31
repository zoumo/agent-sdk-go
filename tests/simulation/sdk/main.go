// Explicit offline Go SDK acceptance entry. No native-agent fallback exists.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/tests/simulation/sdk/claude"
	"github.com/zoumo/agent-sdk-go/tests/simulation/sdk/codex"
)

func main() {
	if len(os.Args) != 3 || os.Args[1] != "--request" {
		fmt.Fprintln(os.Stderr, "use --request /path/to/request.json")
		os.Exit(2)
	}
	if err := runRequest(os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func accept(ctx context.Context, provider, root, raw string, peer driverPeer, policy, observations string) error {
	out, err := os.OpenFile(observations, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
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
		if plan.DriverProfile == "existing-children" {
			err = codexsim.AcceptExistingChildren(ctx, root, peer.Command, peer.Args, raw, codex.ChildDetailPolicy(policy), out)
		} else if plan.DriverProfile == "remote-close" {
			if peer.Remote == nil {
				return fmt.Errorf("remote endpoint is required")
			}
			err = codexsim.AcceptRemoteClose(ctx, root, peer.Remote.URL, peer.Remote.BearerToken)
		} else if plan.DriverProfile == "history" || plan.DriverProfile == "continuation" {
			err = codexsim.AcceptSavedHistory(ctx, peer.Command, peer.Args, raw, codex.ChildDetailPolicy(policy), out)
		} else {
			err = codexsim.AcceptFlow(ctx, peer.Command, peer.Args, raw, codex.ChildDetailPolicy(policy), out)
		}
	case "claude":
		if len(peer.Args) != 0 {
			return fmt.Errorf("Claude requires a runner-prepared executable launcher")
		}
		if plan.DriverProfile == "existing-children" {
			err = claudesim.AcceptExistingChildren(ctx, peer.Command, root)
		} else if plan.DriverProfile == "history" {
			err = claudesim.AcceptSavedHistory(ctx, peer.Command, root, claude.ChildDetailPolicy(policy), out)
		} else {
			err = claudesim.AcceptFlow(ctx, peer.Command, root, claude.ChildDetailPolicy(policy), out)
		}
	default:
		err = fmt.Errorf("unsupported provider")
	}
	return err
}
