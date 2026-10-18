// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	claude "github.com/zoumo/agent-sdk-go/claude"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if os.Getenv(peerEnvironment) == "1" {
		if err := peer(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestOfflineExample(t *testing.T) {
	var output bytes.Buffer
	{
		err := run(&output)
		require.NoError(t, err)
	}
	expected := strings.Split(strings.TrimSpace(frames), "\n")
	associated := make(map[string]bool)
	created := make(map[string]bool)
	nativeCount, rootResult, childAfter, nested, terminal, closed := 0, false, false, false, false, false
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var header struct {
			Type string `json:"type"`
			claude.SubagentInfo
		}
		{
			err := json.Unmarshal([]byte(line), &header)
			require.NoError(t, err)
		}
		switch header.Type {
		case "subagent_associated":
			associated[header.Source.AssociationID] = true
			if header.Source.TaskID == "grandchild" && header.Parent != nil && header.Parent.TaskID == "child" {
				nested = true
			}
		case "subagent_created":
			require.True(t, associated[header.Source.AssociationID], header)
			require.False(t, created[header.Source.TaskID], header)
			created[header.Source.TaskID] = true
		case "observation_state":
			closed = closed || header.State == "closed"
		default:
			require.Falsef(t, nativeCount >= len(expected), "native changed: %s", line)
			require.EqualValuesf(t, expected[nativeCount], line, "native changed: %s", line)
			nativeCount++
			e, err := claude.ParseEvent([]byte(line))
			require.NoError(t, err)
			if e.Result != nil {
				rootResult = true
			}
			if e.Assistant != nil && e.ParentToolUseID != nil && rootResult {
				childAfter = true
			}
			if e.Subtype == "task_updated" && e.System != nil && strings.Contains(string(e.System.Patch), "killed") {
				terminal = true
			}
		}
	}
	require.Equal(t, len(expected), nativeCount, "native frame count")
	for _, tc := range []struct {
		name     string
		observed bool
	}{
		{"root result", rootResult}, {"child after root result", childAfter},
		{"nested association", nested}, {"terminal patch", terminal}, {"closed state", closed},
	} {
		t.Run(tc.name, func(t *testing.T) { require.True(t, tc.observed) })
	}
	require.Len(t, created, 2, "created agents")
}
