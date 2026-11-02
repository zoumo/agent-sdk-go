package main

import (
	"bytes"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type object = map[string]any

func TestClaudeMovedMaterialsUseCurrentSDKAndDerivedHistory(t *testing.T) {
	source := t.TempDir()
	output := filepath.Join(t.TempDir(), "simulation")
	require.NoError(t, os.Mkdir(filepath.Join(source, "raw"), 0700))
	const session = "11111111-1111-4111-8111-111111111111"
	originalCWD := filepath.Join(t.TempDir(), "no-longer-present")
	key := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(originalCWD, "-")
	name := filepath.Join("config/projects", key, session+".jsonl")
	var saved bytes.Buffer
	expected := []object{}
	for i, kind := range []string{"user", "assistant"} {
		id := []string{"u1", "a1"}[i]
		var parent any
		if i == 1 {
			parent = "u1"
		}
		message := object{"content": "saved " + kind}
		require.NoError(t, json.NewEncoder(&saved).Encode(object{"type": kind, "uuid": id, "parentUuid": parent, "sessionId": session, "cwd": originalCWD, "message": message}))
		expected = append(expected, object{"type": kind, "uuid": id, "session_id": session, "message": message})
	}
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(source, name)), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(source, name), saved.Bytes(), 0600))
	plan := object{"provider": "claude", "caseId": "SDK-CASE-033", "project": originalCWD, "driverProfile": "history", "scenario": object{"files": object{"numbers.txt": "7\n11\n"}}}
	require.NoError(t, writeJSON(filepath.Join(source, "plan.json"), plan))
	require.NoError(t, writeJSON(filepath.Join(source, "report.json"), object{"receipts": []object{{"get_session_messages": expected}}}))
	rows := []object{
		{"type": "control_request", "request_id": "init", "request": object{"subtype": "initialize", "hooks": nil}},
		{"type": "control_response", "response": object{"subtype": "success", "request_id": "init", "response": object{}}},
		{"type": "user", "session_id": "default", "parent_tool_use_id": nil, "message": object{"role": "user", "content": "task"}},
		{"type": "system", "subtype": "init", "session_id": session, "cwd": originalCWD},
		{"type": "result", "session_id": session, "subtype": "success", "is_error": false, "result": "done"},
	}
	var raw bytes.Buffer
	for i, row := range rows {
		direction := "s2c"
		if i == 0 || i == 2 {
			direction = "c2s"
		}
		require.NoError(t, json.NewEncoder(&raw).Encode(object{"ordinal": i + 1, "direction": direction, "channel": "protocol", "epoch": 1, "elapsedMs": 0, "wire": row}))
	}
	require.NoError(t, os.WriteFile(filepath.Join(source, "raw/raw.jsonl"), raw.Bytes(), 0600))
	// Move the full material package and remove its original location.
	moved := filepath.Join(t.TempDir(), "adopted-copy")
	require.NoError(t, os.Rename(source, moved))

	source = moved
	bindTest(t, source)
	var manifest map[string]any
	require.NoError(t, readJSON(filepath.Join(source, "manifest.json"), &manifest))
	manifest["savedFiles"] = map[string]string{name: digest(saved.Bytes())}
	require.NoError(t, writeJSON(filepath.Join(source, "manifest.json"), manifest))
	driver := filepath.Join(t.TempDir(), "history-driver")
	snapshot := filepath.Join(output, "before-finish-config")
	script := "#!/bin/sh\nset -e\n" + quote(driverBinary) + " \"$@\"\ncp -R \"$CLAUDE_CONFIG_DIR\" " + quote(snapshot) + "\n"
	require.NoError(t, os.WriteFile(driver, []byte(script), 0700))
	cmd := exec.Command(runnerBinary, "--recording", source, "--output", output, "--driver", driver)
	log, err := cmd.CombinedOutput()
	require.NoError(t, err, string(log))
	var d descriptor
	require.NoError(t, readJSON(filepath.Join(output, "preserve/run.json"), &d))
	require.NotEmpty(t, d.MaterializationSHA256)
	var transformed struct {
		ExecutionProject string
		Transformations  []struct{ Source, SourceSHA256, Derived, DerivedSHA256 string }
	}
	require.NoError(t, readJSON(d.Materialization, &transformed))
	require.Equal(t, d.Workspace, transformed.ExecutionProject)
	require.Len(t, transformed.Transformations, 1)
	change := transformed.Transformations[0]
	original, err := os.ReadFile(filepath.Join(source, name))
	require.NoError(t, err)
	require.Equal(t, saved.Bytes(), original)
	originalCopy, err := os.ReadFile(filepath.Join(snapshot, strings.TrimPrefix(name, "config/")))
	require.NoError(t, err)
	require.Equal(t, original, originalCopy)
	derived, err := os.ReadFile(filepath.Join(snapshot, strings.TrimPrefix(change.Derived, "config/")))
	require.NoError(t, err)
	require.Equal(t, bytes.ReplaceAll(original, []byte(originalCWD), []byte(transformed.ExecutionProject)), derived)
	for _, item := range []struct{ path, hash string }{{filepath.Join(source, change.Source), change.SourceSHA256}, {filepath.Join(snapshot, strings.TrimPrefix(change.Derived, "config/")), change.DerivedSHA256}} {
		b, err := os.ReadFile(item.path)
		digest := digest(b)
		require.NoError(t, err)
		require.Equal(t, item.hash, digest)
	}
	unchanged, err := os.ReadFile(filepath.Join(source, "raw/raw.jsonl"))
	require.NoError(t, err)
	require.Equal(t, raw.Bytes(), unchanged)
	require.NoDirExists(t, originalCWD)
	require.NoDirExists(t, d.ResourceRoot)
	var finish struct {
		Status          string
		CleanupComplete bool
	}
	require.NoError(t, readJSON(filepath.Join(output, "preserve/finish.json"), &finish))
	require.Equal(t, "passed", finish.Status)
	require.True(t, finish.CleanupComplete)
}
