package agentclaude_test

import (
	"bufio"
	"encoding/json"
	"errors"
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
	VersionOutput      string     `json:"versionOutput,omitempty"`
	VersionExit        bool       `json:"versionExit,omitempty"`
	SessionID          string     `json:"sessionId,omitempty"`
	CWD                string     `json:"cwd,omitempty"`
	TranscriptPath     string     `json:"transcriptPath,omitempty"`
	Frames             []string   `json:"frames,omitempty"`
	FrameDelays        []int      `json:"frameDelays,omitempty"`
	Mutation           string     `json:"mutation,omitempty"`
	Replacement        string     `json:"replacement,omitempty"`
	ObservationPath    string     `json:"observationPath,omitempty"`
	SessionMarker      string     `json:"sessionMarker,omitempty"`
	ArgvPath           string     `json:"argvPath,omitempty"`
	EnvironmentKeys    []string   `json:"environmentKeys,omitempty"`
	VersionEnvPath     string     `json:"versionEnvPath,omitempty"`
	SessionEnvPath     string     `json:"sessionEnvPath,omitempty"`
	WaitForUser        bool       `json:"waitForUser,omitempty"`
	BeforeInput        []string   `json:"beforeInput,omitempty"`
	TurnFrames         [][]string `json:"turnFrames,omitempty"`
	InitializeError    bool       `json:"initializeError,omitempty"`
	InitializeDelay    int        `json:"initializeDelay,omitempty"`
	InitialMode        *string    `json:"initialMode,omitempty"`
	InitializeBody     string     `json:"initializeBody,omitempty"`
	SandboxSettings    string     `json:"sandboxSettings,omitempty"`
	SkipSandboxProbe   bool       `json:"skipSandboxProbe,omitempty"`
	SandboxUnavailable bool       `json:"sandboxUnavailable,omitempty"`
	StatusResponse     string     `json:"statusResponse,omitempty"`
	StatusWrongID      bool       `json:"statusWrongId,omitempty"`
	StatusError        bool       `json:"statusError,omitempty"`
	StatusIgnore       bool       `json:"statusIgnore,omitempty"`
	AfterStatus        []string   `json:"afterStatus,omitempty"`
	AfterStatusDelays  []int      `json:"afterStatusDelays,omitempty"`
}

type claudeResumePeerObservation struct {
	CallbackID string            `json:"callbackId"`
	Hooks      json.RawMessage   `json:"hooks"`
	Responses  []json.RawMessage `json:"responses"`
	UserFrames []json.RawMessage `json:"userFrames"`
	SessionID  string            `json:"sessionId"`
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
		if err := writeClaudePeerEnvironment(scenario.VersionEnvPath, scenario.EnvironmentKeys); err != nil {
			return 73
		}
		if scenario.VersionExit {
			return 70
		}
		output := scenario.VersionOutput
		if output == "" {
			output = "2.1.293 (Claude Code)\n"
		}
		_, _ = fmt.Fprint(os.Stdout, output)
		return 0
	}
	if err := writeClaudePeerEnvironment(scenario.SessionEnvPath, scenario.EnvironmentKeys); err != nil {
		return 74
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
	assignedID := ""
	claiming := false
	resuming := false
	var permissionMode string
	var settingsJSON string
	for _, arg := range args {
		if value, ok := strings.CutPrefix(arg, "--session-id="); ok {
			assignedID = value
			scenario.SessionID = value
		}
		if arg == "--await-claim" {
			claiming = true
		}
		if arg == "--resume" || strings.HasPrefix(arg, "--resume=") {
			resuming = true
		}
		if value, ok := strings.CutPrefix(arg, "--permission-mode="); ok {
			permissionMode = value
		}
		if value, ok := strings.CutPrefix(arg, "--settings="); ok {
			settingsJSON = value
		}
	}
	reader := bufio.NewReader(os.Stdin)
	initialize, err := reader.ReadBytes('\n')
	if err != nil {
		return 63
	}
	if scenario.SandboxUnavailable {
		_, _ = fmt.Fprintln(os.Stderr, "Error: sandbox required but unavailable: bubblewrap (bwrap) not installed")
		return 1
	}
	var request struct {
		RequestID string `json:"request_id"`
		Request   struct {
			Hooks json.RawMessage `json:"hooks"`
		} `json:"request"`
	}
	if json.Unmarshal(initialize, &request) != nil || request.RequestID == "" {
		return 64
	}
	callbackID := ""
	var hooks map[string][]struct {
		HookCallbackIDs []string `json:"hookCallbackIds"`
	}
	if len(request.Request.Hooks) > 0 && json.Unmarshal(request.Request.Hooks, &hooks) != nil {
		return 64
	}
	for _, matcher := range hooks["SessionStart"] {
		for _, candidate := range matcher.HookCallbackIDs {
			if strings.HasPrefix(candidate, "agent-sdk-go:claude:resume-probe:") {
				callbackID = candidate
			}
		}
	}
	if callbackID == "" && assignedID == "" && !claiming && !resuming {
		return 65
	}
	observation := &claudeResumePeerObservation{
		CallbackID: callbackID,
		Hooks:      append(json.RawMessage(nil), request.Request.Hooks...),
		SessionID:  scenario.SessionID,
	}
	writeClaudeResumeObservation(scenario.ObservationPath, observation)
	if err := applyClaudeResumeMutation(&scenario); err != nil {
		return 66
	}
	if scenario.InitializeDelay > 0 {
		time.Sleep(time.Duration(scenario.InitializeDelay) * time.Millisecond)
	}
	mode := permissionMode
	if scenario.InitialMode != nil {
		mode = *scenario.InitialMode
	}
	responseBody := "{}"
	if mode != "" {
		responseBody = fmt.Sprintf(`{"current_permission_mode":%q}`, mode)
	}
	if scenario.InitializeBody != "" {
		responseBody = scenario.InitializeBody
	}
	response := fmt.Sprintf(
		`{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":%s}}`,
		request.RequestID, responseBody,
	)
	if scenario.InitializeError {
		response = fmt.Sprintf(
			`{"type":"control_response","response":{"subtype":"error","request_id":%q,"error":"initialize rejected"}}`,
			request.RequestID,
		)
	}
	if _, err := fmt.Fprintln(os.Stdout, response); err != nil {
		return 67
	}
	if claiming {
		claim, err := reader.ReadBytes('\n')
		if err != nil {
			return 75
		}
		var request struct {
			RequestID string `json:"request_id"`
			Request   struct {
				Subtype string
				CWD     string `json:"cwd"`
			} `json:"request"`
		}
		if json.Unmarshal(claim, &request) != nil ||
			request.RequestID == "" ||
			request.Request.Subtype != "claim_session" {
			return 76
		}
		reply := fmt.Sprintf(
			`{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":{"status":"ok","cwd":%q,"session_id":%q}}}`,
			request.RequestID,
			request.Request.CWD,
			scenario.SessionID,
		)
		if _, err := fmt.Fprintln(os.Stdout, reply); err != nil {
			return 77
		}
	}
	if !scenario.InitializeError && !scenario.SkipSandboxProbe &&
		permissionMode != "" && permissionMode == mode && settingsJSON != "" {
		settings := scenario.SandboxSettings
		if settings == "" {
			settings = `{"effective":` + settingsJSON + `}`
		}
		if err := replyToClaudeSandboxProbe(reader, settings); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			return 81
		}
	}
	emit := func(frame string) error {
		frame = strings.NewReplacer(
			claudeResumePeerCallbackID, callbackID,
			claudeResumePeerSessionIDID, scenario.SessionID,
			claudeResumePeerCwdID, scenario.CWD,
			claudeResumePeerTranscriptID, scenario.TranscriptPath,
		).Replace(frame)
		_, err := fmt.Fprintln(os.Stdout, frame)
		return err
	}
	statusRequestID := ""
	if resuming {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return 78
		}
		var wire struct {
			RequestID string `json:"request_id"`
			Request   struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
		}
		if json.Unmarshal(line, &wire) != nil || wire.Request.Subtype != "get_status" || wire.RequestID == "" {
			return 79
		}
		statusRequestID = wire.RequestID
	}
	for _, frame := range scenario.BeforeInput {
		if emit(frame) != nil {
			return 68
		}
	}
	if scenario.WaitForUser {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			writeClaudeResumeObservation(scenario.ObservationPath, observation)
			return 0
		}
		observation.UserFrames = append(observation.UserFrames, append(json.RawMessage(nil), line...))
		writeClaudeResumeObservation(scenario.ObservationPath, observation)
	}
	for _, frames := range scenario.TurnFrames {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			writeClaudeResumeObservation(scenario.ObservationPath, observation)
			return 0
		}
		var wire struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(line, &wire) == nil && wire.Type == "user" {
			observation.UserFrames = append(observation.UserFrames, append(json.RawMessage(nil), line...))
			writeClaudeResumeObservation(scenario.ObservationPath, observation)
		}
		for _, frame := range frames {
			if emit(frame) != nil {
				return 68
			}
		}
	}
	for index, frame := range scenario.Frames {
		if index < len(scenario.FrameDelays) && scenario.FrameDelays[index] > 0 {
			time.Sleep(time.Duration(scenario.FrameDelays[index]) * time.Millisecond)
		}
		if emit(frame) != nil {
			return 68
		}
	}
	if resuming && !scenario.StatusIgnore {
		body := scenario.StatusResponse
		if body == "" {
			body = resumeStatusBody(scenario.SessionID, scenario.CWD)
		}
		if scenario.StatusWrongID {
			statusRequestID = "unrelated-request"
		}
		response := fmt.Sprintf(`{"type":"control_response","response":{"subtype":"success","request_id":%q,"response":%s}}`, statusRequestID, body)
		if scenario.StatusError {
			response = fmt.Sprintf(`{"type":"control_response","response":{"subtype":"error","request_id":%q,"error":"status unavailable"}}`, statusRequestID)
		}
		if emit(response) != nil {
			return 68
		}
		for index, frame := range scenario.AfterStatus {
			if index < len(scenario.AfterStatusDelays) && scenario.AfterStatusDelays[index] > 0 {
				time.Sleep(time.Duration(scenario.AfterStatusDelays[index]) * time.Millisecond)
			}
			if emit(frame) != nil {
				return 68
			}
		}
	}

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var wire struct {
				Type      string `json:"type"`
				RequestID string `json:"request_id"`
				Request   struct {
					Subtype string `json:"subtype"`
				} `json:"request"`
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
			if wire.Type == "user" {
				observation.UserFrames = append(observation.UserFrames, append(json.RawMessage(nil), line...))
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

func replyToClaudeSandboxProbe(reader *bufio.Reader, settings string) error {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return err
	}
	var wire struct {
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype string `json:"subtype"`
		} `json:"request"`
	}
	if json.Unmarshal(line, &wire) != nil || wire.Request.Subtype != "get_settings" || wire.RequestID == "" {
		return errors.New("expected get_settings control")
	}
	_, err = fmt.Fprintf(os.Stdout, "{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":%q,\"response\":%s}}\n", wire.RequestID, settings)
	return err
}

func applyClaudeResumeMutation(scenario *claudeResumePeerScenario) error {
	switch scenario.Mutation {
	case "":
		return nil
	case "unrelated-project-entry":
		return os.WriteFile(
			filepath.Join(filepath.Dir(filepath.Dir(scenario.TranscriptPath)), ".DS_Store"),
			[]byte("unrelated project entry"), 0o600,
		)
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

func writeClaudePeerEnvironment(path string, keys []string) error {
	if path == "" {
		return nil
	}
	env := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			env[key] = value
		}
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
