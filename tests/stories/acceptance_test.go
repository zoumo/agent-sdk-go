package stories_test

import (
	"context"
)

// storyObservation is a detached test fact extracted by a native adapter. Raw keeps
// the observed wire payload; it is never used to construct response authority.
type storyObservation struct{ Kind, ID, Parent, Raw string }
type storyTrace struct {
	Events                                                                                       []storyObservation
	SendError, AnswerError, RetryError, CanceledError, OverflowError, InterruptError, CloseError error
	AnswerID, AnswerText, AnswerDecision                                                         string
	AnswerCount, InputCount                                                                      int
}

var storyIDs = []string{"US-001", "US-002", "US-003", "US-004", "US-005", "US-006", "US-007", "US-008", "US-009", "US-010", "US-011", "US-012"}

// checkStory applies common user-visible acceptance rules, without requiring native
// events to share a schema, an ordering beyond causal boundaries, or dedup rules.
func (s *CommonStoriesSuite) checkStory(story string, got storyTrace) {
	s.T().Helper()
	check := s.Assert()
	find := func(kind, id string) int {
		for i, e := range got.Events {
			if e.Kind == kind && (id == "" || e.ID == id) {
				return i
			}
		}
		return -1
	}
	switch story {
	case "US-001":
		check.NoError(got.SendError, "input submission failed")
		check.GreaterOrEqual(find("barrier", ""), 0, "input barrier missing")
		check.Greater(find("complete", "root"), find("barrier", ""), "send acknowledgement confused with task completion")
	case "US-002":
		check.GreaterOrEqual(find("tool", ""), 0, "tool completion missing")
		check.Greater(find("complete", "root"), find("tool", ""), "tool completion must remain separate from task completion")
	case "US-003":
		found := false
		for _, e := range got.Events {
			if e.Kind == "associated" && e.ID == "child" && e.Parent == "root" {
				found = true
			}
		}
		check.True(found, "confirmed child parent lost")
		check.GreaterOrEqual(find("native", "child"), 0, "child native source lost")
	case "US-004":
		for _, pair := range [][2]string{{"child", "root"}, {"sibling", "root"}, {"grand", "child"}} {
			found := false
			for _, e := range got.Events {
				if e.Kind == "associated" && e.ID == pair[0] && e.Parent == pair[1] {
					found = true
				}
			}
			check.True(found, "nested/parallel relation lost: "+pair[0])
		}
		check.GreaterOrEqual(find("gap", ""), 0, "unknown source must produce visible uncertainty")
		check.Less(find("associated", "unknown"), 0, "unknown parent guessed")
	case "US-005":
		check.GreaterOrEqual(find("complete", "root"), 0, "root completion missing")
		check.Greater(find("native", "child"), find("complete", "root"), "child observation ended at root completion")
	case "US-006", "US-006-allow", "US-007":
		check.NoError(got.AnswerError, "original handle first answer")
		check.Error(got.RetryError, "original handle must be single use")
		check.Equal(1, got.AnswerCount, "answer uniqueness lost")
		check.Equal("original", got.AnswerID, "answer correlation lost")
		if story == "US-006" {
			check.Equal("deny", got.AnswerDecision, "native permission refusal changed")
		}
		if story == "US-006-allow" {
			check.Equal("allow", got.AnswerDecision, "native permission approval changed")
		}
		if story == "US-007" {
			check.Equal("free text / 自由文本", got.AnswerText, "answer not mapped to original question")
		}
	case "US-008":
		check.ErrorIs(got.CanceledError, context.Canceled, "canceled answer must retain native context cancellation")
		check.NoError(got.AnswerError, "pre-canceled answer must leave original handle available under native contract")
		check.Equal(1, got.AnswerCount, "pre-canceled answer must not consume handle")
	case "US-008-close":
		check.NoError(got.CloseError, "close failed")
		check.Error(got.AnswerError, "closing while waiting for an answer must invalidate the original handle")
		check.Error(got.RetryError, "closed original handle must stay invalid")
	case "US-009":
		check.Error(got.OverflowError, "slow consumption silently lost events")
	case "US-010":
		unknown := 0
		for _, e := range got.Events {
			if e.Kind == "unknown" {
				unknown++
				check.Contains(e.Raw, `"future":{"big":9007199254740993,"nullable":null}`, "unknown payload changed")
			}
		}
		check.Equal(2, unknown, "unknown no-ID arrivals must survive independently")
	case "US-011":
		check.GreaterOrEqual(find("gap", ""), 0, "history recovery gap lost")
		check.GreaterOrEqual(find("associated", "child"), 0, "history association missing")
		check.Less(find("created", "child"), 0, "history association invented creation")
		check.Zero(got.InputCount, "recovery replayed input")
		check.Zero(got.AnswerCount, "recovery replayed answer")
	case "US-012":
		check.NoError(got.InterruptError, "interrupt failed")
		check.NoError(got.CloseError, "close failed")
		check.GreaterOrEqual(find("barrier", ""), 0, "interrupt barrier missing")
		check.Greater(find("complete", "root"), find("barrier", ""), "interrupt acknowledgement fabricated completion")
	}
}
