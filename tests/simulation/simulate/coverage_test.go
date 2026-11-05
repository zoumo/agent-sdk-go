package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAcceptanceMatrixMatchesSharedPolicy(t *testing.T) {
	slots, err := acceptanceSlots()
	require.NoError(t, err)
	require.Len(t, slots, 72)

	counts := map[string]int{}
	optional := map[string]bool{}
	for _, slot := range slots {
		counts[slot.Requirement]++
		if slot.Requirement == "optional" {
			optional[slot.Provider+"/"+slot.CaseID] = true
		}
	}
	require.Equal(t, map[string]int{"required": 63, "optional": 9}, counts)

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
		"codex/SDK-CASE-003",
		"codex/SDK-CASE-012",
		"codex/SDK-CASE-025",
		"codex/SDK-CASE-026",
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
	require.ErrorContains(t, err, "absent from the 72-slot matrix")

	require.NoError(t, os.WriteFile(filepath.Join(root, "plan.json"), []byte("{"), 0o600))
	_, err = selectExternalRecording(root, slots)
	require.ErrorContains(t, err, "external recording plan")
}

func TestGatePreparedSessionsAreConcurrentAndAlwaysCleaned(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "calls.log")
	tool := fakeUnavailableTestify(t)
	output := filepath.Join(t.TempDir(), "gate")
	cmd := exec.CommandContext(t.Context(), runnerBinary,
		"--gate", "--jobs", "4", "--output", output, "--driver", driverBinary)
	cmd.Env = append(os.Environ(), "AGENT_SDK_TESTIFY_BIN="+tool, "FAKE_TESTIFY_LOG="+logPath)
	b, err := cmd.CombinedOutput()
	require.Error(t, err, string(b))

	var report coverage
	require.NoError(t, readJSON(filepath.Join(output, "coverage.json"), &report))
	require.Equal(t, coverageSchemaVersion, report.SchemaVersion)
	require.Equal(t, coverageMatrix{Cases: 36, Slots: 72, Required: 63, Optional: 9}, report.Matrix)
	require.Len(t, report.Cases, 72)
	require.Equal(t, 63, report.RequiredBlocking)
	for _, row := range report.Cases {
		require.Equal(t, materialStatusUnavailable, row.MaterialStatus,
			"%s/%s: %s", row.Provider, row.CaseID, row.Error)
		require.Equal(t, "unverified", row.SimulationStatus)
		require.False(t, row.CoverageEligible)
		require.Contains(t, row.MissingReason, "session has no current recording")
		require.Len(t, row.SessionIDs, 1)
		require.Empty(t, row.CleanupError)
	}
	require.Equal(t, 0, report.MaterialFailed)
	require.Equal(t, map[string]int{"unverified": 72}, report.Counts)
	require.Equal(t, map[string]int{materialStatusUnavailable: 72}, report.MaterialCounts)
	require.Equal(t, 0, report.OptionalBlocking)

	calls, readErr := os.ReadFile(logPath)
	require.NoError(t, readErr)
	var prepares, finishes, versions int
	for line := range strings.SplitSeq(strings.TrimSpace(string(calls)), "\n") {
		switch line {
		case "prepare":
			prepares++
		case "finish":
			finishes++
		case "version":
			versions++
		}
	}
	require.Equal(t, 1, versions)
	require.Equal(t, 72, prepares)
	require.Equal(t, 0, finishes)
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
	require.Len(t, report.Cases, 72)
	require.Equal(t, map[string]int{"passed": 1, "unverified": 71}, report.Counts)
	require.Equal(t, 63, report.RequiredBlocking)
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
	require.Contains(t, string(b), "session has no current recording")

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
	require.Equal(t, map[string]int{"failed": 1, "unverified": 71}, report.Counts)
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

func fakeUnavailableTestify(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-testify")
	script := `#!/bin/sh
set -eu
hashfile() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}
cmd=$1
shift
printf '%s\n' "$cmd" >> "$FAKE_TESTIFY_LOG"
case "$cmd" in
version)
  echo 'fake 0.1.0'
  ;;
prepare)
  agent=''
  caseid=''
  dir=''
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --agent) agent=$2; shift 2 ;;
      --case) caseid=$2; shift 2 ;;
      --session-dir) dir=$2; shift 2 ;;
      *) shift ;;
    esac
  done
  mkdir -p "$dir/source" "$dir/runtime/baseline/workspace" "$dir/runtime/baseline/home" "$dir/runtime/baseline/config" "$dir/runtime/baseline/tmp" "$dir/runtime/routes/mock/workspace" "$dir/runtime/routes/mock/protocol" "$dir/executions"
  dir=$(cd "$dir" && pwd -P)
  printf '{"provider":"%s","caseId":"%s","driverProfile":"structured-input"}\n' "$agent" "$caseid" > "$dir/source/plan.json"
  printf '#!/bin/sh\nexit 0\n' > "$dir/launcher"
  chmod 700 "$dir/launcher"
  planhash=$(hashfile "$dir/source/plan.json")
  toolhash=$(hashfile "$0")
  sid="session-$agent-$caseid"
  cat > "$dir/session.json" <<EOF
{
  "schemaVersion": 3,
  "sessionId": "$sid",
  "tool": {"command": "$0", "args": []},
  "toolVersion": "0.1.0",
  "toolSHA256": "$toolhash",
  "agentType": "$agent",
  "source": {
    "kind": "case",
    "id": "$caseid",
    "path": "$dir/source/plan.json",
    "sha256": "$planhash",
    "provenance": "official",
    "driverProfile": "structured-input"
  },
  "resourceRoot": "$dir/runtime",
  "workspace": "$dir/runtime/baseline/workspace",
  "home": "$dir/runtime/baseline/home",
  "config": "$dir/runtime/baseline/config",
  "tmp": "$dir/runtime/baseline/tmp",
  "protocolCwd": "$dir/runtime/baseline/workspace",
  "env": {"FAKE_ROUTE": "1"},
  "routes": {
    "mock": {
      "enabled": true,
      "executor": "external-sdk",
      "target": "replay",
      "provenance": "official",
      "workspace": "$dir/runtime/routes/mock/workspace",
      "protocolCwd": "$dir/runtime/routes/mock/protocol",
      "env": {"FAKE_ROUTE": "1"},
      "connections": [{
        "id": "main",
        "command": "$0",
        "args": ["agent", "mock", "--session-dir", "$dir", "--connection", "main"],
        "launcher": "$dir/launcher"
      }]
    }
  }
}
EOF
  printf '{"schemaVersion":3,"sessionId":"%s","sessionSHA256":"fake","executions":[]}\n' "$sid" > "$dir/executions/index.json"
  ;;
export)
  echo 'session has no current recording' >&2
  exit 1
  ;;
esac
`
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
	return path
}

func TestGateRejectsExternalSelectionOutsideMatrixWithoutPreparing(t *testing.T) {
	source := controlledCapture(t)
	output := filepath.Join(t.TempDir(), "gate")
	cmd := exec.CommandContext(t.Context(), runnerBinary,
		"--gate", "--recording", source, "--output", output, "--driver", driverBinary)
	b, err := cmd.CombinedOutput()
	require.Error(t, err, string(b))
	require.Contains(t, string(b), "absent from the 72-slot matrix")
	require.NoDirExists(t, filepath.Join(output, "codex-sdk-synthetic-codex"))
}
