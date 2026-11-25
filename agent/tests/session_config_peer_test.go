package agent_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zoumo/agent-sdk-go/internal/testsupport/nativepeer"
)

func TestMain(m *testing.M) { nativepeer.TestMain(m, agentExecutionPeer) }

func agentExecutionPeer(provider, scenario string) (err error) {
	if provider == "claude" && len(os.Args) == 2 && os.Args[1] == "--version" {
		_, err := fmt.Fprintln(os.Stdout, "2.1.293 (Claude Code)")
		return err
	}
	if scenario != agentExecutionSuccess &&
		scenario != agentExecutionActive &&
		scenario != agentExecutionResume &&
		scenario != agentExecutionReject &&
		scenario != agentExecutionUnknown {
		return fmt.Errorf("unsupported agent execution peer scenario %s/%s", provider, scenario)
	}
	sessionID := agentExecutionSessionID
	resumeSeen := false
	for _, arg := range os.Args[1:] {
		if value, ok := strings.CutPrefix(arg, "--session-id="); ok {
			sessionID = value
		}
		if value, ok := strings.CutPrefix(arg, "--resume="); ok {
			sessionID = value
			resumeSeen = true
		}
	}
	if provider == "claude" && scenario == agentExecutionResume && !resumeSeen {
		return errors.New("agent execution resume scenario requires --resume")
	}
	if provider == "claude" && scenario != agentExecutionResume && resumeSeen {
		return errors.New("non-resume agent execution scenario received --resume")
	}
	tracer, err := openAgentExecutionTracer()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, tracer.Close()) }()
	if err := tracer.Record("argv", os.Args[1:]); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	codexTurnCount := 0
	codexActiveTurn := ""
	claudeTurnCount := 0
	claudeActiveTurn := false
	for scanner.Scan() {
		raw := append(json.RawMessage(nil), scanner.Bytes()...)
		var frame struct {
			ID        json.RawMessage `json:"id"`
			Method    string          `json:"method"`
			Type      string          `json:"type"`
			RequestID string          `json:"request_id"`
			Params    json.RawMessage `json:"params"`
			Request   struct {
				Subtype string `json:"subtype"`
			} `json:"request"`
		}
		if err := json.Unmarshal(raw, &frame); err != nil {
			return err
		}
		if err := tracer.Record("frame", raw); err != nil {
			return err
		}
		switch provider {
		case "codex":
			switch frame.Method {
			case "initialize":
				if err := encoder.Encode(map[string]any{
					"id": frame.ID,
					"result": map[string]any{
						"userAgent":      "synthetic",
						"codexHome":      "/synthetic",
						"platformFamily": "unix",
						"platformOs":     "linux",
					},
				}); err != nil {
					return err
				}
			case "initialized":
			case "thread/start":
				if scenario == agentExecutionResume {
					return errors.New("resume scenario used thread/start")
				}
				if err := encoder.Encode(map[string]any{
					"id":     frame.ID,
					"result": agentExecutionCodexResult(sessionID, cwd),
				}); err != nil {
					return err
				}
			case "thread/resume":
				if scenario != agentExecutionResume {
					return errors.New("non-resume scenario used thread/resume")
				}
				var params struct {
					ThreadID string `json:"threadId"`
				}
				if err := json.Unmarshal(frame.Params, &params); err != nil {
					return err
				}
				if params.ThreadID != sessionID {
					return fmt.Errorf(
						"resume thread id %q, want %q",
						params.ThreadID,
						sessionID,
					)
				}
				if err := encoder.Encode(map[string]any{
					"id":     frame.ID,
					"result": agentExecutionCodexResult(sessionID, cwd),
				}); err != nil {
					return err
				}
			case "thread/settings/update":
				if scenario == agentExecutionUnknown {
					return nil
				}
				if scenario == agentExecutionReject {
					if err := encoder.Encode(map[string]any{
						"id": frame.ID,
						"error": map[string]any{
							"code":    -32602,
							"message": "unsupported model",
						},
					}); err != nil {
						return err
					}
					continue
				}
				if err := encoder.Encode(map[string]any{
					"id":     frame.ID,
					"result": map[string]any{},
				}); err != nil {
					return err
				}
				if scenario == agentExecutionActive && codexActiveTurn != "" {
					if err := encoder.Encode(map[string]any{
						"method": "turn/completed",
						"params": map[string]any{
							"threadId": sessionID,
							"turn": map[string]any{
								"id":     codexActiveTurn,
								"status": "completed",
								"items":  []any{},
								"error":  nil,
							},
						},
					}); err != nil {
						return err
					}
					codexActiveTurn = ""
				}
			case "thread/unsubscribe":
				if err := encoder.Encode(map[string]any{
					"id":     frame.ID,
					"result": map[string]any{"status": "unsubscribed"},
				}); err != nil {
					return err
				}
			case "turn/start":
				codexTurnCount++
				turnID := fmt.Sprintf("turn-after-update-%d", codexTurnCount)
				if err := encoder.Encode(map[string]any{
					"id": frame.ID,
					"result": map[string]any{
						"turn": map[string]any{
							"id":     turnID,
							"status": "inProgress",
							"items":  []any{},
							"error":  nil,
						},
					},
				}); err != nil {
					return err
				}
				if err := encoder.Encode(map[string]any{
					"method": "turn/started",
					"params": map[string]any{
						"threadId": sessionID,
						"turn": map[string]any{
							"id":     turnID,
							"status": "inProgress",
							"items":  []any{},
							"error":  nil,
						},
					},
				}); err != nil {
					return err
				}
				if scenario == agentExecutionActive && codexTurnCount == 1 {
					codexActiveTurn = turnID
					continue
				}
				if err := encoder.Encode(map[string]any{
					"method": "turn/completed",
					"params": map[string]any{
						"threadId": sessionID,
						"turn": map[string]any{
							"id":     turnID,
							"status": "completed",
							"items":  []any{},
							"error":  nil,
						},
					},
				}); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unexpected agent execution Codex request: %s", raw)
			}
		case "claude":
			if frame.Type == "user" {
				claudeTurnCount++
				at := time.Now().UTC().Format(time.RFC3339Nano)
				if err := encoder.Encode(map[string]any{
					"type":       "system",
					"subtype":    "session_state_changed",
					"session_id": sessionID,
					"state":      "running",
					"timestamp":  at,
				}); err != nil {
					return err
				}
				if scenario == agentExecutionActive && claudeTurnCount == 1 {
					claudeActiveTurn = true
					continue
				}
				if err := encoder.Encode(map[string]any{
					"type":            "result",
					"subtype":         "success",
					"session_id":      sessionID,
					"is_error":        false,
					"result":          "synthetic execution completed",
					"num_turns":       1,
					"duration_ms":     1,
					"duration_api_ms": 1,
					"total_cost_usd":  0,
					"usage":           map[string]any{},
					"timestamp":       at,
				}); err != nil {
					return err
				}
				if err := encoder.Encode(map[string]any{
					"type":       "system",
					"subtype":    "session_state_changed",
					"session_id": sessionID,
					"state":      "idle",
					"timestamp":  at,
				}); err != nil {
					return err
				}
				continue
			}
			if frame.Type != "control_request" {
				return fmt.Errorf("unexpected agent execution Claude frame: %s", raw)
			}
			response := any(map[string]any{})
			switch frame.Request.Subtype {
			case "get_status":
				response = map[string]any{"sections": []any{map[string]any{"rows": []any{map[string]any{"label": "Session ID", "value": sessionID}, map[string]any{"label": "cwd", "value": cwd}}}}}
			case "get_settings":
				response = json.RawMessage(`{
					"effective":{"sandbox":{"enabled":false}},
					"applied":{"ultracode":false}
				}`)
			case "claim_session":
				response = map[string]any{
					"status":     "ok",
					"cwd":        cwd,
					"session_id": sessionID,
				}
			case "initialize":
				response = map[string]any{"current_permission_mode": "bypassPermissions"}
			case "set_model", "apply_flag_settings":
			default:
				return fmt.Errorf(
					"unexpected agent execution Claude control request: %s",
					raw,
				)
			}
			if scenario == agentExecutionReject &&
				(frame.Request.Subtype == "set_model" ||
					frame.Request.Subtype == "apply_flag_settings") {
				if err := encoder.Encode(map[string]any{
					"type": "control_response",
					"response": map[string]any{
						"subtype":    "error",
						"request_id": frame.RequestID,
						"error":      "unsupported model",
					},
				}); err != nil {
					return err
				}
				continue
			}
			if scenario == agentExecutionUnknown &&
				(frame.Request.Subtype == "set_model" ||
					frame.Request.Subtype == "apply_flag_settings") {
				return nil
			}
			if err := encoder.Encode(map[string]any{
				"type": "control_response",
				"response": map[string]any{
					"subtype":    "success",
					"request_id": frame.RequestID,
					"response":   response,
				},
			}); err != nil {
				return err
			}
			if frame.Request.Subtype == "initialize" {
				if err := encoder.Encode(map[string]any{
					"type":                "system",
					"subtype":             "init",
					"session_id":          sessionID,
					"cwd":                 cwd,
					"claude_code_version": "2.1.293",
					"model":               "observed/model",
				}); err != nil {
					return err
				}
			}
			if scenario == agentExecutionActive &&
				claudeActiveTurn &&
				(frame.Request.Subtype == "set_model" ||
					frame.Request.Subtype == "apply_flag_settings") {
				at := time.Now().UTC().Format(time.RFC3339Nano)
				if err := encoder.Encode(map[string]any{
					"type":            "result",
					"subtype":         "success",
					"session_id":      sessionID,
					"is_error":        false,
					"result":          "synthetic execution completed",
					"num_turns":       1,
					"duration_ms":     1,
					"duration_api_ms": 1,
					"total_cost_usd":  0,
					"usage":           map[string]any{},
					"timestamp":       at,
				}); err != nil {
					return err
				}
				if err := encoder.Encode(map[string]any{
					"type":       "system",
					"subtype":    "session_state_changed",
					"session_id": sessionID,
					"state":      "idle",
					"timestamp":  at,
				}); err != nil {
					return err
				}
				claudeActiveTurn = false
			}
		default:
			return fmt.Errorf("unknown agent execution provider %q", provider)
		}
	}
	return scanner.Err()
}
