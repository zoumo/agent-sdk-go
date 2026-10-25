// Child parentUuid selection follows the official Python SDK at
// 0e89b298e771259378a6cd3734e00d323dff5703. MIT notice: NOTICE.
package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
)

func (r *childReader) bad(path, kind, reason string, raw []byte) error {
	return &SessionQueryError{Kind: kind, Path: r.absolute(path), Reason: reason, Raw: bytes.Clone(raw)}
}
func childObject(raw []byte) (map[string]json.RawMessage, error) {
	if e := validateDiscoveryJSON(raw); e != nil {
		return nil, e
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(raw, &fields); e != nil || fields == nil {
		return nil, errors.New("expected JSON object")
	}
	return fields, nil
}
func childIdentity(fields map[string]json.RawMessage, parent, agent string) error {
	for _, item := range []struct {
		keys []string
		want string
	}{{[]string{"sessionId", "session_id"}, parent}, {[]string{"agentId", "agent_id"}, agent}} {
		for _, key := range item.keys {
			if _, present := fields[key]; !present {
				continue
			}
			v, e := discoveryString(fields, key)
			if e != nil || v == nil || *v == "" || (item.want != "" && *v != item.want) {
				return errors.New("source identity mismatch: " + key)
			}
		}
	}
	return nil
}

// Physical lines and line-byte limits apply to every source, including blank
// lines and multiline sidecars. JSONL must end with a newline; sidecars need not.
func (r *childReader) lines(data []byte, path string, jsonl bool, visit func([]byte) error) error {
	for len(data) > 0 {
		if e := r.ctx.Err(); e != nil {
			return e
		}
		if r.c.Lines >= r.c.Limits.MaxLines {
			return r.fail("resource-limit", path, "physical lines")
		}
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			i = len(data)
			if jsonl {
				return r.bad(path, "corrupt", "incomplete final JSONL line", data)
			}
		}
		line := data[:i]
		if len(line) > r.c.Limits.MaxLineBytes {
			return r.fail("resource-limit", path, "line bytes")
		}
		r.c.Lines++
		if e := visit(line); e != nil {
			return e
		}
		if i == len(data) {
			break
		}
		data = data[i+1:]
	}
	return nil
}
func (r *childReader) parse(data []byte, path, agent string) ([]SessionMessage, error) {
	records := []SessionMessage{}
	ids := map[string]bool{}
	e := r.lines(data, path, true, func(raw []byte) error {
		if len(bytes.TrimSpace(raw)) == 0 {
			return nil
		}
		fields, e := childObject(raw)
		if e != nil {
			return r.bad(path, "corrupt", e.Error(), raw)
		}
		if e = childIdentity(fields, r.c.SessionID, agent); e != nil {
			return r.bad(path, "corrupt", e.Error(), raw)
		}
		kind, e := discoveryString(fields, "type")
		if e != nil || kind == nil || *kind == "" {
			return r.bad(path, "corrupt", "missing/invalid type", raw)
		}
		switch *kind {
		case "user", "assistant", "progress", "system", "attachment":
		case "api-request-shape", "api-request-blob", "api-request", "cost-state":
			return nil
		default:
			if transcriptMetadata(*kind) {
				return nil
			}
			return r.bad(path, "unsupported", "unknown entry type", raw)
		}
		id, e := discoveryString(fields, "uuid")
		if e != nil || id == nil || *id == "" {
			return r.bad(path, "corrupt", "missing/invalid UUID", raw)
		}
		if ids[*id] {
			return r.bad(path, "corrupt", "duplicate UUID", raw)
		}
		ids[*id] = true
		parent, e := discoveryString(fields, "parentUuid")
		if e != nil {
			return r.bad(path, "corrupt", "invalid parentUuid", raw)
		}
		timestamp, e := discoveryString(fields, "timestamp")
		if e != nil {
			return r.bad(path, "corrupt", "invalid timestamp", raw)
		}
		if *kind == "user" || *kind == "assistant" {
			var message map[string]json.RawMessage
			if json.Unmarshal(fields["message"], &message) != nil || message == nil {
				return r.bad(path, "corrupt", "invalid message", raw)
			}
			content := bytes.TrimSpace(message["content"])
			if len(content) == 0 || (content[0] != '"' && content[0] != '[') {
				return r.bad(path, "corrupt", "invalid message content", raw)
			}
		}
		if r.c.Records >= r.c.Limits.MaxRecords {
			return r.fail("resource-limit", path, "indexed records")
		}
		r.c.Records++
		sid, _ := discoveryString(fields, "sessionId")
		if sid == nil {
			sid, _ = discoveryString(fields, "session_id")
		}
		record := SessionMessage{Type: *kind, UUID: *id, ParentUUID: parent, Timestamp: timestamp, Message: bytes.Clone(fields["message"]), Raw: bytes.Clone(raw)}
		if sid != nil {
			record.SessionID = *sid
		}
		records = append(records, record)
		return nil
	})
	return records, e
}
func (r *childReader) child(candidate childCandidate) (SubagentInfo, []SessionMessage, error) {
	info := SubagentInfo{SessionID: r.c.SessionID, AgentID: candidate.agent, Path: r.absolute(candidate.path), Running: "unknown"}
	data, e := r.read(candidate.path, false)
	if e != nil {
		return info, nil, e
	}
	records, e := r.parse(data, candidate.path, candidate.agent)
	if e != nil {
		return info, nil, e
	}
	sidecar := strings.TrimSuffix(candidate.path, ".jsonl") + ".meta.json"
	_, e = r.stat(sidecar)
	if errors.Is(e, os.ErrNotExist) {
		r.absent = append(r.absent, sidecar)
		return info, records, nil
	}
	if e != nil {
		return info, nil, e
	}
	data, e = r.read(sidecar, true)
	if e != nil {
		return info, nil, e
	}
	if e = r.lines(data, sidecar, false, func([]byte) error { return nil }); e != nil {
		return info, nil, e
	}
	fields, e := childObject(data)
	if e != nil {
		return info, nil, r.bad(sidecar, "corrupt", e.Error(), data)
	}
	if e = childIdentity(fields, r.c.SessionID, candidate.agent); e != nil {
		return info, nil, r.bad(sidecar, "corrupt", e.Error(), data)
	}
	tool, te := discoveryString(fields, "toolUseId")
	parent, pe := discoveryString(fields, "parentAgentId")
	if te != nil || pe != nil || (tool != nil && *tool == "") || (parent != nil && (!validChildAgent(*parent) || *parent == candidate.agent)) {
		return info, nil, r.bad(sidecar, "corrupt", "invalid sidecar parent/tool fields", data)
	}
	info.Sidecar = bytes.Clone(data)
	info.ParentAgentID = parent
	info.ParentToolUseID = tool
	return info, records, nil
}
func (r *childReader) chain(records []SessionMessage, path string) ([]SessionMessage, error) {
	byID := make(map[string]SessionMessage, len(records))
	leaf := ""
	for _, record := range records {
		byID[record.UUID] = record
		if record.Type == "user" || record.Type == "assistant" {
			leaf = record.UUID
		}
	}
	// Validate every branch before selecting one; a bad abandoned branch must not
	// hide behind a valid selected leaf. Linear memoization bounds traversal work.
	checked := map[string]bool{}
	for _, record := range records {
		visiting := map[string]bool{}
		walk := []string{}
		for id := record.UUID; id != "" && !checked[id]; {
			if e := r.ctx.Err(); e != nil {
				return nil, e
			}
			current, ok := byID[id]
			if !ok {
				return nil, r.bad(path, "corrupt", "missing parent UUID", record.Raw)
			}
			if visiting[id] {
				return nil, r.bad(path, "corrupt", "parent cycle", current.Raw)
			}
			visiting[id] = true
			walk = append(walk, id)
			id = ""
			if current.ParentUUID != nil {
				id = *current.ParentUUID
			}
		}
		for _, id := range walk {
			checked[id] = true
		}
	}
	chain := []SessionMessage{}
	for id := leaf; id != ""; {
		if e := r.ctx.Err(); e != nil {
			return nil, e
		}
		current := byID[id]
		chain = append(chain, current)
		id = ""
		if current.ParentUUID != nil {
			id = *current.ParentUUID
		}
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}
