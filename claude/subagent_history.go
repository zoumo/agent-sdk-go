package claude

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SubagentHistoryLimits bounds the entire independent local query, not each page.
// Zero selects the defaults recorded in the returned Coverage.Limits.
type SubagentHistoryLimits struct {
	MaxBytes                           int64
	MaxLines, MaxRecords, MaxLineBytes int
	MaxFiles, MaxDirectories, MaxDepth int
	MaxSidecarBytes                    int64
}
type ListSubagentsOptions struct {
	Scope                    SessionQueryScope
	Limits                   SubagentHistoryLimits
	Limit, Offset, MaxAgents int
}
type SubagentMessagesOptions struct {
	Scope                      SessionQueryScope
	Limits                     SubagentHistoryLimits
	Limit, Offset, MaxMessages int
}
type SubagentInfo struct {
	SessionID, AgentID, Path       string
	ParentAgentID, ParentToolUseID *string
	Sidecar                        json.RawMessage
	Running                        string // Always unknown: saved records do not establish live state.
}
type SubagentMessage struct {
	SessionMessage
	AgentID                        string
	ParentAgentID, ParentToolUseID *string
}
type SubagentHistoryCoverage struct {
	Status                                                          string
	ConfigDir, ProjectDir, SessionID, AgentID, MainPath, Root, Path string
	Directories                                                     []string
	Entries, Files, Lines, Records                                  int
	Bytes                                                           int64
	Atomic                                                          bool
	Limits                                                          SubagentHistoryLimits
	MaxEntries, Limit, Offset, MaxResults                           int
	SidecarStatus                                                   string
}
type SubagentsPage struct {
	Records  []SubagentInfo
	Coverage SubagentHistoryCoverage
}
type SubagentMessagesPage struct {
	Info     SubagentInfo
	Messages []SubagentMessage
	Coverage SubagentHistoryCoverage
}

// ListSubagents discovers saved children without a Session, environment lookup,
// process, history replay or live membership/answer authority. Any error clears Records.
func ListSubagents(ctx context.Context, parentSessionID string, o ListSubagentsOptions) (page SubagentsPage, err error) {
	page.Records = []SubagentInfo{}
	page.Coverage = SubagentHistoryCoverage{Status: "unknown", ConfigDir: o.Scope.ConfigDir, ProjectDir: o.Scope.ProjectDir, SessionID: parentSessionID, SidecarStatus: "per-record"}
	defer func() {
		if err != nil {
			page.Records = []SubagentInfo{}
		}
	}()
	q, err := newChildQuery(ctx, parentSessionID, "", o.Scope, o.Limits, o.Limit, o.Offset, o.MaxAgents, 100, &page.Coverage)
	if err != nil {
		return page, err
	}
	defer q.close()
	candidates, err := q.inventory()
	if err != nil {
		return page, err
	}
	if len(candidates) > q.c.MaxResults {
		return page, queryError("resource-limit", q.c.Root, "MaxAgents", nil)
	}
	for _, candidate := range candidates {
		info, _, e := q.load(candidate, false)
		if e != nil {
			return page, e
		}
		page.Records = append(page.Records, info)
	}
	if err = q.verify(); err != nil {
		return page, err
	}
	start := min(q.c.Offset, len(page.Records))
	end := start + min(q.c.Limit, len(page.Records)-start)
	page.Records = page.Records[start:end]
	q.c.Status = "complete"
	return page, nil
}

// GetSubagentMessages selects the child's own final-leaf parentUuid chain. It
// never runs main-history compaction/parallel recovery or opens a live Session.
// Any error clears Info and Messages, retaining only actual Coverage.
func GetSubagentMessages(ctx context.Context, parentSessionID, agentID string, o SubagentMessagesOptions) (page SubagentMessagesPage, err error) {
	page.Messages = []SubagentMessage{}
	page.Coverage = SubagentHistoryCoverage{Status: "unknown", ConfigDir: o.Scope.ConfigDir, ProjectDir: o.Scope.ProjectDir, SessionID: parentSessionID, AgentID: agentID}
	defer func() {
		if err != nil {
			page.Info = SubagentInfo{}
			page.Messages = []SubagentMessage{}
		}
	}()
	if !safeChildID(agentID) {
		return page, queryError("invalid-input", "", "invalid native agentID", ErrInvalidInput)
	}
	q, err := newChildQuery(ctx, parentSessionID, agentID, o.Scope, o.Limits, o.Limit, o.Offset, o.MaxMessages, 1000, &page.Coverage)
	if err != nil {
		return page, err
	}
	defer q.close()
	candidates, err := q.inventory()
	if err != nil {
		return page, err
	}
	var chosen *childCandidate
	for i := range candidates {
		if candidates[i].agent == agentID {
			chosen = &candidates[i]
		}
	}
	if chosen == nil {
		return page, queryError("missing", q.c.Root, "saved child not found", os.ErrNotExist)
	}
	var records []SessionMessage
	page.Info, records, err = q.load(*chosen, true)
	if err != nil {
		return page, err
	}
	if len(records) > q.c.MaxResults {
		return page, queryError("resource-limit", page.Info.Path, "MaxMessages", nil)
	}
	start := min(q.c.Offset, len(records))
	end := start + min(q.c.Limit, len(records)-start)
	for _, r := range records[start:end] {
		page.Messages = append(page.Messages, SubagentMessage{SessionMessage: r, AgentID: agentID, ParentAgentID: cloneChildString(page.Info.ParentAgentID), ParentToolUseID: cloneChildString(page.Info.ParentToolUseID)})
	}
	if err = q.verify(); err != nil {
		return page, err
	}
	q.c.Status = "complete"
	return page, nil
}

func safeChildID(id string) bool {
	if !utf8.ValidString(id) || len(id) == 0 || len(id) > 255 || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, "/\\:") && !strings.ContainsFunc(id, unicode.IsControl)
}
func cloneChildString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
func newChildQuery(ctx context.Context, session, agent string, scope SessionQueryScope, limits SubagentHistoryLimits, limit, offset, maxResults, defaultLimit int, c *SubagentHistoryCoverage) (*childQuery, error) {
	if ctx == nil {
		return nil, queryError("invalid-input", "", "nil context", ErrInvalidInput)
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if !historyUUID.MatchString(session) || !filepath.IsAbs(scope.ConfigDir) || (scope.ProjectDir != "" && !filepath.IsAbs(scope.ProjectDir)) {
		return nil, queryError("invalid-input", "", "native parent UUID and explicit absolute scope required", ErrInvalidInput)
	}
	if limit < 0 || offset < 0 || maxResults < 0 || scope.MaxEntries < 0 || limits.MaxBytes < 0 || limits.MaxSidecarBytes < 0 {
		return nil, queryError("invalid-input", "", "negative limit", ErrInvalidInput)
	}
	for _, n := range []int{limits.MaxLines, limits.MaxRecords, limits.MaxLineBytes, limits.MaxFiles, limits.MaxDirectories, limits.MaxDepth} {
		if n < 0 {
			return nil, queryError("invalid-input", "", "negative limit", ErrInvalidInput)
		}
	}
	if limit == 0 {
		limit = defaultLimit
	}
	if maxResults == 0 {
		maxResults = 10000
	}
	if scope.MaxEntries == 0 {
		scope.MaxEntries = 10000
	}
	if limits.MaxBytes == 0 {
		limits.MaxBytes = 64 << 20
	}
	if limits.MaxSidecarBytes == 0 {
		limits.MaxSidecarBytes = 64 << 10
	}
	for _, d := range []struct {
		p *int
		n int
	}{{&limits.MaxLines, 100000}, {&limits.MaxRecords, 100000}, {&limits.MaxLineBytes, 8 << 20}, {&limits.MaxFiles, 10000}, {&limits.MaxDirectories, 10000}, {&limits.MaxDepth, 64}} {
		if *d.p == 0 {
			*d.p = d.n
		}
	}
	c.Limits = limits
	c.MaxEntries = scope.MaxEntries
	c.Limit = limit
	c.Offset = offset
	c.MaxResults = maxResults
	if limit > maxResults {
		return nil, queryError("resource-limit", "", "requested result limit exceeds maximum", nil)
	}
	return &childQuery{ctx: ctx, session: session, scope: scope, c: c}, nil
}
