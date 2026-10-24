package codex

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

var ErrUnsupportedDiscovery = errors.New("codex: descendant queries require experimentalApi initialization")
var ErrDiscoveryCursor = errors.New("codex: descendant query cursor did not advance or repeated")

// SubagentQuery selects stored descendants of the Session's fixed main thread.
// It cannot redirect the ancestor or select a direct-parent-only query.
type SubagentQuery struct {
	Cursor         types.Optional[string]
	Limit          types.Optional[uint32]
	SourceKinds    types.Optional[[]types.ThreadSourceKind]
	Archived       types.Optional[bool]
	ModelProviders types.Optional[[]string]
	Originators    types.Optional[[]string]
	SortKey        types.Optional[types.ThreadSortKey]
	SortDirection  types.Optional[types.SortDirection]
	Cwd            types.Optional[types.ThreadListCwdFilter]
	UseStateDbOnly types.Optional[bool]
	SectionID      types.Optional[string]
	ProjectID      types.Optional[string]
	SearchTerm     types.Optional[string]
	// Zero selects defaults: 128 pages, 10000 records, 16 MiB of native result
	// JSON. A page exceeding record/byte capacity is not retained or truncated;
	// NextCursor remains its input cursor so the caller can query it separately.
	MaxPages   int
	MaxRecords int
	MaxBytes   int
}

// SubagentCoverage describes only the requested storage enumeration, never live
// observation, active execution, or a snapshot across concurrent native changes.
type SubagentCoverage struct {
	Requested types.ThreadListParams
	Archived  bool
	// PagesCompleted counts successfully decoded RPC responses, including an
	// over-budget page that could not be retained. len(SubagentPage.Pages) is
	// the retained page count; neither count proves enumeration completeness.
	PagesCompleted int
	// EndReached reports the native end cursor even when enumeration began at
	// a supplied cursor. EnumerationComplete additionally requires a full walk
	// from the beginning and refers only to Requested filters and native storage.
	EndReached          bool
	EnumerationComplete bool
	// SourceSelection is explicit, native-interactive (empty list), or
	// native-unrestricted (null with the pinned ancestor relation filter).
	SourceSelection  string
	IncompleteReason string
}

// SubagentPage retains each native response, including unknown fields and
// cursors. Records do not establish observation membership or execution state.
type SubagentPage struct {
	Pages []types.ThreadListResponse
	// NextCursor is the next page input, or the unretained/failed page input.
	// On cursor-cycle or missing-end-cursor it preserves the native problematic
	// cursor instead; inspect Coverage.IncompleteReason before continuing.
	NextCursor types.Optional[string]
	Coverage   SubagentCoverage
}

// ListSubagents reads native stored descendants without attaching or replaying.
// Defaults explicitly include all five pinned subAgent source kinds and only
// non-archived records. Archived records require a separate query.
func (s *Session) ListSubagents(ctx context.Context, query SubagentQuery) (SubagentPage, error) {
	p := types.ThreadListParams{
		AncestorThreadID: types.Some(s.root), Cursor: query.Cursor, Limit: query.Limit, SourceKinds: query.SourceKinds, Archived: query.Archived,
		ModelProviders: query.ModelProviders, Originators: query.Originators, SortKey: query.SortKey, SortDirection: query.SortDirection, Cwd: query.Cwd,
		UseStateDbOnly: query.UseStateDbOnly, SectionID: query.SectionID, ProjectID: query.ProjectID, SearchTerm: query.SearchTerm,
	}
	if !p.SourceKinds.IsSet() {
		p.SourceKinds = types.Some([]types.ThreadSourceKind{types.ThreadSourceKindSubAgent, types.ThreadSourceKindSubAgentReview, types.ThreadSourceKindSubAgentCompact, types.ThreadSourceKindSubAgentThreadSpawn, types.ThreadSourceKindSubAgentOther})
	}
	// Freeze the exact JSON request, including Some(nil slice) -> null, before
	// interpreting coverage. This also detaches all filter slices/pointers from
	// caller-owned query storage without collapsing [] into null.
	rawParams, err := json.Marshal(p)
	if err == nil {
		err = json.Unmarshal(rawParams, &p)
	}
	if err != nil {
		return SubagentPage{Coverage: SubagentCoverage{IncompleteReason: "invalid-input"}}, &CallError{Method: "thread/list", Dispatch: NotSent, Cause: err}
	}
	r := SubagentPage{Coverage: SubagentCoverage{Requested: p, Archived: p.Archived.ValueOr(false)}, NextCursor: p.Cursor}
	r.Coverage.SourceSelection = "explicit"
	if sources, ok := p.SourceKinds.Get(); ok && len(sources) == 0 {
		r.Coverage.SourceSelection = "native-interactive"
	} else if p.SourceKinds.IsNull() {
		r.Coverage.SourceSelection = "native-unrestricted"
	}
	fail := func(reason string, err error) (SubagentPage, error) {
		r.Coverage.IncompleteReason = reason
		return r, err
	}
	if !s.client.experimentalAPI {
		return fail("experimental-api-disabled", &CallError{Method: "thread/list", Dispatch: NotSent, Cause: ErrUnsupportedDiscovery})
	}
	if query.MaxPages < 0 || query.MaxRecords < 0 || query.MaxBytes < 0 {
		return fail("invalid-limits", &CallError{Method: "thread/list", Dispatch: NotSent, Cause: ErrInvalidInput})
	}
	maxPages, maxRecords, maxBytes := query.MaxPages, query.MaxRecords, query.MaxBytes
	if maxPages == 0 {
		maxPages = 128
	}
	if maxRecords == 0 {
		maxRecords = 10000
	}
	if maxBytes == 0 {
		maxBytes = 16 << 20
	}
	seen := make(map[string]struct{})
	if cursor, ok := p.Cursor.Get(); ok {
		seen[cursor] = struct{}{}
	}
	records, bytes := 0, 0
	for {
		if len(r.Pages) >= maxPages {
			return fail("page-limit", ErrLimit)
		}
		var response types.ThreadListResponse
		if err := s.client.callOwned(ctx, "thread/list", p, &response, false, nil, s); err != nil {
			return fail("native-query-failed", err)
		}
		r.Coverage.PagesCompleted++
		raw, err := json.Marshal(response)
		if err != nil {
			return fail("invalid-response", err)
		}
		if len(response.Data) > maxRecords-records || len(raw) > maxBytes-bytes {
			return fail("result-limit", ErrLimit)
		}
		records += len(response.Data)
		bytes += len(raw)
		r.Pages = append(r.Pages, response)
		r.NextCursor = response.NextCursor
		cursor, ok := response.NextCursor.Get()
		if !ok {
			if !response.NextCursor.IsSet() {
				return fail("missing-end-cursor", ErrProtocol)
			}
			r.Coverage.EndReached = true
			r.Coverage.EnumerationComplete = !query.Cursor.IsSet() || query.Cursor.IsNull()
			if !r.Coverage.EnumerationComplete {
				r.Coverage.IncompleteReason = "started-at-cursor"
			}
			return r, nil
		}
		if _, exists := seen[cursor]; exists || cursor == "" {
			return fail("cursor-cycle", ErrDiscoveryCursor)
		}
		seen[cursor] = struct{}{}
		p.Cursor = response.NextCursor
	}
}
