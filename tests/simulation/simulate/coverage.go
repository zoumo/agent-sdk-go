package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type coverageRow struct {
	Scene            scene  `json:"scene"`
	SimulationStatus string `json:"simulationStatus"`
	Output, Error    string
	MaterialError    bool
}
type coverage struct {
	SchemaVersion                  int `json:"schemaVersion"`
	ManifestSHA256, SourceRevision string
	Tool                           toolIdentity
	SDK                            any
	Cases                          []coverageRow  `json:"cases"`
	Counts                         map[string]int `json:"counts"`
}

func gate(ctx context.Context, tool toolIdentity, o options) error {
	var list struct {
		SchemaVersion                  int
		ManifestSHA256, SourceRevision string
		Cases                          []scene
	}
	if err := toolJSON(ctx, tool.Command, []string{"--list", "--json"}, &list); err != nil {
		return err
	}
	if list.SchemaVersion != 1 || len(list.Cases) != 72 {
		return errors.New("unsupported/incomplete 72-combination catalog")
	}
	var external *scene
	if o.Recording != "" {
		var described scene
		if err := toolJSON(ctx, tool.Command, []string{"--describe", "--json", "--recording", o.Recording}, &described); err != nil {
			return err
		}
		if err := validateScene(described); err != nil {
			return err
		}
		external = &described
		found := false
		for _, s := range list.Cases {
			if s.Provider == external.Provider && s.CaseID == external.CaseID {
				found = true
			}
		}
		if !found {
			return errors.New("external case is not in complete catalog")
		}
	}
	report := coverage{SchemaVersion: 1, ManifestSHA256: list.ManifestSHA256, SourceRevision: list.SourceRevision, Tool: tool, SDK: sdkIdentity(o.Driver), Cases: make([]coverageRow, len(list.Cases)), Counts: map[string]int{}}
	seen := map[string]bool{}
	for _, s := range list.Cases {
		key := s.Provider + "/" + s.CaseID
		if seen[key] || s.SchemaVersion != 1 || (s.Requirement != "required" && s.Requirement != "optional") {
			return errors.New("invalid catalog identity/requirement")
		}
		seen[key] = true
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range o.Jobs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				s := list.Cases[i]
				row := coverageRow{Scene: s, SimulationStatus: "unverified"}
				if (external == nil && s.Available) || (external != nil && s.Provider == external.Provider && s.CaseID == external.CaseID) {
					child := o
					child.Output = filepath.Join(o.Output, s.Provider+"-"+s.CaseID)
					row.Output = child.Output
					selection := []string{"--provider", s.Provider, "--case", s.CaseID}
					if external != nil {
						selection = []string{"--recording", o.Recording}
					}
					err := os.Mkdir(child.Output, 0700)
					if err == nil {
						err = simulate(ctx, tool, child, selection)
					}
					row.SimulationStatus = "passed"
					if err != nil {
						row.SimulationStatus = "failed"
						row.Error = err.Error()
					}
					var outcome summary
					if e := readJSON(filepath.Join(child.Output, "summary.json"), &outcome); e != nil {
						row.MaterialError = true
					} else {
						row.MaterialError = outcome.MaterialError != ""
						for _, run := range outcome.Runs {
							row.MaterialError = row.MaterialError || run.MaterialError != ""
						}
					}

				} else {
					row.Error = s.MissingReason
					if external != nil {
						row.Error = "not selected in external-material gate"
					}
				}
				report.Cases[i] = row
			}
		}()
	}
	for i := range list.Cases {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	var failures []error
	for _, r := range report.Cases {
		report.Counts[r.SimulationStatus]++
		if (r.Scene.Requirement == "required" && (r.Scene.Verification != "passed" || r.SimulationStatus != "passed")) || r.MaterialError {
			failures = append(failures, fmt.Errorf("%s/%s: source=%s SDK=%s: %s", r.Scene.Provider, r.Scene.CaseID, r.Scene.Verification, r.SimulationStatus, r.Error))
		}
	}
	return errors.Join(errors.Join(failures...), writeJSON(filepath.Join(o.Output, "coverage.json"), report))
}
