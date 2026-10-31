package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	fixturedata "github.com/zoumo/agent-sdk-go/codex/tests/testdata"
)

// Exercise the real request consumer, rather than the tool's synthetic driver.
// No private runtime peer fields are present in the original materials.
func TestSDKRequestUsesRunnerPeers(t *testing.T) {
	driver := filepath.Join(t.TempDir(), "sdk-driver")
	build := exec.Command("go", "build", "-o", driver, "../../tests/simulation/sdk")
	data, err := build.CombinedOutput()
	require.NoError(t, err, "%s", data)
	fixture, err := fixturedata.Files.ReadFile("integration/lifecycle.jsonl")
	require.NoError(t, err)
	var prefix []object
	for _, line := range bytes.Split(bytes.TrimSpace(fixture), []byte{'\n'})[:5] {
		var frame object
		require.NoError(t, json.Unmarshal(line, &frame))
		prefix = append(prefix, frame)
	}
	makeFrames := func(wires ...string) []object {
		result := append([]object{}, prefix...)
		for _, wire := range wires {
			var message object
			require.NoError(t, json.Unmarshal([]byte(wire), &message))
			direction := "s2c"
			if message["method"] == "turn/start" {
				direction = "c2s"
			}
			result = append(result, object{"ordinal": len(result) + 1, "direction": direction, "channel": "protocol", "epoch": 1, "elapsedMs": 0, "wire": message})
		}
		return result
	}
	main := makeFrames(
		`{"id":103,"method":"turn/start","params":{"threadId":"thread-001","input":[{"type":"text","text":"synthetic task"}]}}`,
		`{"id":103,"result":{"turn":{"id":"turn-001","items":[],"status":"inProgress"}}}`,
		`{"method":"turn/completed","params":{"threadId":"thread-001","turn":{"id":"turn-001","items":[],"status":"completed"}}}`,
	)
	writeFrames := func(path string, frames []object) {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		var out bytes.Buffer
		for _, frame := range frames {
			require.NoError(t, json.NewEncoder(&out).Encode(frame))
		}
		require.NoError(t, os.WriteFile(path, out.Bytes(), 0600))
	}
	for _, profile := range []string{"structured-input", "remote-close", "background-children", "remote-close-postprocess"} {
		t.Run(profile, func(t *testing.T) {
			postprocessFailure := profile == "remote-close-postprocess"
			if postprocessFailure {
				profile = "remote-close"
			}
			o := sample(t, "sdk-request")
			o.Mode, o.Driver, o.DriverArgs = "simulate", driver, nil
			if postprocessFailure {
				wrapper := filepath.Join(t.TempDir(), "driver")
				require.NoError(t, os.WriteFile(wrapper, []byte(fmt.Sprintf("#!/bin/sh\nmkdir %q\nexec %q \"$@\"\n", filepath.Join(o.Output, "phase-0-stderr.private"), driver)), 0700))
				o.Driver = wrapper
			}
			frames := main
			scenario := object{}
			if profile == "background-children" {
				scenario = object{"parameters": object{"childDetailPolicy": "suppress"}}
				thread := obj(obj(prefix[4]["wire"])["result"])["thread"]
				b, e := json.Marshal(thread)
				require.NoError(t, e)
				var child object
				require.NoError(t, json.Unmarshal(b, &child))
				child["id"], child["parentThreadId"] = "child", "thread-001"
				frames = append([]object{}, main[:7]...)
				for _, wire := range []object{
					{"method": "thread/started", "params": object{"thread": child}},
					{"method": "item/agentMessage/delta", "params": object{"threadId": "child", "turnId": "child-turn", "itemId": "detail", "delta": "hello"}},
					obj(main[7]["wire"]),
					{"method": "turn/completed", "params": object{"threadId": "child", "turn": object{"id": "child-turn", "items": []object{}, "status": "completed"}}},
				} {
					frames = append(frames, object{"ordinal": len(frames) + 1, "direction": "s2c", "channel": "protocol", "epoch": 1, "elapsedMs": 0, "wire": wire})
				}
				require.NoError(t, writeJSON(filepath.Join(o.Materials, "report.json"), object{
					"childEvidence": []object{{"childThreadId": "child", "parentThreadId": "thread-001"}},
					"receipts":      []object{{"child-detail-selection": object{"rootId": "thread-001", "childIds": []string{"child"}}}},
				}))
			}
			writeFrames(filepath.Join(o.Materials, "raw/raw.jsonl"), frames)
			require.NoError(t, writeJSON(filepath.Join(o.Materials, "plan.json"), object{"provider": "codex", "caseId": "sdk-request", "project": "/fixture/project", "driverProfile": profile, "scenario": scenario}))
			if profile == "remote-close" {
				probe := append([]object{}, prefix[:3]...)
				for _, row := range []object{
					{"direction": "c2s", "wire": object{"id": 104, "method": "thread/read", "params": object{"threadId": "thread-001", "includeTurns": true}}},
					{"direction": "s2c", "wire": object{"id": 104, "result": object{"thread": object{"id": "thread-001", "cliVersion": "0.162.1", "createdAt": 1, "updatedAt": 1, "cwd": "/fixture/project", "ephemeral": true, "modelProvider": "synthetic", "preview": "", "source": "appServer", "status": object{"type": "idle"}, "turns": []object{}, "sessionId": "session-001", "projectId": nil}}}},
				} {
					row["ordinal"], row["channel"], row["epoch"], row["elapsedMs"] = len(probe)+1, "protocol", 1, 0
					probe = append(probe, row)
				}
				writeFrames(filepath.Join(o.Materials, "remote-probe/raw/raw.jsonl"), probe)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, err := run(ctx, o)
			if err != nil {
				log, _ := os.ReadFile(filepath.Join(o.Output, "stderr.log"))
				t.Log(string(log))
			}
			if postprocessFailure {
				require.ErrorContains(t, err, "is a directory")
				var outcome result
				require.NoError(t, readJSON(filepath.Join(o.Output, "result.json"), &outcome))
				require.Equal(t, "passed", outcome.Status)
				return
			}
			require.NoError(t, err)
			var plan object
			require.NoError(t, readJSON(filepath.Join(o.Output, "materials/case.json"), &plan))
			require.NotContains(t, plan, "_mock")
			require.NotContains(t, plan, "_peers")
			require.NoError(t, checkTransport("simulate", o.Output, "codex", "passed"))
			if profile == "background-children" {
				require.FileExists(t, filepath.Join(o.Output, "suppress/replay-peer.json"))
				require.NoError(t, os.Remove(filepath.Join(o.Output, "suppress/replay-peer.json")))
				require.Error(t, checkTransport("simulate", o.Output, "codex", "passed"))
			}
		})
	}
}
