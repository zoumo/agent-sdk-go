package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
)

func (q *childQuery) identity(fields map[string]json.RawMessage, agent, path string) error {
	for key, want := range map[string]string{"sessionId": q.session, "session_id": q.session, "agentId": agent, "agent_id": agent} {
		if _, ok := fields[key]; !ok {
			continue
		}
		value, e := discoveryString(fields, key)
		if e != nil || value == nil || *value != want {
			return queryError("corrupt", path, "source identity mismatch: "+key, nil)
		}
	}
	return nil
}
func (q *childQuery) parse(data []byte, path, agent string, chain bool) ([]SessionMessage, error) {
	records := []SessionMessage{}
	index := map[string]int{}
	leaf := -1
	scan := transcriptScanner(bytes.NewReader(data), q.c.Limits.MaxLineBytes)
	for scan.Scan() {
		if e := q.ctx.Err(); e != nil {
			return nil, e
		}
		if q.c.Lines >= q.c.Limits.MaxLines {
			return nil, queryError("resource-limit", path, "lines", nil)
		}
		q.c.Lines++
		raw := scan.Bytes()
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		bad := func(kind, reason string) error {
			return &SessionQueryError{Kind: kind, Path: path, Reason: reason, Raw: bytes.Clone(raw)}
		}
		if validateDiscoveryJSON(raw) != nil {
			return nil, bad("corrupt", "invalid JSON object")
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return nil, bad("corrupt", "object required")
		}
		if e := q.identity(fields, agent, path); e != nil {
			if qe, ok := e.(*SessionQueryError); ok {
				qe.Raw = bytes.Clone(raw)
			}
			return nil, e
		}
		kind, e := discoveryString(fields, "type")
		if e != nil || kind == nil {
			return nil, bad("corrupt", "invalid type")
		}
		switch *kind {
		case "user", "assistant", "progress", "system", "attachment":
		default:
			if transcriptMetadata(*kind) || *kind == "api-request-shape" || *kind == "api-request-blob" || *kind == "api-request" || *kind == "cost-state" {
				continue
			}
			return nil, bad("unsupported", "unknown entry type")
		}
		if q.c.Records >= q.c.Limits.MaxRecords {
			return nil, bad("resource-limit", "indexed records")
		}
		q.c.Records++
		if !chain {
			continue
		}
		id, e := discoveryString(fields, "uuid")
		if e != nil || id == nil || *id == "" {
			return nil, bad("corrupt", "missing UUID")
		}
		if _, exists := index[*id]; exists {
			return nil, bad("corrupt", "duplicate UUID")
		}
		parent, e := discoveryString(fields, "parentUuid")
		if e != nil || (parent != nil && *parent == "") {
			return nil, bad("corrupt", "invalid parentUuid")
		}
		stamp, e := discoveryString(fields, "timestamp")
		if e != nil {
			return nil, bad("corrupt", "invalid timestamp")
		}
		if *kind == "user" || *kind == "assistant" {
			var message map[string]json.RawMessage
			if json.Unmarshal(fields["message"], &message) != nil || message == nil {
				return nil, bad("corrupt", "invalid native message")
			}
			leaf = len(records)
		}
		index[*id] = len(records)
		records = append(records, SessionMessage{Type: *kind, UUID: *id, SessionID: q.session, ParentUUID: parent, Timestamp: stamp, Message: bytes.Clone(fields["message"]), Raw: bytes.Clone(raw)})
	}
	if e := scan.Err(); e != nil {
		if errors.Is(e, bufio.ErrTooLong) {
			return nil, queryError("resource-limit", path, "line bytes", e)
		}
		return nil, queryError("corrupt", path, "incomplete or oversized JSONL", e)
	}
	// Check every branch, including records outside the selected leaf's chain.
	done := map[string]bool{}
	for _, r := range records {
		visiting := map[string]bool{}
		id := r.UUID
		for id != "" && !done[id] {
			if visiting[id] {
				return nil, queryError("corrupt", path, "child parent cycle", nil)
			}
			visiting[id] = true
			pos, ok := index[id]
			if !ok {
				return nil, queryError("corrupt", path, "missing child parent", nil)
			}
			next := records[pos].ParentUUID
			if next == nil {
				break
			}
			id = *next
		}
		for id := range visiting {
			done[id] = true
		}
	}
	if leaf < 0 {
		return []SessionMessage{}, nil
	}
	selected := []SessionMessage{}
	for i := leaf; ; {
		r := records[i]
		if r.Type == "user" || r.Type == "assistant" {
			selected = append(selected, r)
		}
		if r.ParentUUID == nil {
			break
		}
		i = index[*r.ParentUUID]
	}
	for i, j := 0, len(selected)-1; i < j; i, j = i+1, j-1 {
		selected[i], selected[j] = selected[j], selected[i]
	}
	return selected, nil
}

func (q *childQuery) parseSidecar(data []byte, path, metaPath, agent string, info *SubagentInfo) error {
	lines := bytes.Count(data, []byte("\n"))
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lines++
	}
	if lines > q.c.Limits.MaxLines-q.c.Lines {
		return queryError("resource-limit", path, "sidecar lines", nil)
	}
	q.c.Lines += lines
	if validateDiscoveryJSON(data) != nil {
		return &SessionQueryError{Kind: "corrupt", Path: metaPath, Reason: "invalid sidecar JSON", Raw: bytes.Clone(data)}
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return &SessionQueryError{Kind: "corrupt", Path: metaPath, Reason: "sidecar object required", Raw: bytes.Clone(data)}
	}
	if e := q.identity(fields, agent, metaPath); e != nil {
		return e
	}
	for key, dest := range map[string]**string{"toolUseId": &info.ParentToolUseID, "parentAgentId": &info.ParentAgentID} {
		value, e := discoveryString(fields, key)
		if e != nil || (value != nil && (*value == "" || (key == "parentAgentId" && (!safeChildID(*value) || *value == agent)))) {
			return &SessionQueryError{Kind: "corrupt", Path: metaPath, Reason: "invalid sidecar parent", Raw: bytes.Clone(data), Cause: e}
		}
		*dest = value
	}
	info.Sidecar = bytes.Clone(data)
	return nil
}
