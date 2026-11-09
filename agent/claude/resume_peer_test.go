package agentclaude_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	claudeResumePeerEnv          = "AGENT_CLAUDE_TEST_RESUME_PEER"
	claudeResumePeerSessionID    = "11111111-1111-4111-8111-111111111111"
	claudeResumePeerTranscriptID = "__TRANSCRIPT_PATH__"
	claudeResumePeerCallbackID   = "__CALLBACK_ID__"
	claudeResumePeerCwdID        = "__CWD__"
	claudeResumePeerSessionIDID  = "__SESSION_ID__"
)

type claudeResumePeerScenario struct {
	VersionOutput   string   `json:"versionOutput,omitempty"`
	VersionExit     bool     `json:"versionExit,omitempty"`
	SessionID       string   `json:"sessionId,omitempty"`
	CWD             string   `json:"cwd,omitempty"`
	TranscriptPath  string   `json:"transcriptPath,omitempty"`
	Frames          []string `json:"frames,omitempty"`
	FrameDelays     []int    `json:"frameDelays,omitempty"`
	Mutation        string   `json:"mutation,omitempty"`
	Replacement     string   `json:"replacement,omitempty"`
	ObservationPath string   `json:"observationPath,omitempty"`
	SessionMarker   string   `json:"sessionMarker,omitempty"`
	ArgvPath        string   `json:"argvPath,omitempty"`
}

type claudeResumePeerObservation struct {
	CallbackID string            `json:"callbackId"`
	Hooks      json.RawMessage   `json:"hooks"`
	Responses  []json.RawMessage `json:"responses"`
}

type claudeResumePeer struct {
	CLIPath     string
	Env         []string
	SessionMark string
	Observation string
}

func newClaudeResumePeer(
	t *testing.T,
	scenario claudeResumePeerScenario,
) claudeResumePeer {
	t.Helper()
	directory := t.TempDir()
	if scenario.SessionID == "" {
		scenario.SessionID = claudeResumePeerSessionID
	}
	scenario.ObservationPath = filepath.Join(directory, "observation.json")
	scenario.SessionMarker = filepath.Join(directory, "session-started")
	path := filepath.Join(directory, "scenario.json")
	data, err := json.Marshal(scenario)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	executable, err := os.Executable()
	require.NoError(t, err)
	env := append([]string(nil), os.Environ()...)
	env = append(
		env,
		claudeResumePeerEnv+"="+path,
		"HOME="+t.TempDir(),
		"GORACE=atexit_sleep_ms=0",
	)
	return claudeResumePeer{
		CLIPath:     executable,
		Env:         env,
		SessionMark: scenario.SessionMarker,
		Observation: scenario.ObservationPath,
	}
}

func runClaudeResumePeer(scenarioPath string, args []string) int {
	data, err := os.ReadFile(scenarioPath)
	if err != nil {
		return 60
	}
	var scenario claudeResumePeerScenario
	if json.Unmarshal(data, &scenario) != nil {
		return 61
	}
	if len(args) == 1 && args[0] == "--version" {
		if scenario.VersionExit {
			return 70
		}
		output := scenario.VersionOutput
		if output == "" {
			output = "2.1.258 (Claude Code)\n"
		}
		_, _ = fmt.Fprint(os.Stdout, output)
		return 0
	}
	if scenario.ArgvPath != "" {
		data, err := json.Marshal(args)
		if err != nil {
			return 71
		}
		if err := os.WriteFile(scenario.ArgvPath, data, 0o600); err != nil {
			return 72
		}
	}
	if err := os.WriteFile(scenario.SessionMarker, []byte("started\n"), 0o600); err != nil {
		return 62
	}

	reader := bufio.NewReader(os.Stdin)
	initialize, err := reader.ReadBytes('\n')
	if err != nil {
		return 63
	}
	var request struct {
		RequestID string `json:"request_id"`
		Request   struct {
			Hooks map[string][]struct {
				HookCallbackIDs []string `json:"hookCallbackIds"`
			} `json:"hooks"`
		} `json:"request"`
	}
	if json.Unmarshal(initialize, &request) != nil || request.RequestID == "" {
		return 64
	}
	callbackID := ""
	for _, matcher := range request.Request.Hooks["SessionStart"] {
		for _, candidate := range matcher.HookCallbackIDs {
			if strings.HasPrefix(candidate, "agent-sdk-go:claude:resume-probe:") {
				callbackID = candidate
			}
		}
	}
	if callbackID == "" {
		return 65
	}
	observation := &claudeResumePeerObservation{
		CallbackID: callbackID,
		Hooks:      mustMarshalJSON(request.Request.Hooks),
	}
	writeClaudeResumeObservation(scenario.ObservationPath, observation)
	if err := applyClaudeResumeMutation(&scenario); err != nil {
		return 66
	}
	response := fmt.Sprintf(
		`{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{}}}`,
		request.RequestID,
	)
	if _, err := fmt.Fprintln(os.Stdout, response); err != nil {
		return 67
	}
	for index, frame := range scenario.Frames {
		if index < len(scenario.FrameDelays) && scenario.FrameDelays[index] > 0 {
			time.Sleep(time.Duration(scenario.FrameDelays[index]) * time.Millisecond)
		}
		frame = strings.NewReplacer(
			claudeResumePeerCallbackID, callbackID,
			claudeResumePeerSessionIDID, scenario.SessionID,
			claudeResumePeerCwdID, scenario.CWD,
			claudeResumePeerTranscriptID, scenario.TranscriptPath,
		).Replace(frame)
		if _, err := fmt.Fprintln(os.Stdout, frame); err != nil {
			return 68
		}
	}

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var wire struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(line, &wire) != nil {
				return 69
			}
			if wire.Type == "control_response" {
				observation.Responses = append(
					observation.Responses,
					append(json.RawMessage(nil), line...),
				)
				writeClaudeResumeObservation(scenario.ObservationPath, observation)
			}
		}
		if err != nil {
			break
		}
	}
	writeClaudeResumeObservation(scenario.ObservationPath, observation)
	return 0
}

func applyClaudeResumeMutation(scenario *claudeResumePeerScenario) error {
	switch scenario.Mutation {
	case "":
		return nil
	case "append":
		file, err := os.OpenFile(scenario.TranscriptPath, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		_, writeErr := file.WriteString(
			`{"type":"custom-title","customTitle":"grew during resume"}` + "\n",
		)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		return closeErr
	case "replace":
		directory := filepath.Dir(scenario.TranscriptPath)
		temporary, err := os.CreateTemp(directory, ".resume-replacement-*")
		if err != nil {
			return err
		}
		name := temporary.Name()
		if _, err := temporary.WriteString(scenario.Replacement); err != nil {
			temporary.Close()
			os.Remove(name)
			return err
		}
		if err := temporary.Close(); err != nil {
			os.Remove(name)
			return err
		}
		return os.Rename(name, scenario.TranscriptPath)
	case "remove":
		return os.Remove(scenario.TranscriptPath)
	default:
		return fmt.Errorf("unknown mutation %q", scenario.Mutation)
	}
}

func writeClaudeResumeObservation(
	path string,
	observation *claudeResumePeerObservation,
) {
	data, err := json.Marshal(observation)
	if err != nil {
		return
	}
	temporary := path + ".new"
	if os.WriteFile(temporary, data, 0o600) == nil {
		_ = os.Rename(temporary, path)
	}
}

func mustMarshalJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return data
}
