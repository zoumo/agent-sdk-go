package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

// The Go SDK adapter is a native driver too: it owns the case's public SDK
// operations and assertions. The process runner sees only its final result.
type peerCommand struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}
type remoteConnection struct {
	URL         string `json:"url"`
	BearerToken string `json:"bearerToken"`
}
type driverPeer struct {
	peerCommand
	Remote *struct {
		Connections map[string]remoteConnection `json:"connections"`
	} `json:"remote"`
}

func runRequest(path string) error {
	var req struct {
		Case, Materials, Workspace, Output, Policy string
		Peer                                       driverPeer
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &req); err != nil {
		return err
	}
	var plan struct {
		Provider      string
		CaseID        string `json:"caseId"`
		DriverProfile string
		Scenario      struct {
			Parameters struct{ ChildDetailPolicy string }
		}
	}
	b, err = os.ReadFile(filepath.Join(req.Materials, "case.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &plan); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var caseErr error
	if req.Case != plan.CaseID {
		caseErr = fmt.Errorf("case differs from supplied materials")
	} else {
		if req.Policy != "preserve" && req.Policy != "suppress" {
			return fmt.Errorf("explicit host policy must be preserve or suppress")
		}
		caseErr = acceptRequest(ctx, plan.Provider, plan.DriverProfile, req.Output, req.Peer, req.Policy)
		if caseErr == nil && (plan.DriverProfile == "children" || plan.DriverProfile == "topology" || plan.DriverProfile == "background-children") {
			caseErr = validateChildDelivery(plan.Provider, req.Output, plan.DriverProfile == "background-children")
		}

	}
	result := map[string]string{"status": "passed"}
	if caseErr != nil {
		result["status"] = "failed"
		result["message"] = caseErr.Error()
	}
	b, err = json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(req.Output, "result.json"), append(b, '\n'), 0600)
}

func acceptRequest(ctx context.Context, provider, profile, root string, peer driverPeer, policy string) error {
	if peer.Command == "" {
		return fmt.Errorf("runner must provide a peer command")
	}
	return accept(ctx, provider, root, filepath.Join(root, "raw/raw.jsonl"), peer, policy, filepath.Join(root, "deliveries.jsonl"))
}
