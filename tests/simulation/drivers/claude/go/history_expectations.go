package claudesim

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/zoumo/agent-sdk-go/claude"
)

func savedHistoryExpectations(root, sessionID string) ([]map[string]json.RawMessage, error) {
	var plan struct {
		HistoryBindings []struct {
			Role, Source, SHA256 string
			SessionID            string `json:"sessionId"`
			ParentSessionID      string `json:"parentSessionId"`
			ChildSessionID       string `json:"childSessionId"`
		}
		Scenario struct {
			Parameters struct{ HistoryTarget string }
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "plan.json"))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, err
	}
	var receipts []map[string]json.RawMessage
	appendReceipt := func(name string, value any) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		receipts = append(receipts, map[string]json.RawMessage{name: raw})
		return nil
	}
	for _, binding := range plan.HistoryBindings {
		if binding.Role != "runtime" || !strings.HasSuffix(binding.Source, ".jsonl") ||
			(binding.SessionID != sessionID && binding.ParentSessionID != sessionID) {
			continue
		}
		if !filepath.IsLocal(binding.Source) {
			return nil, errors.New("saved history source escapes the SDK projection")
		}
		raw, err := os.ReadFile(filepath.Join(root, binding.Source))
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256(raw)
		if hex.EncodeToString(hash[:]) != binding.SHA256 {
			return nil, errors.New("saved history expectation bytes changed")
		}
		messages, err := recordedHistoryMessages(raw, sessionID, binding.ChildSessionID != "")
		if err != nil {
			return nil, err
		}
		if binding.ChildSessionID != "" {
			err = appendReceipt("get_subagent_messages", map[string]any{
				"SessionID": sessionID, "AgentID": binding.ChildSessionID, "Messages": messages,
			})
		} else {
			err = appendReceipt("get_session_messages", messages)
			if err == nil && plan.Scenario.Parameters.HistoryTarget == "pagination" {
				for _, slice := range [][2]int{{1, 0}, {2, 1}, {2, len(messages)}, {1, len(messages) + 1}} {
					limit, offset := slice[0], slice[1]
					start := min(offset, len(messages))
					end := min(start+limit, len(messages))
					if err := appendReceipt("history-slice", map[string]any{
						"Limit": limit, "Offset": offset, "Messages": messages[start:end],
					}); err != nil {
						return nil, err
					}
				}
			}
		}
		if err != nil {
			return nil, err
		}
	}
	if len(receipts) == 0 {
		return nil, errors.New("recording has no bound saved history expectations")
	}
	return receipts, nil
}

func recordedHistoryMessages(data []byte, sessionID string, child bool) ([]claude.SessionMessage, error) {
	messages := []claude.SessionMessage{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 16<<20)
	seen := map[string]bool{}
	for scanner.Scan() {
		var record struct {
			Type, UUID, TeamName string
			SessionID            string `json:"sessionId"`
			IsMeta, IsSidechain  bool
			Message              json.RawMessage
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, err
		}
		if record.Type != "user" && record.Type != "assistant" {
			continue
		}
		if !child && (record.IsMeta || record.IsSidechain || record.TeamName != "") {
			continue
		}
		if record.UUID == "" || seen[record.UUID] || record.SessionID != sessionID || len(record.Message) == 0 {
			return nil, errors.New("saved history expectation has invalid native identity")
		}
		seen[record.UUID] = true
		messages = append(messages, claude.SessionMessage{
			Type: record.Type, UUID: record.UUID, SessionID: sessionID, Message: record.Message,
		})
	}
	return messages, scanner.Err()
}
