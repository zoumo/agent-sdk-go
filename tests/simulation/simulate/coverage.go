package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	coverageSchemaVersion = 2
	verificationUnknown   = "unknown"
	adoptionUnknown       = "unknown"
)

type acceptanceCase struct {
	ID             string
	Optional       bool
	CodexOptional  bool
	ClaudeOptional bool
}

// This is the SDK-owned acceptance policy, not a copy of upstream scenarios,
// bindings or recordings. Both providers share every case; the optional flags
// follow harness/sdk/validation-cases.md.
var acceptanceCases = []acceptanceCase{
	{ID: "SDK-CASE-001"},
	{ID: "SDK-CASE-002"},
	{ID: "SDK-CASE-003", Optional: true},
	{ID: "SDK-CASE-004"},
	{ID: "SDK-CASE-005"},
	{ID: "SDK-CASE-006"},
	{ID: "SDK-CASE-007"},
	{ID: "SDK-CASE-008"},
	{ID: "SDK-CASE-009"},
	{ID: "SDK-CASE-010"},
	{ID: "SDK-CASE-011"},
	{ID: "SDK-CASE-012", CodexOptional: true},
	{ID: "SDK-CASE-013"},
	{ID: "SDK-CASE-014"},
	{ID: "SDK-CASE-015"},
	{ID: "SDK-CASE-016"},
	{ID: "SDK-CASE-017", ClaudeOptional: true},
	{ID: "SDK-CASE-018"},
	{ID: "SDK-CASE-019"},
	{ID: "SDK-CASE-020"},
	{ID: "SDK-CASE-021"},
	{ID: "SDK-CASE-022"},
	{ID: "SDK-CASE-023"},
	{ID: "SDK-CASE-024"},
	{ID: "SDK-CASE-025", Optional: true},
	{ID: "SDK-CASE-026", Optional: true},
	{ID: "SDK-CASE-027"},
	{ID: "SDK-CASE-028"},
	{ID: "SDK-CASE-029"},
	{ID: "SDK-CASE-030"},
	{ID: "SDK-CASE-031"},
	{ID: "SDK-CASE-032", ClaudeOptional: true},
	{ID: "SDK-CASE-033"},
	{ID: "SDK-CASE-034"},
	{ID: "SDK-CASE-035"},
	{ID: "SDK-CASE-036"},
	{ID: "SDK-CASE-037"},
	{ID: "SDK-CASE-038", Optional: true},
}

type coverageSlot struct {
	Provider    string
	CaseID      string
	Requirement string
}

func (c acceptanceCase) requirement(provider string) string {
	optional := c.Optional ||
		(provider == "codex" && c.CodexOptional) ||
		(provider == "claude" && c.ClaudeOptional)
	if optional {
		return "optional"
	}
	return "required"
}

func acceptanceSlots() ([]coverageSlot, error) {
	if len(acceptanceCases) != 38 {
		return nil, fmt.Errorf("acceptance matrix has %d cases; want 38", len(acceptanceCases))
	}
	seen := make(map[string]bool, len(acceptanceCases))
	for _, c := range acceptanceCases {
		if !strings.HasPrefix(c.ID, "SDK-CASE-") || seen[c.ID] {
			return nil, fmt.Errorf("acceptance matrix has invalid or duplicate case %q", c.ID)
		}
		seen[c.ID] = true
	}
	slots := make([]coverageSlot, 0, 76)
	for _, provider := range []string{"codex", "claude"} {
		for _, c := range acceptanceCases {
			slots = append(slots, coverageSlot{
				Provider:    provider,
				CaseID:      c.ID,
				Requirement: c.requirement(provider),
			})
		}
	}
	required, optional := 0, 0
	seenSlots := make(map[string]bool, len(slots))
	for _, slot := range slots {
		key := slot.Provider + "/" + slot.CaseID
		if seenSlots[key] {
			return nil, fmt.Errorf("acceptance matrix repeats slot %s", key)
		}
		seenSlots[key] = true
		switch slot.Requirement {
		case "required":
			required++
		case "optional":
			optional++
		default:
			return nil, fmt.Errorf("acceptance matrix has invalid requirement %q", slot.Requirement)
		}
	}
	if len(slots) != 76 || required != 65 || optional != 11 {
		return nil, fmt.Errorf("acceptance matrix has %d slots (%d required, %d optional); want 76 (65, 11)",
			len(slots), required, optional)
	}
	return slots, nil
}

type coverageSource struct {
	Kind               string `json:"kind,omitempty"`
	ID                 string `json:"id,omitempty"`
	SHA256             string `json:"sha256,omitempty"`
	Provenance         string `json:"provenance,omitempty"`
	DriverProfile      string `json:"driverProfile,omitempty"`
	SelectedProvenance string `json:"selectedProvenance,omitempty"`
}

type coverageMaterial struct {
	ID              string   `json:"id,omitempty"`
	Kind            string   `json:"kind,omitempty"`
	Action          string   `json:"action,omitempty"`
	Provider        string   `json:"provider,omitempty"`
	CaseID          string   `json:"caseId,omitempty"`
	Provenance      string   `json:"provenance,omitempty"`
	PlanSHA256      string   `json:"planSHA256,omitempty"`
	ManifestSHA256  string   `json:"manifestSHA256,omitempty"`
	RecordingSHA256 string   `json:"recordingSHA256,omitempty"`
	Connections     []string `json:"connections,omitempty"`
}

type coverageRow struct {
	Provider         string           `json:"provider"`
	CaseID           string           `json:"caseId"`
	Requirement      string           `json:"requirement"`
	Verification     string           `json:"verification"`
	Adoption         string           `json:"adoption"`
	Source           coverageSource   `json:"source"`
	Material         coverageMaterial `json:"material"`
	MaterialStatus   string           `json:"materialStatus"`
	MissingReason    string           `json:"missingReason,omitempty"`
	SimulationStatus string           `json:"simulationStatus"`
	CoverageEligible bool             `json:"coverageEligible"`
	CoverageNote     string           `json:"coverageNote,omitempty"`
	Output           string           `json:"output,omitempty"`
	Error            string           `json:"error,omitempty"`
	MaterialError    string           `json:"materialError,omitempty"`
	ProtocolError    string           `json:"protocolError,omitempty"`
	BridgeError      string           `json:"bridgeError,omitempty"`
	CleanupError     string           `json:"cleanupError,omitempty"`
	SessionIDs       []string         `json:"sessionIds,omitempty"`
	RunIDs           []string         `json:"runIds,omitempty"`
}

type coverageSelection struct {
	Provider   string `json:"provider"`
	CaseID     string `json:"caseId"`
	Provenance string `json:"provenance,omitempty"`
	PlanSHA256 string `json:"planSHA256,omitempty"`
}

type coverage struct {
	SchemaVersion    int                `json:"schemaVersion"`
	Matrix           coverageMatrix     `json:"matrix"`
	Tool             toolIdentity       `json:"tool"`
	SDK              any                `json:"sdk"`
	Selection        *coverageSelection `json:"selection,omitempty"`
	Cases            []coverageRow      `json:"cases"`
	Counts           map[string]int     `json:"counts"`
	MaterialCounts   map[string]int     `json:"materialCounts"`
	RequiredBlocking int                `json:"requiredBlocking"`
	OptionalBlocking int                `json:"optionalBlocking"`
	MaterialFailed   int                `json:"materialFailed"`
}

type coverageMatrix struct {
	Cases    int `json:"cases"`
	Slots    int `json:"slots"`
	Required int `json:"required"`
	Optional int `json:"optional"`
}

type externalSelection struct {
	slot       coverageSlot
	provenance string
	planSHA256 string
}

func gate(ctx context.Context, tool toolIdentity, o options) error {
	slots, err := acceptanceSlots()
	if err != nil {
		return err
	}
	var external *externalSelection
	if o.Recording != "" {
		external, err = selectExternalRecording(o.Recording, slots)
		if err != nil {
			return err
		}
	}
	report := newCoverageReport(tool, o, slots, external)
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range o.Jobs {
		wg.Go(func() {
			for i := range jobs {
				report.Cases[i] = evaluateSlot(ctx, tool, o, slots[i], external)
			}
		})
	}
	for i := range slots {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	summarizeCoverage(&report)
	var failures []error
	for _, row := range report.Cases {
		if failure := coverageRowFailure(row); failure != nil {
			failures = append(failures, failure)
		}
	}
	writeErr := writeJSON(filepath.Join(o.Output, "coverage.json"), report)
	return errors.Join(errors.Join(failures...), writeErr)
}

func newCoverageReport(tool toolIdentity, o options, slots []coverageSlot, external *externalSelection) coverage {
	required, optional := 0, 0
	cases := make([]coverageRow, len(slots))
	for i, slot := range slots {
		cases[i] = coverageRow{
			Provider:         slot.Provider,
			CaseID:           slot.CaseID,
			Requirement:      slot.Requirement,
			Verification:     verificationUnknown,
			Adoption:         adoptionUnknown,
			MaterialStatus:   materialStatusNotEvaluated,
			SimulationStatus: "unverified",
		}
		if slot.Requirement == "required" {
			required++
		} else {
			optional++
		}
	}
	report := coverage{
		SchemaVersion: coverageSchemaVersion,
		Matrix: coverageMatrix{
			Cases:    len(acceptanceCases),
			Slots:    len(slots),
			Required: required,
			Optional: optional,
		},
		Tool:           tool,
		SDK:            sdkIdentity(o.Driver),
		Cases:          cases,
		Counts:         map[string]int{},
		MaterialCounts: map[string]int{},
	}
	if external != nil {
		report.Selection = &coverageSelection{
			Provider:   external.slot.Provider,
			CaseID:     external.slot.CaseID,
			Provenance: external.provenance,
			PlanSHA256: external.planSHA256,
		}
	}
	return report
}

func selectExternalRecording(path string, slots []coverageSlot) (*externalSelection, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("--recording must be absolute")
	}
	raw, err := os.ReadFile(filepath.Join(path, "plan.json"))
	if err != nil {
		return nil, fmt.Errorf("read external recording plan: %w", err)
	}
	var plan struct {
		Provider   string `json:"provider"`
		CaseID     string `json:"caseId"`
		Provenance string `json:"provenance"`
	}
	if err := json.Unmarshal(raw, &plan); err != nil {
		return nil, fmt.Errorf("external recording plan: %w", err)
	}
	if plan.Provider != "codex" && plan.Provider != "claude" {
		return nil, fmt.Errorf("external recording has unsupported provider %q", plan.Provider)
	}
	if plan.CaseID == "" {
		return nil, errors.New("external recording has no case ID")
	}
	for _, slot := range slots {
		if slot.Provider == plan.Provider && slot.CaseID == plan.CaseID {
			return &externalSelection{
				slot:       slot,
				provenance: plan.Provenance,
				planSHA256: digest(raw),
			}, nil
		}
	}
	return nil, fmt.Errorf("external recording %s/%s is absent from the 76-slot matrix", plan.Provider, plan.CaseID)
}

func evaluateSlot(ctx context.Context, tool toolIdentity, o options, slot coverageSlot, external *externalSelection) coverageRow {
	row := coverageRow{
		Provider:         slot.Provider,
		CaseID:           slot.CaseID,
		Requirement:      slot.Requirement,
		Verification:     verificationUnknown,
		Adoption:         adoptionUnknown,
		MaterialStatus:   materialStatusNotEvaluated,
		SimulationStatus: "unverified",
	}
	selected := external == nil ||
		(external.slot.Provider == slot.Provider && external.slot.CaseID == slot.CaseID)
	if !selected {
		row.Error = "not selected in external-recording gate"
		return row
	}
	child := o
	child.Output = filepath.Join(o.Output, slot.Provider+"-"+slot.CaseID)
	source := sourceSelection{Provider: slot.Provider, Case: slot.CaseID}
	if external != nil {
		source = sourceSelection{Recording: o.Recording}
	}
	if err := os.Mkdir(child.Output, 0o700); err != nil {
		row.Output = child.Output
		row.Error = err.Error()
		row.MaterialStatus = materialStatusInvalid
		row.MaterialError = err.Error()
		return row
	}
	row.Output = child.Output
	simulationErr := simulate(ctx, tool, child, source)
	var result summary
	if err := readJSON(filepath.Join(child.Output, "summary.json"), &result); err != nil {
		row.Error = errors.Join(simulationErr, err).Error()
		row.MaterialStatus = materialStatusInvalid
		row.MaterialError = err.Error()
		return row
	}
	completeCoverageRow(&row, result, simulationErr)
	if external != nil {
		row.CoverageEligible = false
		row.CoverageNote = "external recording selection does not qualify real recording coverage"
		row.Source.SelectedProvenance = external.provenance
	}
	return row
}

func completeCoverageRow(row *coverageRow, result summary, simulationErr error) {
	row.SimulationStatus = result.Status
	if simulationErr != nil {
		row.Error = simulationErr.Error()
	} else {
		row.Error = result.Error
	}
	if len(result.Runs) == 0 {
		row.MaterialStatus = materialStatusInvalid
		row.MaterialError = "single-case runner produced no prepared session result"
		return
	}
	var materialErrors, protocolErrors, bridgeErrors, cleanupErrors []string
	statuses := map[string]bool{}
	for i, run := range result.Runs {
		if i == 0 || row.Source.Kind == "" {
			row.Source = run.Source
		}
		row.Material = run.Material
		statuses[run.MaterialStatus] = true
		if run.MaterialError != "" {
			materialErrors = append(materialErrors, run.MaterialError)
		}
		if run.ProtocolError != "" {
			protocolErrors = append(protocolErrors, run.ProtocolError)
		}
		if run.BridgeError != "" {
			bridgeErrors = append(bridgeErrors, run.BridgeError)
		}
		if run.CleanupError != "" {
			cleanupErrors = append(cleanupErrors, run.CleanupError)
		}
		if run.SessionID != "" {
			row.SessionIDs = append(row.SessionIDs, run.SessionID)
		}
		if run.RunID != "" {
			row.RunIDs = append(row.RunIDs, run.RunID)
		}
	}
	row.MaterialError = strings.Join(uniqueStrings(materialErrors), "; ")
	row.ProtocolError = strings.Join(uniqueStrings(protocolErrors), "; ")
	row.BridgeError = strings.Join(uniqueStrings(bridgeErrors), "; ")
	row.CleanupError = strings.Join(uniqueStrings(cleanupErrors), "; ")
	switch {
	case statuses[materialStatusInvalid]:
		row.MaterialStatus = materialStatusInvalid
	case statuses[materialStatusAvailable]:
		row.MaterialStatus = materialStatusAvailable
	case statuses[materialStatusUnavailable]:
		row.MaterialStatus = materialStatusUnavailable
	default:
		row.MaterialStatus = materialStatusInvalid
		row.MaterialError = "single-case runner did not report material status"
	}
	if row.MaterialStatus == materialStatusUnavailable && row.CleanupError == "" {
		row.SimulationStatus = "unverified"
		row.MissingReason = row.MaterialError
		if row.MissingReason == "" {
			row.MissingReason = row.Error
		}
	}
	row.CoverageEligible = coverageEligible(*row)
}

func coverageEligible(row coverageRow) bool {
	return row.MaterialStatus == materialStatusAvailable &&
		row.Source.Kind == "case" &&
		row.Source.Provenance == "official" &&
		row.Source.ID == row.CaseID &&
		row.Material.Kind == "material" &&
		row.Material.Provenance == "builtin" &&
		row.Material.Provider == row.Provider &&
		row.Material.CaseID == row.CaseID &&
		row.Material.PlanSHA256 != ""
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func summarizeCoverage(report *coverage) {
	for _, row := range report.Cases {
		report.Counts[row.SimulationStatus]++
		report.MaterialCounts[row.MaterialStatus]++
		switch row.Requirement {
		case "required":
			if !row.CoverageEligible || row.SimulationStatus != "passed" {
				report.RequiredBlocking++
			}
		case "optional":
			if row.MaterialError != "" || row.CleanupError != "" {
				report.OptionalBlocking++
			}
		}
		if row.MaterialError != "" {
			report.MaterialFailed++
		}
	}
}

func coverageRowFailure(row coverageRow) error {
	materialInvalid := row.MaterialError != ""
	cleanupFailed := row.CleanupError != ""
	requiredFailed := row.Requirement == "required" &&
		(!row.CoverageEligible || row.SimulationStatus != "passed")
	if !materialInvalid && !cleanupFailed && !requiredFailed {
		return nil
	}
	detail := row.Error
	if detail == "" {
		detail = row.MissingReason
	}
	return fmt.Errorf("%s/%s requires=%s material=%s coverage=%t SDK=%s: %s",
		row.Provider, row.CaseID, row.Requirement, row.MaterialStatus,
		row.CoverageEligible, row.SimulationStatus, detail)
}
