// Explicit offline Go SDK acceptance entry. No native-agent fallback exists.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zoumo/agent-sdk-go/claude"
	transcript "github.com/zoumo/agent-sdk-go/claude/tests/replay/acceptance"
	"github.com/zoumo/agent-sdk-go/codex"
	recording "github.com/zoumo/agent-sdk-go/codex/tests/replay"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--request" {
		if err := runRequest(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	peer := flag.String("peer", "", "verified Go mock executable")
	raw := flag.String("recording", "", "verified original raw JSONL recording")
	receipt := flag.String("receipt", "", "new private Go mock receipt path")
	provider := flag.String("provider", "codex", "native provider")
	root := flag.String("case", "", "verified private case directory")
	launcher := flag.String("launcher", "", "Claude mock-only process adapter")
	policy := flag.String("policy", "preserve", "Go Session child-detail delivery policy")
	observations := flag.String("observations", "", "new private actual Go Receive stream")
	flag.Parse()
	if *peer == "" || *raw == "" || *receipt == "" || *observations == "" || (*policy != "preserve" && *policy != "suppress") {
		fmt.Fprintln(os.Stderr, "explicit peer, recording and receipt required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := accept(ctx, *provider, *root, *peer, *raw, *receipt, *launcher, *policy, *observations); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func accept(ctx context.Context, provider, root, peer, raw, receipt, launcher, policy, observations string) error {
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
			err = recording.AcceptExistingChildren(ctx, root, peer, raw, receipt, codex.ChildDetailPolicy(policy), out)
		} else if plan.DriverProfile == "remote-close" {
			err = recording.AcceptRemoteClose(ctx, root, peer, filepath.Dir(receipt))
		} else if plan.DriverProfile == "history" || plan.DriverProfile == "continuation" {
			err = recording.AcceptSavedHistory(ctx, peer, raw, receipt, codex.ChildDetailPolicy(policy), out)
		} else {
			err = recording.AcceptFlow(ctx, peer, raw, receipt, out)
		}
	case "claude":
		if plan.DriverProfile == "existing-children" {
			err = transcript.AcceptExistingChildren(ctx, launcher, root)
		} else if plan.DriverProfile == "history" {
			err = transcript.AcceptSavedHistory(ctx, launcher, root, claude.ChildDetailPolicy(policy), out)
		} else {
			err = transcript.AcceptFlow(ctx, launcher, root, out)
		}
	default:
		err = fmt.Errorf("unsupported provider")
	}
	return err
}
