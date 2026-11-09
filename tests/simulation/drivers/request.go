package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

// The Go SDK adapter is a native driver too: it owns the case's public SDK
// operations and assertions. The process runner sees only its final result.
type remoteConnection struct {
	URL         string `json:"url"`
	BearerToken string `json:"bearerToken"`
}
type driverPeer struct {
	FixtureDir string `json:"fixtureDir"`
	Connection string `json:"connection"`
	Remote     *struct {
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
	if err := json.Unmarshal(b, &req); err != nil {
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
	if err := json.Unmarshal(b, &plan); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var caseErr error
	executionID := ""
	if req.Case != plan.CaseID {
		caseErr = errors.New("case differs from supplied materials")
	} else {
		if req.Policy != "preserve" && req.Policy != "suppress" {
			return errors.New("explicit host policy must be preserve or suppress")
		}
		executionID, caseErr = acceptRequest(ctx, plan.Provider, plan.DriverProfile, req.Output, req.Peer, req.Policy)
		if caseErr == nil && (plan.DriverProfile == "children" || plan.DriverProfile == "topology" || plan.DriverProfile == "background-children") {
			caseErr = validateChildDelivery(plan.Provider, req.Output, plan.DriverProfile == "background-children")
		}
	}
	result := map[string]string{"status": "passed"}
	if caseErr != nil {
		result["status"] = "failed"
		result["message"] = caseErr.Error()
	}
	if executionID != "" {
		result["executionId"] = executionID
	}
	b, err = json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(req.Output, "result.json"), append(b, '\n'), 0o600)
}

func acceptRequest(ctx context.Context, provider, profile, root string, peer driverPeer, policy string) (executionID string, result error) {
	var mock *mocktest.Session
	if profile != "remote-close" {
		if peer.Connection != "main" {
			return "", errors.New("stdio connection must be main")
		}
		if peer.FixtureDir == "" {
			return "", errors.New("runner must provide a prepared fixture")
		}
		var err error
		mock, err = mocktest.Open(peer.FixtureDir)
		if err != nil {
			return "", err
		}
		defer func() { result = errors.Join(result, mock.Close()) }()
	}
	result = accept(ctx, provider, root, filepath.Join(root, "raw", "raw.jsonl"), peer, mock, policy, filepath.Join(root, "deliveries.jsonl"))
	if mock == nil {
		return "", result
	}
	ids := mock.ExecutionIDs()
	if len(ids) == 1 {
		executionID = ids[0]
	}
	report, reportErr := mock.Report()
	if reportErr != nil {
		return executionID, errors.Join(result, reportErr)
	}
	if len(report.Executions) == 1 {
		executionID = report.Executions[0].ID
	}
	return executionID, result
}
