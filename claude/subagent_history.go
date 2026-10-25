package claude

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// SubagentHistoryLimits bounds cumulative initial reads and inventory traversal.
// Zero selects 64 MiB, 100000 lines/records, 8 MiB/line, 10000 files/directories,
// depth 64 and 64 KiB/sidecar. Negative limits are invalid.
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

// SubagentInfo contains stored facts only. Missing sidecar ancestry stays nil;
// directory nesting never implies an agent parent. Running is always unknown.
type SubagentInfo struct {
	SessionID, AgentID, Path       string
	ParentAgentID, ParentToolUseID *string
	Sidecar                        json.RawMessage
	Running                        string
}
type SubagentMessage struct {
	SessionMessage
	AgentID                        string
	ParentAgentID, ParentToolUseID *string
}

// SubagentHistoryCoverage describes this bounded read set, never a snapshot or
// running-state guarantee. Bytes counts initial reads, excluding verification.
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

func validChildAgent(id string) bool {
	if len(id) == 0 || len(id) > 255 || !utf8.ValidString(id) || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) || strings.ContainsRune("/\\:", r) {
			return false
		}
	}
	return true
}
func prepareChildQuery(ctx context.Context, parent, agent string, scope *SessionQueryScope, c *SubagentHistoryCoverage) error {
	if ctx == nil {
		return queryError("invalid-input", "", "nil context", ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !historyUUID.MatchString(parent) || (agent != "" && !validChildAgent(agent)) || !filepath.IsAbs(scope.ConfigDir) || (scope.ProjectDir != "" && !filepath.IsAbs(scope.ProjectDir)) {
		return queryError("invalid-input", "", "native parent UUID, agent component and absolute scope required", ErrInvalidInput)
	}
	l := &c.Limits
	if c.Limit < 0 || c.Offset < 0 || c.MaxResults < 0 || scope.MaxEntries < 0 || l.MaxBytes < 0 || l.MaxLines < 0 || l.MaxRecords < 0 || l.MaxLineBytes < 0 || l.MaxFiles < 0 || l.MaxDirectories < 0 || l.MaxDepth < 0 || l.MaxSidecarBytes < 0 {
		return queryError("invalid-input", "", "negative limit/offset", ErrInvalidInput)
	}
	if scope.MaxEntries == 0 {
		scope.MaxEntries = 10000
	}
	c.MaxEntries = scope.MaxEntries
	if c.MaxResults == 0 {
		c.MaxResults = 10000
	}
	if l.MaxBytes == 0 {
		l.MaxBytes = 64 << 20
	}
	if l.MaxLines == 0 {
		l.MaxLines = 100000
	}
	if l.MaxRecords == 0 {
		l.MaxRecords = 100000
	}
	if l.MaxLineBytes == 0 {
		l.MaxLineBytes = 8 << 20
	}
	if l.MaxFiles == 0 {
		l.MaxFiles = 10000
	}
	if l.MaxDirectories == 0 {
		l.MaxDirectories = 10000
	}
	if l.MaxDepth == 0 {
		l.MaxDepth = 64
	}
	if l.MaxSidecarBytes == 0 {
		l.MaxSidecarBytes = 64 << 10
	}
	if c.Limit > c.MaxResults {
		return queryError("resource-limit", "", "requested limit exceeds maximum results", nil)
	}
	return nil
}
func childCoverage(parent, agent string, scope SessionQueryScope, limits SubagentHistoryLimits, limit, offset, maxResults int) SubagentHistoryCoverage {
	return SubagentHistoryCoverage{Status: "unknown", ConfigDir: scope.ConfigDir, ProjectDir: scope.ProjectDir, SessionID: parent, AgentID: agent, Limits: limits, Limit: limit, Offset: offset, MaxResults: maxResults, Directories: []string{}}
}

// ListSubagents independently inventories stored child transcripts. It never
// constructs a Session or invokes a CLI. Limit defaults to 100, MaxAgents to
// 10000. All candidates are validated before the path-sorted visible slice.
// Errors clear Records and preserve actual (possibly partial) coverage.
func ListSubagents(ctx context.Context, parent string, o ListSubagentsOptions) (page SubagentsPage, err error) {
	page.Records = []SubagentInfo{}
	page.Coverage = childCoverage(parent, "", o.Scope, o.Limits, o.Limit, o.Offset, o.MaxAgents)
	page.Coverage.SidecarStatus = "per-record"
	if page.Coverage.Limit == 0 {
		page.Coverage.Limit = 100
	}
	defer func() {
		if err != nil {
			page.Records = []SubagentInfo{}
		}
	}()
	if err = prepareChildQuery(ctx, parent, "", &o.Scope, &page.Coverage); err != nil {
		return
	}
	r, e := newChildReader(ctx, o.Scope, &page.Coverage)
	if e != nil {
		return page, e
	}
	defer r.close()
	r.maxAgents = page.Coverage.MaxResults
	candidates, e := r.inventory(parent)
	if e != nil {
		return page, e
	}
	if len(candidates) > page.Coverage.MaxResults {
		return page, r.fail("resource-limit", page.Coverage.Root, "agent inventory")
	}
	for i, candidate := range candidates {
		info, _, e := r.child(candidate)
		if e != nil {
			return page, e
		}
		if i >= page.Coverage.Offset && len(page.Records) < page.Coverage.Limit {
			page.Records = append(page.Records, info)
		}
	}
	if err = r.verify(); err != nil {
		return
	}
	page.Coverage.Status = "complete"
	return
}

// GetSubagentMessages reads one saved child's parentUuid chain. Limit defaults
// to 1000, MaxMessages to 10000. Selection follows the pinned official child
// algorithm, independently of main-session compaction/parallel recovery.
// It performs no live operation and never returns partial content on error.
func GetSubagentMessages(ctx context.Context, parent, agent string, o SubagentMessagesOptions) (page SubagentMessagesPage, err error) {
	page.Messages = []SubagentMessage{}
	page.Coverage = childCoverage(parent, agent, o.Scope, o.Limits, o.Limit, o.Offset, o.MaxMessages)
	if page.Coverage.Limit == 0 {
		page.Coverage.Limit = 1000
	}
	defer func() {
		if err != nil {
			page.Messages = []SubagentMessage{}
			page.Info = SubagentInfo{}
		}
	}()
	if agent == "" {
		return page, queryError("invalid-input", "", "empty agent component", ErrInvalidInput)
	}
	if err = prepareChildQuery(ctx, parent, agent, &o.Scope, &page.Coverage); err != nil {
		return
	}
	r, e := newChildReader(ctx, o.Scope, &page.Coverage)
	if e != nil {
		return page, e
	}
	defer r.close()
	candidates, e := r.inventory(parent)
	if e != nil {
		return page, e
	}
	var selected *childCandidate
	for i := range candidates {
		if candidates[i].agent == agent {
			selected = &candidates[i]
		}
	}
	if selected == nil {
		return page, r.fail("missing", page.Coverage.Root, "child not found")
	}
	page.Coverage.Path = r.absolute(selected.path)
	info, records, e := r.child(*selected)
	if e != nil {
		return page, e
	}
	page.Info = info
	if info.Sidecar == nil {
		page.Coverage.SidecarStatus = "absent"
	} else {
		page.Coverage.SidecarStatus = "present"
	}
	chain, e := r.chain(records, selected.path)
	if e != nil {
		return page, e
	}
	visible := 0
	for _, message := range chain {
		if message.Type != "user" && message.Type != "assistant" {
			continue
		}
		if visible >= page.Coverage.Offset && len(page.Messages) < page.Coverage.Limit {
			page.Messages = append(page.Messages, SubagentMessage{SessionMessage: message, AgentID: agent, ParentAgentID: copyChildString(info.ParentAgentID), ParentToolUseID: copyChildString(info.ParentToolUseID)})
		}
		visible++
	}
	if err = r.verify(); err != nil {
		return
	}
	page.Coverage.Status = "complete"
	return
}
func copyChildString(v *string) *string {
	if v == nil {
		return nil
	}
	s := *v
	return &s
}
