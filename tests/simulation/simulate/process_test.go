package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	fixturedata "github.com/zoumo/agent-sdk-go/codex/tests/testdata"
)

var runnerBinary, driverBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sdk-simulate-tests-")
	if err != nil {
		panic(err)
	}
	runnerBinary = filepath.Join(dir, "simulate")
	driverBinary = filepath.Join(dir, "driver")
	for _, entry := range [][2]string{{runnerBinary, "."}, {driverBinary, "../drivers"}} {
		cmd := exec.Command("go", "build", "-o", entry[0], entry[1])
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
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, b, 0600))
}
func controlledCapture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	b, err := fixturedata.Files.ReadFile("integration/lifecycle.jsonl")
	require.NoError(t, err)
	rows := bytes.Split(bytes.TrimSpace(b), []byte("\n"))[:5]
	for i, wire := range []string{
		`{"id":103,"method":"turn/start","params":{"threadId":"thread-001","input":[{"type":"text","text":"synthetic task"}]}}`,
		`{"id":103,"result":{"turn":{"id":"turn-001","items":[],"status":"inProgress"}}}`,
		`{"method":"turn/completed","params":{"threadId":"thread-001","turn":{"id":"turn-001","items":[],"status":"completed"}}}`,
	} {
		direction := "s2c"
		if i == 0 {
			direction = "c2s"
		}
		row, _ := json.Marshal(map[string]any{"ordinal": len(rows) + 1, "direction": direction, "channel": "protocol", "epoch": 1, "elapsedMs": 0, "wire": json.RawMessage(wire)})
		rows = append(rows, row)
	}
	require.NoError(t, os.Mkdir(filepath.Join(dir, "raw"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "raw/raw.jsonl"), append(bytes.Join(rows, []byte("\n")), 10), 0600))
	writeJSONTest(t, filepath.Join(dir, "plan.json"), map[string]any{"provider": "codex", "caseId": "custom-structured", "project": "/fixture/project", "driverProfile": "structured-input", "scenario": map[string]any{"files": map[string]string{"numbers.txt": "7\n11\n"}}})
	bindTest(t, dir)
	return dir
}
func bindTest(t *testing.T, dir string) {
	t.Helper()
	plan, e := os.ReadFile(filepath.Join(dir, "plan.json"))
	require.NoError(t, e)
	raw, e := os.ReadFile(filepath.Join(dir, "raw/raw.jsonl"))
	require.NoError(t, e)
	writeJSONTest(t, filepath.Join(dir, "manifest.json"), map[string]any{"provenance": "synthetic", "planSHA256": digest(plan), "files": map[string]string{"raw.jsonl": digest(raw)}})
}
func TestSDKRequestUsesRunnerPeers(t *testing.T) {
	source := controlledCapture(t)
	output := filepath.Join(t.TempDir(), "run")
	cmd := exec.Command(runnerBinary, "--recording", source, "--output", output, "--driver", driverBinary)
	b, err := cmd.CombinedOutput()
	require.NoError(t, err, string(b))
	var summary struct {
		Status string
		Runs   []struct {
			RunID  string
			Status string
		}
	}
	data, err := os.ReadFile(filepath.Join(output, "summary.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &summary))
	require.Equal(t, "passed", summary.Status)
	require.Len(t, summary.Runs, 1)
	require.NotEmpty(t, summary.Runs[0].RunID)
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func TestSelectedRunsAndIndependentPolicies(t *testing.T) {
	for _, tc := range []struct{ name, profile, mutation, want string }{
		{"background-children", "background-children", "", ""},
		{"remote-close", "remote-close", "", ""},
		{"remote-driver-crash", "remote-close", "crash", "0/2 required connections"},
		{"missing-probe", "remote-close", "probe", "probe"},
		{"missing-suppress", "background-children", "suppress", "main"},
		{"cross-run-suppress-receipt", "background-children", "cross-run", "main"},
		{"missing-sdk-projection-invalid-receipt", "background-children", "raw", "main"},
		{"remote-postprocessing", "remote-close", "postprocess", "is a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := controlledCapture(t)
			var plan map[string]any
			require.NoError(t, readJSON(filepath.Join(source, "plan.json"), &plan))
			plan["driverProfile"] = tc.profile
			raw, err := os.ReadFile(filepath.Join(source, "raw/raw.jsonl"))
			require.NoError(t, err)
			rows := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
			if tc.profile == "background-children" {
				plan["scenario"] = map[string]any{"parameters": map[string]string{"childDetailPolicy": "suppress"}}
				var root map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(rows[4], &root))
				var msg struct {
					Result struct{ Thread map[string]any }
				}
				require.NoError(t, json.Unmarshal(root["wire"], &msg))
				child := msg.Result.Thread
				child["id"] = "child"
				child["parentThreadId"] = "thread-001"
				terminal := rows[7]
				rows = rows[:7]
				for _, wire := range []any{
					map[string]any{"method": "thread/started", "params": map[string]any{"thread": child}},
					json.RawMessage(`{"method":"item/agentMessage/delta","params":{"threadId":"child","turnId":"child-turn","itemId":"detail","delta":"hello"}}`),
					func() json.RawMessage {
						var f map[string]json.RawMessage
						require.NoError(t, json.Unmarshal(terminal, &f))
						return f["wire"]
					}(),
					json.RawMessage(`{"method":"turn/completed","params":{"threadId":"child","turn":{"id":"child-turn","items":[],"status":"completed"}}}`),
				} {
					b, e := json.Marshal(map[string]any{"ordinal": len(rows) + 1, "direction": "s2c", "channel": "protocol", "epoch": 1, "elapsedMs": 0, "wire": wire})
					require.NoError(t, e)
					rows = append(rows, b)
				}
				writeJSONTest(t, filepath.Join(source, "report.json"), map[string]any{"childEvidence": []any{map[string]any{"childThreadId": "child", "parentThreadId": "thread-001"}}, "receipts": []any{map[string]any{"child-detail-selection": map[string]any{"rootId": "thread-001", "childIds": []string{"child"}}}}})
			}
			writeJSONTest(t, filepath.Join(source, "plan.json"), plan)
			require.NoError(t, os.WriteFile(filepath.Join(source, "raw/raw.jsonl"), append(bytes.Join(rows, []byte("\n")), 10), 0600))
			bindTest(t, source)
			if tc.profile == "remote-close" {
				probe := append([][]byte{}, rows[:3]...)
				for i, wire := range []string{
					`{"id":104,"method":"thread/read","params":{"threadId":"thread-001","includeTurns":true}}`,
					`{"id":104,"result":{"thread":{"id":"thread-001","cliVersion":"0.162.1","createdAt":1,"updatedAt":1,"cwd":"/fixture/project","ephemeral":true,"modelProvider":"synthetic","preview":"","source":"appServer","status":{"type":"idle"},"turns":[],"sessionId":"session-001","projectId":null}}}`,
				} {
					dir := "s2c"
					if i == 0 {
						dir = "c2s"
					}
					b, _ := json.Marshal(map[string]any{"ordinal": len(probe) + 1, "direction": dir, "channel": "protocol", "epoch": 1, "elapsedMs": 0, "wire": json.RawMessage(wire)})
					probe = append(probe, b)
				}
				probeRoot := filepath.Join(source, "remote-probe")
				require.NoError(t, os.MkdirAll(filepath.Join(probeRoot, "raw"), 0700))
				require.NoError(t, os.WriteFile(filepath.Join(probeRoot, "raw/raw.jsonl"), append(bytes.Join(probe, []byte("\n")), 10), 0600))
				writeJSONTest(t, filepath.Join(probeRoot, "plan.json"), plan)
				bindTest(t, probeRoot)
				var manifest map[string]any
				require.NoError(t, readJSON(filepath.Join(source, "manifest.json"), &manifest))
				b, err := os.ReadFile(filepath.Join(probeRoot, "raw/raw.jsonl"))
				require.NoError(t, err)
				manifest["remotePhaseFiles"] = map[string]string{"remote-probe/raw/raw.jsonl": digest(b)}
				writeJSONTest(t, filepath.Join(source, "manifest.json"), manifest)
			}
			output := filepath.Join(t.TempDir(), "run")
			driver := driverBinary
			if tc.mutation != "" {
				driver = filepath.Join(t.TempDir(), "driver")
				script := "#!/bin/sh\nset -e\n"
				if tc.mutation == "postprocess" {
					script += "mkdir " + quote(filepath.Join(output, "preserve/sdk/phase-main-stderr.private")) + "\n"
				}
				if tc.mutation == "crash" {
					script += "exit 9\n"
				} else {
					script += quote(driverBinary) + " \"$@\"\n"
				}

				switch tc.mutation {
				case "probe":
					script += "rm " + quote(filepath.Join(output, "preserve/probe.receipt.json")) + "\n"
				case "suppress":
					script += "if [ -f " + quote(filepath.Join(output, "suppress/main.receipt.json")) + " ]; then rm " + quote(filepath.Join(output, "suppress/main.receipt.json")) + "; fi\n"
				case "cross-run":
					script += "if [ -f " + quote(filepath.Join(output, "suppress/main.receipt.json")) + " ]; then cp " + quote(filepath.Join(output, "preserve/main.receipt.json")) + " " + quote(filepath.Join(output, "suppress/main.receipt.json")) + "; fi\n"
				case "raw":
					script += "if [ -f " + quote(filepath.Join(output, "suppress/main.receipt.json")) + " ]; then rm " + quote(filepath.Join(output, "suppress/sdk/raw/raw.jsonl")) + "; echo '{}' > " + quote(filepath.Join(output, "suppress/main.receipt.json")) + "; fi\n"
				}
				require.NoError(t, os.WriteFile(driver, []byte(script), 0700))
			}
			cmd := exec.Command(runnerBinary, "--recording", source, "--output", output, "--driver", driver)
			b, err := cmd.CombinedOutput()
			if tc.want != "" {
				require.Error(t, err, string(b))
				require.Contains(t, string(b), tc.want)
			} else {
				require.NoError(t, err, string(b))
			}
			var result summary
			require.NoError(t, readJSON(filepath.Join(output, "summary.json"), &result))
			var first descriptor
			require.NoError(t, readJSON(filepath.Join(output, "preserve/run.json"), &first))
			require.NoDirExists(t, first.ResourceRoot)
			var immutable map[string]any
			require.NoError(t, readJSON(first.Contract, &immutable))
			for _, key := range []string{"_mock", "_peers", "_fixtureProject"} {
				require.NotContains(t, immutable, key)
			}
			if tc.mutation == "crash" {
				require.JSONEq(t, "null", string(result.Runs[0].Business))
				require.Contains(t, string(b), "exit status 9")
				return
			}
			require.JSONEq(t, `{"status":"passed"}`, string(result.Runs[0].Business))
			if tc.profile == "background-children" {
				require.Len(t, result.Runs, 2)
				var second descriptor
				require.NoError(t, readJSON(filepath.Join(output, "suppress/run.json"), &second))
				require.NotEqual(t, first.RunID, second.RunID)
				require.NotEqual(t, first.Env["CODEX_HOME"], second.Env["CODEX_HOME"])
				require.NoDirExists(t, second.ResourceRoot)
				require.JSONEq(t, `{"status":"passed"}`, string(result.Runs[1].Business))
			}
		})
	}
}

// Restores the SDK-owned half of corpusSuite.TestAdoptedMaterialsRunCurrentSDKAndRetainFailures.
// Tool adoption/qualification remains tested by the tool; this input is synthetic.
func TestAdoptedMaterialsRunCurrentSDKAndRetainFailures(t *testing.T) {
	source := controlledCapture(t)
	var plan map[string]any
	require.NoError(t, readJSON(filepath.Join(source, "plan.json"), &plan))
	plan["caseId"] = "SDK-CASE-002"
	writeJSONTest(t, filepath.Join(source, "plan.json"), plan)
	bindTest(t, source)
	before := inventoryTest(t, source)
	output := filepath.Join(t.TempDir(), "success")
	wrapper := filepath.Join(t.TempDir(), "driver")
	script := "#!/bin/sh\nset -e\ncp numbers.txt " + quote(filepath.Join(filepath.Dir(output), "initial-numbers.txt")) + "\nexec " + quote(driverBinary) + " \"$@\"\n"
	require.NoError(t, os.WriteFile(wrapper, []byte(script), 0700))
	cmd := exec.Command(runnerBinary, "--recording", source, "--output", output, "--driver", wrapper)
	b, e := cmd.CombinedOutput()
	require.NoError(t, e, string(b))
	numbers, e := os.ReadFile(filepath.Join(filepath.Dir(output), "initial-numbers.txt"))
	require.NoError(t, e)
	require.Equal(t, "7\n11\n", string(numbers))
	failed := filepath.Join(t.TempDir(), "failed")
	cmd = exec.Command(runnerBinary, "--recording", source, "--output", failed, "--driver", "/usr/bin/false")
	b, e = cmd.CombinedOutput()
	require.Error(t, e, string(b))
	require.FileExists(t, filepath.Join(failed, "summary.json"))
	require.FileExists(t, filepath.Join(failed, "preserve/finish.json"))
	gateOut := filepath.Join(t.TempDir(), "gate")
	cmd = exec.Command(runnerBinary, "--gate", "--recording", source, "--output", gateOut, "--driver", driverBinary, "--jobs", "2")
	b, e = cmd.CombinedOutput()
	require.Error(t, e, string(b))
	var c coverage
	require.NoError(t, readJSON(filepath.Join(gateOut, "coverage.json"), &c))
	require.Len(t, c.Cases, 72)
	require.Equal(t, map[string]int{"passed": 1, "unverified": 71}, c.Counts)
	require.Equal(t, before, inventoryTest(t, source))
}
func inventoryTest(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		name, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		result[name] = digest(b)
		return nil
	}))
	return result
}
