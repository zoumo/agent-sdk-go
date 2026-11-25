package simulate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	fixturedata "github.com/zoumo/agent-sdk-go/internal/testsupport/codex/testdata"
	"github.com/zoumo/agent-sdk-go/internal/testsupport/mocktest"
	"github.com/zoumo/agent-sdk-go/replay"
)

const stampedTestEnv = "SDK_SIMULATION_STAMPED_TEST"

var driverBinary, requestMutatorBinary string

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--simulation-test-process" {
		os.Exit(runSimulationProcess(os.Args[2:]))
	}
	if _, err := replay.BuildIdentity(); err != nil {
		if os.Getenv(stampedTestEnv) == "1" {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(runStampedTest())
	}
	buildRoot := os.Getenv("AGENT_SDK_TESTIFY_BUILD_DIR")
	if buildRoot == "" {
		buildRoot = "/tmp"
	} else if err := os.MkdirAll(buildRoot, 0o700); err != nil {
		panic(err)
	}
	dir, err := os.MkdirTemp(buildRoot, "sdk-simulation-tests-")
	if err != nil {
		panic(err)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		panic(err)
	}
	driverBinary = filepath.Join(dir, "driver")
	requestMutatorBinary = filepath.Join(dir, "mutate-request")
	for _, entry := range [][2]string{
		{driverBinary, "./driver"},
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

// Go tests do not record VCS metadata by default. Rebuild once from the
// verified checkout so evidence still names the exact SDK build.
func runStampedTest() int {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	args := []string{"scripts/replay/with-build-checkout.sh", "go", "test", "-buildvcs=true", "-count=1"}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Value == "true" && (setting.Key == "-race" || setting.Key == "-cover") {
				args = append(args, setting.Key)
			}
		}
	}
	args = append(args, "./tests/simulation", "-args")
	args = append(args, os.Args[1:]...)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := exec.CommandContext(ctx, "bash", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), stampedTestEnv+"=1")
	if os.Getenv("SDK_SIMULATION_ARCHIVE_ROOT") == "" {
		cmd.Env = append(cmd.Env, "SDK_SIMULATION_ARCHIVE_ROOT="+filepath.Join(root, ".artifacts", "simulation", "gates"))
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			return exit.ExitCode()
		}
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func simulationCommand(ctx context.Context, arguments ...string) *exec.Cmd {
	return exec.CommandContext(ctx, os.Args[0], append([]string{"--simulation-test-process"}, arguments...)...)
}

func runSimulationProcess(arguments []string) int {
	var o options
	flags := flag.NewFlagSet("simulation-test-process", flag.ContinueOnError)
	flags.StringVar(&o.Recording, "recording", "", "External test capture")
	flags.StringVar(&o.Provider, "provider", "", "Test provider")
	flags.StringVar(&o.Case, "case", "", "Test case")
	flags.StringVar(&o.Output, "output", "", "Test evidence output")
	flags.StringVar(&o.Driver, "driver", "", "Isolated SDK driver")
	flags.IntVar(&o.Jobs, "jobs", 1, "Concurrent cases")
	flags.DurationVar(&o.Timeout, "timeout", 30*time.Second, "Per-case deadline")
	flags.BoolVar(&o.Gate, "gate", false, "Test full coverage")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := execute(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
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
	cmd := simulationCommand(t.Context(), "--recording", source, "--output", output, "--driver", driverBinary)
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
	cmd := simulationCommand(t.Context(), "--recording", source, "--output", output, "--driver", driverBinary)
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
	driver := filepath.Join(t.TempDir(), "driver")
	require.NoError(t, os.WriteFile(driver, []byte(`#!/bin/sh
if [ "${SIMULATION_HOST_ONLY+x}" = x ]; then
    echo "simulation driver inherited a host-only variable" >&2
    exit 1
fi
exec `+quote(driverBinary)+` "$@"
`), 0o700))
	const marker = "simulation-host-only-marker"
	t.Setenv("SIMULATION_HOST_ONLY", marker)
	cmd := simulationCommand(t.Context(), "--provider", "codex", "--case", "SDK-CASE-032", "--output", output, "--driver", driver)
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

	var request struct {
		Env  json.RawMessage
		Peer map[string]any
	}
	require.NoError(t, readJSON(filepath.Join(output, "preserve", "sdk", "request.json"), &request))
	require.Empty(t, request.Env)
	require.Len(t, request.Peer, 1)
	remote, ok := request.Peer["remote"].(map[string]any)
	require.True(t, ok)
	connections, ok := remote["connections"].(map[string]any)
	require.True(t, ok)
	require.Len(t, connections, 2)
	for _, id := range remoteConnectionIDs {
		endpoint, ok := connections[id].(map[string]any)
		require.True(t, ok)
		require.Len(t, endpoint, 1)
		require.NotEmpty(t, endpoint["url"])
		require.Contains(t, endpoint["url"], "ws://127.0.0.1:")
		require.True(t, strings.HasSuffix(endpoint["url"].(string), "/"+id))
	}
	require.NoError(t, filepath.WalkDir(output, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		data, err := os.ReadFile(path)
		if err == nil && bytes.Contains(data, []byte(marker)) {
			return fmt.Errorf("host-only marker persisted in %s", path)
		}
		return err
	}))
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
			cmd := simulationCommand(t.Context(), args...)
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
	cmd := simulationCommand(t.Context(), "--recording", source, "--output", output, "--driver", wrapper)
	b, err := cmd.CombinedOutput()
	require.NoError(t, err, string(b))
	numbers, err := os.ReadFile(filepath.Join(filepath.Dir(output), "initial-numbers.txt"))
	require.NoError(t, err)
	require.Equal(t, "7\n11\n", string(numbers))

	failed := filepath.Join(t.TempDir(), "failed")
	cmd = simulationCommand(t.Context(), "--recording", source, "--output", failed, "--driver", "/usr/bin/false")
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
