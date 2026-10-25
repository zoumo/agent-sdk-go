// Chain and parallel-recovery algorithm adapted from the official Python SDK
// 0e89b298e771259378a6cd3734e00d323dff5703. MIT notice: NOTICE.
package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
)

type queryRecord struct {
	historyRecord
	parent, timestamp   *string
	side, meta          bool
	team, agent, source string
	agentPresent        bool
	response            string
	calls, results      []string
	toolResult          bool
}

func (r *queryRecord) visible() bool {
	return (r.Type == "user" || r.Type == "assistant") && !r.side && !r.meta && r.team == ""
}

func parseQueryTranscript(ctx context.Context, data []byte, session string, o SessionMessagesOptions, c *SessionMessagesCoverage) ([]*queryRecord, error) {
	records := []*queryRecord{}
	scan := transcriptScanner(bytes.NewReader(data), o.MaxLineBytes)
	for scan.Scan() {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if c.Lines >= o.MaxLines {
			return nil, queryError("resource-limit", c.Path, "source lines", nil)
		}
		c.Lines++
		raw := scan.Bytes()
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		bad := func(kind, reason string) error {
			return &SessionQueryError{Kind: kind, Path: c.Path, Reason: reason, Raw: bytes.Clone(raw)}
		}
		if e := validateDiscoveryJSON(raw); e != nil {
			return nil, bad("corrupt", e.Error())
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return nil, bad("corrupt", "entry must be an object")
		}
		// Exact-case field lookup keeps native JS key semantics.
		str := func(key string) (*string, error) { return discoveryString(fields, key) }
		kind, e := str("type")
		if e != nil || kind == nil || *kind == "" {
			return nil, bad("corrupt", "missing/invalid type")
		}
		for _, key := range []string{"sessionId", "session_id"} {
			v, e := str(key)
			if e != nil || (v != nil && *v != session) {
				return nil, bad("corrupt", "session identity mismatch")
			}
		}
		switch *kind {
		case "user", "assistant", "system", "progress", "attachment":
		default:
			if transcriptMetadata(*kind) {
				continue
			}
			return nil, bad("unsupported", "entry type: "+*kind)
		}
		if len(records) >= o.MaxRecords {
			return nil, bad("resource-limit", "indexed records")
		}
		r := &queryRecord{}
		r.Type = *kind
		id, e := str("uuid")
		if e != nil || id == nil || *id == "" {
			return nil, bad("corrupt", "missing/invalid UUID")
		}
		r.UUID = *id
		parent, e := str("parentUuid")
		if e != nil {
			return nil, bad("corrupt", "invalid parentUuid")
		}
		if _, ok := fields["parentUuid"]; !ok {
			return nil, bad("corrupt", "missing parentUuid")
		}
		r.parent = parent
		if parent != nil {
			r.ParentUUID = *parent
		}
		sid, e := str("sessionId")
		if e != nil || sid == nil || *sid != session {
			return nil, bad("corrupt", "sessionId mismatch")
		}
		r.SessionID = *sid
		r.timestamp, e = str("timestamp")
		if e != nil {
			return nil, bad("corrupt", "invalid timestamp")
		}
		if r.timestamp != nil {
			r.Timestamp = *r.timestamp
		}
		for key, dest := range map[string]*string{"teamName": &r.team, "agentId": &r.agent, "sourceToolAssistantUUID": &r.source} {
			v, e := str(key)
			if e != nil {
				return nil, bad("corrupt", "invalid "+key)
			}
			if v != nil {
				*dest = *v
				if key == "agentId" {
					r.agentPresent = true
				}
			}
		}
		for key, dest := range map[string]*bool{"isSidechain": &r.side, "isMeta": &r.meta} {
			if v, ok := fields[key]; ok {
				if bytes.Equal(bytes.TrimSpace(v), []byte("null")) || json.Unmarshal(v, dest) != nil {
					return nil, bad("corrupt", "invalid "+key)
				}
			}
		}
		r.Raw = bytes.Clone(raw)
		r.Message = bytes.Clone(fields["message"])
		if r.Type == "user" || r.Type == "assistant" {
			var msg map[string]json.RawMessage
			if json.Unmarshal(r.Message, &msg) != nil || msg == nil {
				return nil, bad("corrupt", "invalid message")
			}
			content := bytes.TrimSpace(msg["content"])
			if len(content) == 0 || (content[0] != '"' && content[0] != '[') {
				return nil, bad("corrupt", "invalid message content")
			}
			if r.Type == "assistant" {
				_ = json.Unmarshal(msg["id"], &r.response)
			}
			if content[0] == '[' {
				var blocks []map[string]json.RawMessage
				// Unknown non-object blocks are preserved; only known object identities
				// participate in reconstruction, as in the fixed native implementation.
				var values []json.RawMessage
				_ = json.Unmarshal(content, &values)
				for _, v := range values {
					var b map[string]json.RawMessage
					if json.Unmarshal(v, &b) == nil && b != nil {
						blocks = append(blocks, b)
					}
				}
				for _, b := range blocks {
					var typ string
					_ = json.Unmarshal(b["type"], &typ)
					if typ == "tool_use" {
						if id, err := discoveryString(b, "id"); err == nil && id != nil {
							r.calls = append(r.calls, *id)
						}
					}
					if typ == "tool_result" {
						r.toolResult = true
						if id, err := discoveryString(b, "tool_use_id"); err == nil && id != nil {
							r.results = append(r.results, *id)
						}
					}
				}
			}
		}
		records = append(records, r)
		c.Records = len(records)
	}
	if e := scan.Err(); e != nil {
		kind := "corrupt"
		if errors.Is(e, bufio.ErrTooLong) {
			kind = "resource-limit"
		}
		return nil, queryError(kind, c.Path, "incomplete or oversized JSONL record", e)
	}
	c.Records = len(records)
	return records, nil
}

// queryConversation mirrors the fixed native main-leaf/parentUuid algorithm,
// with strict graph validation instead of silently truncating damaged chains.
func queryConversation(ctx context.Context, records []*queryRecord, path string) ([]*queryRecord, error) {
	byID := make(map[string]*queryRecord, len(records))
	positions := make(map[string]int, len(records))
	parents := map[string]bool{}
	bad := func(reason string, r *queryRecord) error {
		return &SessionQueryError{Kind: "corrupt", Path: path, Reason: reason, Raw: bytes.Clone(r.Raw)}
	}
	for i, r := range records {
		if _, ok := byID[r.UUID]; ok {
			return nil, bad("duplicate UUID", r)
		}
		byID[r.UUID] = r
		positions[r.UUID] = i
		if r.ParentUUID != "" {
			parents[r.ParentUUID] = true
		}
	}
	checked := map[string]bool{}
	for _, r := range records {
		visiting := map[string]bool{}
		walk := []string{}
		for cur := r; cur != nil && !checked[cur.UUID]; {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			if visiting[cur.UUID] {
				return nil, bad("parent cycle", cur)
			}
			visiting[cur.UUID] = true
			walk = append(walk, cur.UUID)
			if cur.ParentUUID == "" {
				break
			}
			parent := byID[cur.ParentUUID]
			if parent == nil {
				return nil, bad("missing parent UUID", cur)
			}
			cur = parent
		}
		for _, id := range walk {
			checked[id] = true
		}
	}
	var leaf, fallback *queryRecord
	nearest := map[string]*queryRecord{}
	for _, r := range records {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if parents[r.UUID] {
			continue
		}
		cur := r
		walk := []string{}
		for cur != nil && cur.Type != "user" && cur.Type != "assistant" {
			if cached, ok := nearest[cur.UUID]; ok {
				cur = cached
				break
			}
			walk = append(walk, cur.UUID)
			cur = byID[cur.ParentUUID]
		}
		for _, id := range walk {
			nearest[id] = cur
		}
		if cur != nil && (fallback == nil || positions[cur.UUID] > positions[fallback.UUID]) {
			fallback = cur
		}
		if cur != nil && cur.visible() && (leaf == nil || positions[cur.UUID] > positions[leaf.UUID]) {
			leaf = cur
		}
	}
	if leaf == nil {
		leaf = fallback
	}
	chain := []*queryRecord{}
	seen := map[string]bool{}
	for cur := leaf; cur != nil; cur = byID[cur.ParentUUID] {
		seen[cur.UUID] = true
		chain = append(chain, cur)
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return recoverQueryTools(ctx, records, chain, seen, byID, positions)
}

// Parallel recovery follows Python sessions.py at 0e89b298: group responses,
// index direct/source/call-owner results, and recover strays only for unanswered
// unambiguous same-writer calls. Only recovered content uses native stable
// timestamp ordering; the selected parent chain is never sorted or deduplicated.
func recoverQueryTools(ctx context.Context, records, chain []*queryRecord, seen map[string]bool, byID map[string]*queryRecord, positions map[string]int) ([]*queryRecord, error) {
	anchors := map[string]*queryRecord{}
	groups := map[string][]*queryRecord{}
	owners := map[string]*queryRecord{}
	results := []*queryRecord{}
	for _, r := range chain {
		if r.Type == "assistant" && r.response != "" {
			anchors[r.response] = r
		}
	}
	if len(anchors) == 0 {
		return chain, nil
	}
	for _, r := range records {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if r.Type == "assistant" && r.response != "" {
			groups[r.response] = append(groups[r.response], r)
			for _, id := range r.calls {
				owner, exists := owners[id]
				if !exists || (owner != nil && owner.response == r.response) {
					owners[id] = r
				} else {
					owners[id] = nil
				}
			}
		} else if r.Type == "user" && r.ParentUUID != "" && r.toolResult {
			results = append(results, r)
		}
	}
	sameWriter := func(a, b *queryRecord) bool {
		return a.side == b.side && a.agent == b.agent && a.agentPresent == b.agentPresent
	}
	indexed := map[string][]*queryRecord{}
	for _, r := range results {
		indexed[r.ParentUUID] = append(indexed[r.ParentUUID], r)
		if source := byID[r.source]; source != nil && sameWriter(r, source) {
			indexed[r.source] = append(indexed[r.source], r)
		}
		for _, id := range r.results {
			if owner := owners[id]; owner != nil && sameWriter(r, owner) {
				indexed[owner.UUID] = append(indexed[owner.UUID], r)
			}
		}
	}
	inserts := map[string][]*queryRecord{}
	processed := map[string]bool{}
	answeredOnChain := map[string]bool{}
	for _, r := range chain {
		for _, id := range r.results {
			answeredOnChain[id] = true
		}
	}
	for _, assistant := range chain {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		id := assistant.response
		if assistant.Type != "assistant" || id == "" || processed[id] {
			continue
		}
		processed[id] = true
		group := groups[id]
		members := map[string]bool{}
		siblings := []*queryRecord{}
		for _, r := range group {
			members[r.UUID] = true
			if !seen[r.UUID] {
				siblings = append(siblings, r)
			}
		}
		orphaned, strays := []*queryRecord{}, []*queryRecord{}
		collected := map[string]bool{}
		for _, member := range group {
			for _, r := range indexed[member.UUID] {
				if seen[r.UUID] || collected[r.UUID] {
					continue
				}
				collected[r.UUID] = true
				if members[r.ParentUUID] {
					orphaned = append(orphaned, r)
				} else {
					strays = append(strays, r)
				}
			}
		}
		if len(strays) > 0 {
			answered := map[string]bool{}
			for id := range answeredOnChain {
				answered[id] = true
			}
			for _, r := range orphaned {
				for _, id := range r.results {
					answered[id] = true
				}
			}
			sort.SliceStable(strays, func(i, j int) bool { return positions[strays[i].UUID] < positions[strays[j].UUID] })
			for _, r := range strays {
				open := false
				for _, id := range r.results {
					owner := owners[id]
					if !answered[id] && owner != nil && members[owner.UUID] {
						open = true
						break
					}
				}
				if open {
					for _, id := range r.results {
						answered[id] = true
					}
					orphaned = append(orphaned, r)
				}
			}
		}
		sortTime := func(list []*queryRecord) {
			sort.SliceStable(list, func(i, j int) bool { return list[i].Timestamp < list[j].Timestamp })
		}
		sortTime(siblings)
		sortTime(orphaned)
		recovered := append(siblings, orphaned...)
		for _, r := range recovered {
			seen[r.UUID] = true
		}
		inserts[anchors[id].UUID] = recovered
	}
	out := []*queryRecord{}
	for _, r := range chain {
		out = append(out, r)
		out = append(out, inserts[r.UUID]...)
	}
	return out, nil
}
