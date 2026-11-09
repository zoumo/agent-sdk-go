package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

type object = map[string]any

func TestClaudeMovedHistoryRetainsMaterialBinding(t *testing.T) {
	const sessionID = "11111111-1111-4111-8111-111111111111"
	originalCWD := t.TempDir()
	require.NoError(t, os.Remove(originalCWD))
	key := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(originalCWD, "-")
	name := filepath.Join("config", "projects", key, sessionID+".jsonl")
	var saved bytes.Buffer
	expected := []object{}
	for i, kind := range []string{"user", "assistant"} {
		id := []string{"u1", "a1"}[i]
		var parent any
		if i == 1 {
			parent = "u1"
		}
		message := object{"content": "saved " + kind}
		require.NoError(t, json.NewEncoder(&saved).Encode(object{
			"type": kind, "uuid": id, "parentUuid": parent,
			"sessionId": sessionID, "cwd": originalCWD, "message": message,
		}))
		expected = append(expected, object{"type": kind, "uuid": id, "session_id": sessionID, "message": message})
	}

	wireRows := []object{
		{"direction": "c2s", "wire": object{"type": "control_request", "request_id": "init", "request": object{"subtype": "initialize", "hooks": nil}}},
		{"direction": "s2c", "wire": object{"type": "control_response", "response": object{"subtype": "success", "request_id": "init", "response": object{}}}},
		{"direction": "c2s", "wire": object{"type": "user", "session_id": "default", "parent_tool_use_id": nil, "message": object{"role": "user", "content": "task"}}},
		{"direction": "s2c", "wire": object{"type": "system", "subtype": "init", "session_id": sessionID, "cwd": originalCWD}},
		{"direction": "s2c", "wire": object{"type": "result", "session_id": sessionID, "subtype": "success", "is_error": false, "result": "done"}},
	}
	frames := make([]json.RawMessage, 0, len(wireRows))
	for i, row := range wireRows {
		row["channel"] = "protocol"
		row["epoch"] = 1
		row["elapsedMs"] = 0
		row["ordinal"] = i + 1
		frame, err := json.Marshal(row)
		require.NoError(t, err)
		frames = append(frames, frame)
	}
	envelope, err := json.Marshal(map[string]any{"version": 1, "frames": frames})
	require.NoError(t, err)
	source, err := mocktest.Capture(t.TempDir(), "claude", envelope, mocktest.Options{})
	require.NoError(t, err)
	plan := object{
		"provider": "claude", "caseId": "SDK-CASE-033", "project": originalCWD,
		"driverProfile": "history", "scenario": object{"files": object{"numbers.txt": "7\n11\n"}},
	}
	writeJSONTest(t, filepath.Join(source, "plan.json"), plan)
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(source, name)), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(source, name), saved.Bytes(), 0o600))
	writeJSONTest(t, filepath.Join(source, "report.json"), object{
		"receipts": []object{{"get_session_messages": expected}},
	})
	var manifest map[string]any
	require.NoError(t, readJSON(filepath.Join(source, "manifest.json"), &manifest))
	planBytes, err := os.ReadFile(filepath.Join(source, "plan.json"))
	require.NoError(t, err)
	manifest["planSHA256"] = digest(planBytes)
	manifest["savedFiles"] = map[string]string{name: digest(saved.Bytes())}
	writeJSONTest(t, filepath.Join(source, "manifest.json"), manifest)

	moved := filepath.Join(t.TempDir(), "adopted-copy")
	require.NoError(t, os.Rename(source, moved))
	source = moved
	simulationRoot := t.TempDir()
	output := filepath.Join(simulationRoot, "run")
	driver := filepath.Join(t.TempDir(), "history-driver")
	require.NoError(t, os.WriteFile(driver, []byte("#!/bin/sh\n"+quote(driverBinary)+" \"$@\"\n"), 0o700))
	cmd := exec.CommandContext(t.Context(), runnerBinary, "--recording", source, "--output", output, "--driver", driver)
	log, err := cmd.CombinedOutput()
	require.NoError(t, err, string(log))

	var result summary
	require.NoError(t, readJSON(filepath.Join(output, "summary.json"), &result))
	require.Equal(t, "passed", result.Status)
	require.Len(t, result.Runs, 1)
	require.Equal(t, "passed", result.Runs[0].Status)
	var business struct{ Status, Message string }
	require.NoError(t, json.Unmarshal(result.Runs[0].Business, &business))
	require.Equal(t, "passed", business.Status)
	require.Empty(t, business.Message)
	require.Empty(t, result.Runs[0].ProtocolError)
	require.Empty(t, result.Runs[0].CleanupError)
	original, err := os.ReadFile(filepath.Join(source, name))
	require.NoError(t, err)
	require.Equal(t, saved.Bytes(), original)
	require.NoDirExists(t, originalCWD)
	evidence := readSessionEvidence(t, filepath.Join(output, "preserve", "evidence.json"))
	require.Equal(t, "passed", evidence.Status)
	require.Empty(t, evidence.ValidationError)
	require.NoDirExists(t, filepath.Join(output, "preserve", "session"))
}
