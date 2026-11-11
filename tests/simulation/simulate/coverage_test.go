package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAcceptanceMatrixMatchesSharedPolicy(t *testing.T) {
	slots, err := acceptanceSlots()
	require.NoError(t, err)
	require.Len(t, slots, 76)

	counts := map[string]int{}
	optional := map[string]bool{}
	for _, slot := range slots {
		counts[slot.Requirement]++
		if slot.Requirement == "optional" {
			optional[slot.Provider+"/"+slot.CaseID] = true
		}
	}
	require.Equal(t, map[string]int{"required": 65, "optional": 11}, counts)

	var got []string
	for key := range optional {
		got = append(got, key)
	}
	sort.Strings(got)
	require.Equal(t, []string{
		"claude/SDK-CASE-003",
		"claude/SDK-CASE-017",
		"claude/SDK-CASE-025",
		"claude/SDK-CASE-026",
		"claude/SDK-CASE-032",
		"claude/SDK-CASE-038",
		"codex/SDK-CASE-003",
		"codex/SDK-CASE-012",
		"codex/SDK-CASE-025",
		"codex/SDK-CASE-026",
		"codex/SDK-CASE-038",
	}, got)

	for i, c := range acceptanceCases {
		require.Equal(t, fmt.Sprintf("SDK-CASE-%03d", i+1), c.ID)
	}
}

func TestCoverageFailureKeepsOptionalBusinessVisibleButRejectsIntegrity(t *testing.T) {
	for _, tc := range []struct {
		name string
		row  coverageRow
		fail bool
	}{
		{
			name: "optional business failure",
			row: coverageRow{
				Requirement: "optional", MaterialStatus: materialStatusAvailable,
				SimulationStatus: "failed",
			},
		},
		{
			name: "optional unavailable",
			row: coverageRow{
				Requirement: "optional", MaterialStatus: materialStatusUnavailable,
				SimulationStatus: "unverified",
			},
		},
		{
			name: "optional material corruption",
			row: coverageRow{
				Requirement: "optional", MaterialStatus: materialStatusInvalid,
				MaterialError: "hash mismatch", SimulationStatus: "failed",
			},
			fail: true,
		},
		{
			name: "optional cleanup failure",
			row: coverageRow{
				Requirement: "optional", MaterialStatus: materialStatusAvailable,
				CleanupError: "session cleanup failed", SimulationStatus: "failed",
			},
			fail: true,
		},
		{
			name: "required external pass is not qualified",
			row: coverageRow{
				Provider: "codex", CaseID: "SDK-CASE-001",
				Requirement: "required", MaterialStatus: materialStatusAvailable,
				Source: coverageSource{
					Kind: "case", ID: "SDK-CASE-001", Provenance: "official",
				},
				Material: coverageMaterial{
					Kind: "material", Provider: "codex", CaseID: "SDK-CASE-001",
					Provenance: "external", PlanSHA256: "plan",
				},
				SimulationStatus: "passed", CoverageEligible: false,
			},
			fail: true,
		},
		{
			name: "required builtin pass",
			row: coverageRow{
				Provider: "codex", CaseID: "SDK-CASE-001",
				Requirement: "required", MaterialStatus: materialStatusAvailable,
				Source: coverageSource{
					Kind: "case", ID: "SDK-CASE-001", Provenance: "official",
				},
				Material: coverageMaterial{
					Kind: "material", Provider: "codex", CaseID: "SDK-CASE-001",
					Provenance: "builtin", PlanSHA256: "plan",
				},
				SimulationStatus: "passed", CoverageEligible: true,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := coverageRowFailure(tc.row)
			if tc.fail {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCoverageEligibilityRequiresExportedBuiltinIdentity(t *testing.T) {
	base := coverageRow{
		Provider: "codex", CaseID: "SDK-CASE-001",
		MaterialStatus: materialStatusAvailable,
		Source: coverageSource{
			Kind: "case", ID: "SDK-CASE-001", Provenance: "official",
		},
		Material: coverageMaterial{
			Kind: "material", Provider: "codex", CaseID: "SDK-CASE-001",
			Provenance: "builtin", PlanSHA256: "plan",
		},
	}
	require.True(t, coverageEligible(base))

	for _, tc := range []struct {
		name   string
		mutate func(*coverageRow)
	}{
		{name: "outside official template", mutate: func(row *coverageRow) { row.Source.Provenance = "external" }},
		{name: "synthetic material", mutate: func(row *coverageRow) { row.Material.Provenance = "synthetic" }},
		{name: "consumer material", mutate: func(row *coverageRow) { row.Material.Provenance = "consumer" }},
		{name: "external capture", mutate: func(row *coverageRow) { row.Material.Kind = "capture" }},
		{name: "provider mismatch", mutate: func(row *coverageRow) { row.Material.Provider = "claude" }},
		{name: "case mismatch", mutate: func(row *coverageRow) { row.Material.CaseID = "SDK-CASE-002" }},
		{name: "source case mismatch", mutate: func(row *coverageRow) { row.Source.ID = "SDK-CASE-002" }},
		{name: "missing plan hash", mutate: func(row *coverageRow) { row.Material.PlanSHA256 = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := base
			tc.mutate(&row)
			require.False(t, coverageEligible(row))
		})
	}
}

func TestCoverageCountsOptionalBusinessFailuresWithoutCallingThemBlocking(t *testing.T) {
	report := coverage{
		Cases: []coverageRow{
			{
				Provider: "codex", CaseID: "SDK-CASE-003", Requirement: "optional",
				MaterialStatus: materialStatusAvailable, SimulationStatus: "failed",
			},
			{
				Provider: "codex", CaseID: "SDK-CASE-004", Requirement: "required",
				MaterialStatus: materialStatusUnavailable, SimulationStatus: "unverified",
			},
		},
		Counts:         map[string]int{},
		MaterialCounts: map[string]int{},
	}
	summarizeCoverage(&report)
	require.Equal(t, 1, report.Counts["failed"])
	require.Equal(t, 1, report.Counts["unverified"])
	require.Equal(t, 0, report.OptionalBlocking)
	require.Equal(t, 1, report.RequiredBlocking)
}

func TestExternalSelectionUsesMatrixAndPreservesProvenance(t *testing.T) {
	slots, err := acceptanceSlots()
	require.NoError(t, err)
	root := t.TempDir()
	writeJSONTest(t, filepath.Join(root, "plan.json"), map[string]any{
		"provider": "codex", "caseId": "SDK-CASE-001", "provenance": "synthetic",
	})
	selection, err := selectExternalRecording(root, slots)
	require.NoError(t, err)
	require.Equal(t, "codex", selection.slot.Provider)
	require.Equal(t, "SDK-CASE-001", selection.slot.CaseID)
	require.Equal(t, "synthetic", selection.provenance)

	writeJSONTest(t, filepath.Join(root, "plan.json"), map[string]any{
		"provider": "codex", "caseId": "custom-structured", "provenance": "synthetic",
	})
	_, err = selectExternalRecording(root, slots)
	require.ErrorContains(t, err, "absent from the 76-slot matrix")

	require.NoError(t, os.WriteFile(filepath.Join(root, "plan.json"), []byte("{"), 0o600))
	_, err = selectExternalRecording(root, slots)
	require.ErrorContains(t, err, "external recording plan")
}

func TestGatePreparedSessionsAreConcurrentAndAlwaysCleaned(t *testing.T) {
	output := filepath.Join(t.TempDir(), "gate")
	cmd := exec.CommandContext(t.Context(), runnerBinary, "--gate", "--jobs", "4", "--output", output, "--driver", driverBinary)
	b, err := cmd.CombinedOutput()
	var report coverage
	require.NoError(t, readJSON(filepath.Join(output, "coverage.json"), &report))
	if report.RequiredBlocking > 0 || report.OptionalBlocking > 0 {
		require.Error(t, err, string(b))
	} else {
		require.NoError(t, err, string(b))
	}
	require.Equal(t, coverageSchemaVersion, report.SchemaVersion)
	require.Equal(t, coverageMatrix{Cases: 38, Slots: 76, Required: 65, Optional: 11}, report.Matrix)
	require.Len(t, report.Cases, 76)
	for _, row := range report.Cases {
		require.Empty(t, row.CleanupError, "%s/%s", row.Provider, row.CaseID)
		if row.MaterialStatus == materialStatusUnavailable {
			require.Equal(t, "unverified", row.SimulationStatus)
			require.False(t, row.CoverageEligible)
			require.NotEmpty(t, row.MissingReason)
		}
		for _, policy := range []string{"preserve", "suppress"} {
			require.NoDirExists(t, filepath.Join(output, row.Provider+"-"+row.CaseID, policy, "session"))
		}
	}
	require.Zero(t, report.MaterialFailed)
	require.Equal(t, 76, report.Counts["passed"]+report.Counts["failed"]+report.Counts["unverified"])
	for _, key := range []string{
		"codex/SDK-CASE-037", "codex/SDK-CASE-038",
		"claude/SDK-CASE-037", "claude/SDK-CASE-038",
	} {
		row := coverageRowByKey(t, report, key)
		require.Equal(t, "passed", row.SimulationStatus, "%s: %s", key, row.Error)
		require.Equal(t, materialStatusAvailable, row.MaterialStatus, key)
		require.Equal(t, "official", row.Source.Provenance, key)
		require.Equal(t, "builtin", row.Material.Provenance, key)
		require.True(t, row.CoverageEligible, key)
	}
}

func TestGateSyntheticRecordingLeavesCoverageUnverified(t *testing.T) {
	source := gateRecordingCapture(t)
	output := filepath.Join(t.TempDir(), "gate")
	cmd := exec.CommandContext(t.Context(), runnerBinary,
		"--gate", "--recording", source, "--jobs", "4",
		"--output", output, "--driver", driverBinary)
	b, err := cmd.CombinedOutput()
	require.Error(t, err, string(b))

	var report coverage
	require.NoError(t, readJSON(filepath.Join(output, "coverage.json"), &report))
	require.Len(t, report.Cases, 76)
	require.Equal(t, map[string]int{"passed": 1, "unverified": 75}, report.Counts)
	require.Equal(t, 65, report.RequiredBlocking)
	require.NotNil(t, report.Selection)
	require.Equal(t, "synthetic", report.Selection.Provenance)

	selected := coverageRowByKey(t, report, "codex/SDK-CASE-001")
	require.Equal(t, "passed", selected.SimulationStatus)
	require.False(t, selected.CoverageEligible)
	require.Equal(t, "synthetic", selected.Source.SelectedProvenance)
	require.Equal(t, "external", selected.Source.Provenance)
	require.Equal(t, "external", selected.Material.Provenance)
	require.Empty(t, selected.CleanupError)
	require.Len(t, selected.SessionIDs, 1)
	require.NoDirExists(t, filepath.Join(output, "codex-SDK-CASE-001", "preserve", "session", "root"))

	other := coverageRowByKey(t, report, "claude/SDK-CASE-036")
	require.Equal(t, "unverified", other.SimulationStatus)
	require.Equal(t, materialStatusNotEvaluated, other.MaterialStatus)
	require.False(t, other.CoverageEligible)
}

func TestClaudeRemoteWithoutCurrentMaterialIsUnavailable(t *testing.T) {
	output := filepath.Join(t.TempDir(), "run")
	cmd := exec.CommandContext(t.Context(), runnerBinary,
		"--provider", "claude", "--case", "SDK-CASE-032",
		"--output", output, "--driver", driverBinary)
	b, err := cmd.CombinedOutput()
	require.Error(t, err, string(b))
	require.Contains(t, string(b), "fixture has no current recording")

	var result summary
	require.NoError(t, readJSON(filepath.Join(output, "summary.json"), &result))
	require.Len(t, result.Runs, 1)
	run := result.Runs[0]
	require.Equal(t, materialStatusUnavailable, run.MaterialStatus)
	require.Empty(t, run.MaterialError)
	require.Empty(t, run.CleanupError)
}

func TestGateCancellationCleansSelectedSession(t *testing.T) {
	source := gateRecordingCapture(t)
	output := filepath.Join(t.TempDir(), "gate")
	host := filepath.Join(t.TempDir(), "idle-host")
	hostReady := filepath.Join(t.TempDir(), "host-ready")
	require.NoError(t, os.WriteFile(host,
		[]byte("#!/bin/sh\nprintf ready > "+quote(hostReady)+"\nsleep 60\n"), 0o700))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, runnerBinary,
		"--gate", "--recording", source, "--output", output, "--driver", host)
	require.NoError(t, cmd.Start())
	require.Eventually(t, func() bool {
		_, err := os.Stat(hostReady)
		return err == nil
	}, 10*time.Second, 10*time.Millisecond)

	prepared, err := readPreparedSession(filepath.Join(output, "codex-SDK-CASE-001", "preserve", "session"))
	require.NoError(t, err)
	require.NoError(t, cmd.Process.Signal(os.Interrupt))
	require.Error(t, cmd.Wait())
	require.NoError(t, ctx.Err())
	require.NoDirExists(t, prepared.ResourceRoot)

	evidence := readSessionEvidence(t, filepath.Join(output, "codex-SDK-CASE-001", "preserve", "evidence.json"))
	require.Equal(t, "abandoned", evidence.Status)

	var report coverage
	require.NoError(t, readJSON(filepath.Join(output, "coverage.json"), &report))
	require.Equal(t, map[string]int{"failed": 1, "unverified": 75}, report.Counts)
	selected := coverageRowByKey(t, report, "codex/SDK-CASE-001")
	require.Equal(t, "failed", selected.SimulationStatus)
	require.Empty(t, selected.CleanupError)
}

func gateRecordingCapture(t *testing.T) string {
	t.Helper()
	source := controlledCapture(t)
	var plan map[string]any
	require.NoError(t, readJSON(filepath.Join(source, "plan.json"), &plan))
	plan["caseId"] = "SDK-CASE-001"
	plan["driverProfile"] = "structured-input"
	writeJSONTest(t, filepath.Join(source, "plan.json"), plan)
	refreshSyntheticBinding(t, source)
	return source
}

func coverageRowByKey(t *testing.T, report coverage, key string) coverageRow {
	t.Helper()
	for _, row := range report.Cases {
		if row.Provider+"/"+row.CaseID == key {
			return row
		}
	}
	require.FailNow(t, "coverage row not found", key)
	return coverageRow{}
}

func TestGateRejectsExternalSelectionOutsideMatrixWithoutPreparing(t *testing.T) {
	source := controlledCapture(t)
	output := filepath.Join(t.TempDir(), "gate")
	cmd := exec.CommandContext(t.Context(), runnerBinary,
		"--gate", "--recording", source, "--output", output, "--driver", driverBinary)
	b, err := cmd.CombinedOutput()
	require.Error(t, err, string(b))
	require.Contains(t, string(b), "absent from the 76-slot matrix")
	require.NoDirExists(t, filepath.Join(output, "codex-sdk-synthetic-codex"))
}
