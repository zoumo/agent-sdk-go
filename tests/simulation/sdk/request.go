package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

// The Go SDK adapter is a native driver too: it owns the case's public SDK
// operations and assertions. The process runner sees only its final result.
func runRequest(path string) error {
	var req struct{ Case, Materials, Workspace, Output string }
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
		Mock          string `json:"_mock"`
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
		caseErr = acceptRequest(ctx, plan.Provider, plan.DriverProfile, req.Output, plan.Mock, "preserve")
		if caseErr == nil && (plan.DriverProfile == "children" || plan.DriverProfile == "topology" || plan.DriverProfile == "background-children") {
			caseErr = validateChildDelivery(plan.Provider, req.Output, plan.DriverProfile == "background-children")
		}
		if caseErr == nil && plan.Scenario.Parameters.ChildDetailPolicy != "" {
			second := filepath.Join(req.Output, "suppress")
			caseErr = os.Mkdir(second, 0700)
			if caseErr == nil {
				caseErr = acceptPolicyCopy(ctx, plan.Provider, plan.DriverProfile, req.Output, second, plan.Mock)
			}
			if caseErr == nil {
				caseErr = compareDelivery(plan.Provider, filepath.Join(req.Output, "deliveries.jsonl"), filepath.Join(second, "deliveries.jsonl"))
			}
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

func acceptRequest(ctx context.Context, provider, profile, root, peer, policy string) error {
	receipt := filepath.Join(root, "replay-peer.json")
	launcher := filepath.Join(root, "mock-cli")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	cmd := "#!/bin/sh\nexec " + quote(peer) + " protocol mock --provider " + quote(provider) + " --scenario " + quote(filepath.Join(root, "raw/raw.jsonl")) + " --receipt " + quote(receipt) + "\n"
	if err := os.WriteFile(launcher, []byte(cmd), 0700); err != nil {
		return err
	}
	err := accept(ctx, provider, root, peer, filepath.Join(root, "raw/raw.jsonl"), receipt, launcher, policy, filepath.Join(root, "deliveries.jsonl"))
	if err == nil && profile == "remote-close" {
		for _, item := range [][2]string{{"main-peer.json", "replay-peer.json"}, {"probe-peer.json", "remote-probe/replay-peer.json"}} {
			data, e := os.ReadFile(filepath.Join(root, item[0]))
			if e != nil {
				return e
			}
			if e = os.WriteFile(filepath.Join(root, item[1]), data, 0600); e != nil {
				return e
			}
		}
	}
	return err
}

func acceptPolicyCopy(ctx context.Context, provider, profile, root, target, peer string) error {
	for _, name := range []string{"raw", "config", "plan.json", "report.json"} {
		if err := os.Symlink(filepath.Join(root, name), filepath.Join(target, name)); err != nil {
			return err
		}
	}
	return acceptRequest(ctx, provider, profile, target, peer, "suppress")
}
