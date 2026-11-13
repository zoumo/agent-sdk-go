package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	fixturedata "github.com/zoumo/agent-sdk-go/codex/tests/testdata"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

var runnerBinary, driverBinary, requestMutatorBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sdk-simulate-tests-")
	if err != nil {
		panic(err)
	}
	runnerBinary = filepath.Join(dir, "simulate")
	driverBinary = filepath.Join(dir, "driver")
	requestMutatorBinary = filepath.Join(dir, "mutate-request")
	for _, entry := range [][2]string{
		{runnerBinary, "."},
		{driverBinary, "../drivers"},
		{requestMutatorBinary, "./testdata/mutate-request"},
	} {
		cmd := exec.CommandContext(context.Background(), "go", "build", "-o", entry[0], entry[1])
		if b, e := cmd.CombinedOutput(); e != nil {
			os.Stderr.Write(b)
			os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func writeJSONTest(t *testing.T, path string, v any) {
	t.Helper()
	b, e := json.Marshal(v)
	require.NoError(t, e)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, b, 0o600))
}

func controlledCapture(t *testing.T) string {
	t.Helper()
	lifecycle, err := fixturedata.Files.ReadFile("integration/lifecycle.jsonl")
	require.NoError(t, err)
	rows := bytes.Split(bytes.TrimSpace(lifecycle), []byte("\n"))[:5]
	frames := make([]json.RawMessage, 0, len(rows)+3)
	for _, row := range rows {
		frames = append(frames, append([]byte(nil), row...))
	}
	for i, wire := range []string{
		`{"id":103,"method":"turn/start","params":{"threadId":"thread-001","input":[{"type":"text","text":"synthetic task"}]}}`,
		`{"id":103,"result":{"turn":{"id":"turn-001","items":[],"status":"inProgress"}}}`,
		`{"method":"turn/completed","params":{"threadId":"thread-001","turn":{"id":"turn-001","items":[],"status":"completed"}}}`,
	} {
		direction := "s2c"
		if i == 0 {
			direction = "c2s"
		}
		row, err := json.Marshal(map[string]any{
			"direction": direction,
			"channel":   "protocol",
			"epoch":     1,
			"elapsedMs": 0,
			"wire":      json.RawMessage(wire),
		})
		require.NoError(t, err)
		frames = append(frames, row)
	}
	envelope, err := json.Marshal(map[string]any{"version": 1, "frames": frames})
	require.NoError(t, err)
	capture, err := mocktest.Capture(t.TempDir(), "codex", envelope, mocktest.Options{})
	require.NoError(t, err)

	var plan map[string]any
	require.NoError(t, readJSON(filepath.Join(capture, "plan.json"), &plan))
	plan["caseId"] = "custom-structured"
	plan["driverProfile"] = "structured-input"
	plan["scenario"] = map[string]any{"files": map[string]string{"numbers.txt": "7\n11\n"}}
	writeJSONTest(t, filepath.Join(capture, "plan.json"), plan)
	refreshSyntheticBinding(t, capture)
	return capture
}

func refreshSyntheticBinding(t *testing.T, capture string) {
	t.Helper()
	plan, err := os.ReadFile(filepath.Join(capture, "plan.json"))
	require.NoError(t, err)
	var manifest map[string]any
	require.NoError(t, readJSON(filepath.Join(capture, "manifest.json"), &manifest))
	manifest["planSHA256"] = digest(plan)
	writeJSONTest(t, filepath.Join(capture, "manifest.json"), manifest)
}

func TestSDKRequestUsesLibraryOwnedStdio(t *testing.T) {
	source := controlledCapture(t)
	output := filepath.Join(t.TempDir(), "run")
	cmd := exec.CommandContext(t.Context(), runnerBinary, "--recording", source, "--output", output, "--driver", driverBinary)
	b, err := cmd.CombinedOutput()
	require.NoError(t, err, string(b))

	var result summary
	require.NoError(t, readJSON(filepath.Join(output, "summary.json"), &result))
	require.Equal(t, "passed", result.Status)
	require.Len(t, result.Runs, 1)
	require.NotEmpty(t, result.Runs[0].RunID)
	require.NotEmpty(t, result.Runs[0].SessionID)
	require.Equal(t, "codex", result.Runs[0].Provider)
	require.Empty(t, result.Runs[0].ProtocolError)
	require.Empty(t, result.Runs[0].CleanupError)
	evidence := readSessionEvidence(t, filepath.Join(output, "preserve", "evidence.json"))
	require.Equal(t, "passed", evidence.Status)
	require.Len(t, evidence.Executions, 1)
	require.Equal(t, "in-process", evidence.Executions[0].Connections[0].Transport)
	require.True(t, evidence.Executions[0].Connections[0].FullConsumption)
	require.True(t, evidence.Executions[0].Connections[0].ClientEOF)
	require.True(t, evidence.Executions[0].Connections[0].CleanupComplete)
	require.False(t, evidence.Executions[0].Connections[0].NativeLaunched)
	require.Empty(t, evidence.Executions[0].Connections[0].ProcessReceiptPath)
	require.True(t, evidence.Executions[0].Complete)
	require.Nil(t, evidence.Executions[0].Connections[0].ExitCode)
	var request struct{ Peer map[string]any }
	require.NoError(t, readJSON(filepath.Join(output, "preserve", "sdk", "request.json"), &request))
	parent, err := filepath.EvalSymlinks(filepath.Join(output, "preserve"))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"fixtureDir": filepath.Join(parent, "session"), "connection": "main"}, request.Peer)
}

func TestSourceSuppressPolicyStillRunsAndComparesBothPolicies(t *testing.T) {
	source := controlledChildCapture(t)
	output := filepath.Join(t.TempDir(), "run")
	cmd := exec.CommandContext(t.Context(), runnerBinary, "--recording", source, "--output", output, "--driver", driverBinary)
	b, err := cmd.CombinedOutput()
	require.NoError(t, err, string(b))

	var result summary
	require.NoError(t, readJSON(filepath.Join(output, "summary.json"), &result))
	require.Equal(t, "passed", result.Status)
	require.Len(t, result.Runs, 2)
	require.Equal(t, "suppress", result.Runs[0].ChildDetailPolicy)
	require.Equal(t, "preserve", result.Runs[0].Policy)
	require.Equal(t, "suppress", result.Runs[1].Policy)
	require.NotEqual(t, result.Runs[0].RunID, result.Runs[1].RunID)
	require.NotEqual(t, result.Runs[0].SessionID, result.Runs[1].SessionID)
	require.FileExists(t, filepath.Join(output, "preserve", "sdk", "deliveries.jsonl"))
	require.FileExists(t, filepath.Join(output, "suppress", "sdk", "deliveries.jsonl"))
}

func controlledChildCapture(t *testing.T) string {
	t.Helper()
	root := controlledCapture(t)
	raw, err := os.ReadFile(filepath.Join(root, "raw", "raw.jsonl"))
	require.NoError(t, err)
	envelope, synthetic, err := decodeSyntheticEnvelope(raw)
	require.NoError(t, err)
	require.True(t, synthetic)
	rows := envelope.Frames
	require.GreaterOrEqual(t, len(rows), 8)
	var rootFrame map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rows[4], &rootFrame))
	var started struct {
		Result struct{ Thread map[string]any }
	}
	require.NoError(t, json.Unmarshal(rootFrame["wire"], &started))
	child := started.Result.Thread
	child["id"] = "child"
	child["parentThreadId"] = "thread-001"
	terminal := rows[len(rows)-1]
	rows = rows[:len(rows)-1]
	for _, wire := range []any{
		map[string]any{"method": "thread/started", "params": map[string]any{"thread": child}},
		json.RawMessage(`{"method":"item/agentMessage/delta","params":{"threadId":"child","turnId":"child-turn","itemId":"detail","delta":"hello"}}`),
		func() json.RawMessage {
			var frame map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(terminal, &frame))
			return frame["wire"]
		}(),
		json.RawMessage(`{"method":"turn/completed","params":{"threadId":"child","turn":{"id":"child-turn","items":[],"status":"completed"}}}`),
	} {
		row, err := json.Marshal(map[string]any{
			"direction": "s2c",
			"channel":   "protocol",
			"epoch":     1,
			"elapsedMs": 0,
			"wire":      wire,
			"ordinal":   len(rows) + 1,
		})
		require.NoError(t, err)
		rows = append(rows, row)
	}
	envelope.Frames = rows
	raw, err = json.Marshal(envelope)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "raw", "raw.jsonl"), raw, 0o600))
	var plan map[string]any
	require.NoError(t, readJSON(filepath.Join(root, "plan.json"), &plan))
	plan["driverProfile"] = "background-children"
	plan["scenario"] = map[string]any{"files": map[string]string{"numbers.txt": "7\n11\n"}, "parameters": map[string]string{"childDetailPolicy": "suppress"}}
	writeJSONTest(t, filepath.Join(root, "plan.json"), plan)
	writeJSONTest(t, filepath.Join(root, "report.json"), map[string]any{
		"childEvidence": []any{map[string]any{"childThreadId": "child", "parentThreadId": "thread-001"}},
		"receipts":      []any{map[string]any{"child-detail-selection": map[string]any{"rootId": "thread-001", "childIds": []string{"child"}}}},
	})
	refreshSyntheticBinding(t, root)
	var manifest map[string]any
	require.NoError(t, readJSON(filepath.Join(root, "manifest.json"), &manifest))
	manifest["files"] = map[string]string{"raw.jsonl": digest(raw)}
	writeJSONTest(t, filepath.Join(root, "manifest.json"), manifest)
	return root
}

func TestCodexRemoteCaseRunsDeclaredMainAndProbe(t *testing.T) {
	output := filepath.Join(t.TempDir(), "run")
	cmd := exec.CommandContext(t.Context(), runnerBinary, "--provider", "codex", "--case", "SDK-CASE-032", "--output", output, "--driver", driverBinary)
	b, err := cmd.CombinedOutput()
	require.NoError(t, err, string(b))

	var result summary
	require.NoError(t, readJSON(filepath.Join(output, "summary.json"), &result))
	require.Equal(t, "passed", result.Status)
	require.Len(t, result.Runs, 1)
	require.Empty(t, result.Runs[0].BridgeError)
	evidence := readSessionEvidence(t, filepath.Join(output, "preserve", "evidence.json"))
	require.Equal(t, "passed", evidence.Status)
	require.Len(t, evidence.Executions, 1)
	require.Equal(t, "passed", evidence.Executions[0].Status)
	require.True(t, evidence.Executions[0].Complete)
	require.Len(t, evidence.Executions[0].Connections, 2)
	for _, connection := range evidence.Executions[0].Connections {
		require.Equalf(t, "passed", connection.Status, connection.ID)
		require.Truef(t, connection.FullConsumption, connection.ID)
		require.Truef(t, connection.ClientEOF, connection.ID)
		requireEmbeddedConnectionFacts(t, connection)
		require.NotEmptyf(t, connection.ReceiptPath, connection.ID)
		require.NotEmptyf(t, connection.ResultPath, connection.ID)
		require.Emptyf(t, connection.Failure, connection.ID)
	}

	var request struct{ Peer map[string]any }
	require.NoError(t, readJSON(filepath.Join(output, "preserve", "sdk", "request.json"), &request))
	require.Len(t, request.Peer, 1)
	remote, ok := request.Peer["remote"].(map[string]any)
	require.True(t, ok)
	connections, ok := remote["connections"].(map[string]any)
	require.True(t, ok)
	require.Len(t, connections, 2)
	for _, id := range remoteConnectionIDs {
		endpoint, ok := connections[id].(map[string]any)
		require.True(t, ok)
		require.Len(t, endpoint, 2)
		require.NotEmpty(t, endpoint["url"])
		require.NotEmpty(t, endpoint["bearerToken"])
		require.Contains(t, endpoint["url"], "ws://127.0.0.1:")
		require.True(t, strings.HasSuffix(endpoint["url"].(string), "/"+id))
	}
}

func TestSelectedRunsRetainProcessAndFinishFailures(t *testing.T) {
	for _, tc := range []struct {
		name             string
		mutation         string
		remote           bool
		wantRuns         int
		wantProtocol     string
		wantBridge       string
		wantCleanupError string
		wantBusiness     string
		wantEvidence     string
		wantConnections  map[string]string
	}{
		{name: "remote-driver-crash", mutation: "crash", remote: true, wantRuns: 1, wantBridge: "0/2 required connections", wantEvidence: "failed", wantConnections: map[string]string{"main": "failed", "probe": "failed"}},
		{name: "missing-probe", mutation: "blank-probe-url", remote: true, wantRuns: 1, wantBridge: "1/2 required connections", wantBusiness: "failed", wantEvidence: "failed", wantConnections: map[string]string{"main": "passed", "probe": "failed"}},
		{name: "remote-postprocessing", mutation: "corrupt-probe-result", remote: true, wantRuns: 1, wantCleanupError: "mock result is not a regular unlinked file", wantBusiness: "passed", wantEvidence: "unavailable"},
		{name: "missing-suppress", mutation: "missing-suppress", wantRuns: 2, wantCleanupError: "execution report", wantBusiness: "passed", wantEvidence: "unavailable"},
		{name: "cross-run-suppress-receipt", mutation: "cross-run", wantRuns: 2, wantCleanupError: "execution report", wantBusiness: "passed", wantEvidence: "unavailable"},
		{name: "missing-sdk-projection-invalid-receipt", mutation: "invalid-receipt", wantRuns: 2, wantCleanupError: "execution report", wantBusiness: "passed", wantEvidence: "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "run")
			args := []string{"--recording", controlledChildCapture(t)}
			if tc.remote {
				args = []string{"--provider", "codex", "--case", "SDK-CASE-032"}
			}
			driver := driverBinary
			if tc.mutation != "" {
				driver = filepath.Join(t.TempDir(), "driver")
				require.NoError(t, os.WriteFile(driver, []byte(mutationDriver(tc.mutation, output)), 0o700))
			}
			args = append(args, "--output", output, "--driver", driver)
			cmd := exec.CommandContext(t.Context(), runnerBinary, args...)
			b, err := cmd.CombinedOutput()
			require.Error(t, err, string(b))

			var result summary
			require.NoError(t, readJSON(filepath.Join(output, "summary.json"), &result))
			require.Equal(t, "failed", result.Status)
			require.Len(t, result.Runs, tc.wantRuns)
			if tc.wantBusiness != "" {
				require.Equal(t, tc.wantBusiness, businessStatus(t, result.Runs[0].Business))
				if tc.wantRuns == 2 {
					require.Equal(t, tc.wantBusiness, businessStatus(t, result.Runs[1].Business))
				}
			} else {
				require.JSONEq(t, "null", string(result.Runs[0].Business))
				require.Contains(t, string(b), "exit status 9")
			}
			last := result.Runs[len(result.Runs)-1]
			if tc.wantProtocol != "" {
				require.Contains(t, last.ProtocolError, tc.wantProtocol)
			}
			if tc.wantBridge != "" {
				require.Contains(t, result.Runs[0].BridgeError, tc.wantBridge)
			}
			if tc.wantCleanupError != "" {
				require.Contains(t, last.CleanupError, tc.wantCleanupError)
			}
			if tc.wantRuns == 2 {
				require.Equal(t, "passed", result.Runs[0].Status)
			}
			for _, policy := range []string{"preserve", "suppress"}[:tc.wantRuns] {
				evidence := readSessionEvidence(t, filepath.Join(output, policy, "evidence.json"))
				wantEvidence := tc.wantEvidence
				if tc.wantRuns == 2 && policy == "preserve" {
					wantEvidence = "passed"
				}
				require.Equal(t, wantEvidence, evidence.Status)
				if len(evidence.Executions) > 0 {
					execution := evidence.Executions[0]
					require.True(t, execution.Complete)
					for _, connection := range execution.Connections {
						requireEmbeddedConnectionFacts(t, connection)
						if connection.Status == "passed" {
							require.True(t, connection.FullConsumption)
							require.True(t, connection.ClientEOF)
							require.Empty(t, connection.Failure)
						} else {
							require.NotEmpty(t, connection.Failure)
						}
						if tc.wantConnections != nil {
							want, ok := tc.wantConnections[connection.ID]
							require.True(t, ok)
							require.Equal(t, want, connection.Status)
						}
					}
				}
				prepared, err := readPreparedSession(filepath.Join(output, policy, "session"))
				require.Error(t, err)
				require.Nil(t, prepared)
			}
		})
	}
}

func mutationDriver(mutation, output string) string {
	body := "#!/bin/sh\n"
	switch mutation {
	case "crash":
		return body + "exit 9\n"
	case "blank-probe-url":
		body += quote(requestMutatorBinary) + " \"$2\"\n"
	case "corrupt-probe-result":
		body += "for result in " + quote(filepath.Join(output, "preserve", "session", "packages")) +
			"/*/executions/*/receipts/probe.result.json; do\n" +
			"  rm -f \"$result\"\n" +
			"  mkdir -p \"$result\"\n" +
			"done\n"
	}
	body += quote(driverBinary) + " \"$@\"\nstatus=$?\nif [ \"$status\" -eq 0 ]; then\n"
	switch mutation {
	case "missing-suppress":
		body += "rm -f " + quote(filepath.Join(output, "suppress", "session", "packages")) + "/*/executions/*/receipts/main.json\n"
	case "cross-run":
		saved := quote(filepath.Join(output, "saved-receipt.json"))
		body += "for receipt in " + quote(filepath.Join(output, "preserve", "session", "packages")) + "/*/executions/*/receipts/main.json; do [ ! -f \"$receipt\" ] || cp \"$receipt\" " + saved + "; done\n"
		body += "for receipt in " + quote(filepath.Join(output, "suppress", "session", "packages")) + "/*/executions/*/receipts/main.json; do [ ! -f \"$receipt\" ] || cp " + saved + " \"$receipt\"; done\n"
	case "invalid-receipt":
		body += "rm -f " + quote(filepath.Join(output, "suppress", "sdk", "raw", "raw.jsonl")) + "\n"
		body += "for receipt in " + quote(filepath.Join(output, "suppress", "session", "packages")) +
			"/*/executions/*/receipts/main.json; do\n" +
			"  if [ -f \"$receipt\" ]; then printf '{}\\n' > \"$receipt\"; fi\n" +
			"done\n"
	}
	body += "fi\nexit \"$status\"\n"
	return body
}

// Restores the SDK-owned half of corpusSuite.TestAdoptedMaterialsRunCurrentSDKAndRetainFailures.
// Tool adoption/qualification remains tested by the tool; this input is synthetic.
func TestAdoptedMaterialsRunCurrentSDKAndRetainFailures(t *testing.T) {
	source := controlledCapture(t)
	var plan map[string]any
	require.NoError(t, readJSON(filepath.Join(source, "plan.json"), &plan))
	plan["caseId"] = "SDK-CASE-002"
	writeJSONTest(t, filepath.Join(source, "plan.json"), plan)
	refreshSyntheticBinding(t, source)
	before := inventoryTest(t, source)

	output := filepath.Join(t.TempDir(), "success")
	wrapper := filepath.Join(t.TempDir(), "driver")
	script := "#!/bin/sh\nset -e\ncp numbers.txt " + quote(filepath.Join(filepath.Dir(output), "initial-numbers.txt")) + "\nexec " + quote(driverBinary) + " \"$@\"\n"
	require.NoError(t, os.WriteFile(wrapper, []byte(script), 0o700))
	cmd := exec.CommandContext(t.Context(), runnerBinary, "--recording", source, "--output", output, "--driver", wrapper)
	b, err := cmd.CombinedOutput()
	require.NoError(t, err, string(b))
	numbers, err := os.ReadFile(filepath.Join(filepath.Dir(output), "initial-numbers.txt"))
	require.NoError(t, err)
	require.Equal(t, "7\n11\n", string(numbers))

	failed := filepath.Join(t.TempDir(), "failed")
	cmd = exec.CommandContext(t.Context(), runnerBinary, "--recording", source, "--output", failed, "--driver", "/usr/bin/false")
	b, err = cmd.CombinedOutput()
	require.Error(t, err, string(b))
	require.FileExists(t, filepath.Join(failed, "summary.json"))
	require.Equal(t, "abandoned", readSessionEvidence(t, filepath.Join(failed, "preserve", "evidence.json")).Status)
	require.Equal(t, before, inventoryTest(t, source))
}

func readSessionEvidence(t *testing.T, path string) sessionEvidence {
	t.Helper()
	var evidence sessionEvidence
	require.NoError(t, readJSON(path, &evidence))
	return evidence
}

func businessStatus(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var result struct{ Status string }
	require.NoError(t, json.Unmarshal(raw, &result))
	return result.Status
}

func inventoryTest(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		result[name] = digest(b)
		return nil
	}))
	return result
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
