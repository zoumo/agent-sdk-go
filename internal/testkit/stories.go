// Package testkit contains acceptance assertions used only by repository tests.
// It is internal and deliberately contains no SDK client or answer abstraction.
package testkit

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Observation is a detached test fact extracted by a native adapter. Raw keeps
// the observed wire payload; it is never used to construct response authority.
type Observation struct{ Kind, ID, Parent, Raw string }
type Trace struct {
	Events                                                                                       []Observation
	SendError, AnswerError, RetryError, CanceledError, OverflowError, InterruptError, CloseError error
	AnswerID, AnswerText, AnswerDecision                                                         string
	AnswerCount, InputCount                                                                      int
}

var Stories = []string{"US-001", "US-002", "US-003", "US-004", "US-005", "US-006", "US-007", "US-008", "US-009", "US-010", "US-011", "US-012"}

// Check applies common user-visible acceptance rules, without requiring native
// events to share a schema, an ordering beyond causal boundaries, or dedup rules.
func Check(t *testing.T, story string, got Trace) {
	t.Helper()
	find := func(kind, id string) int {
		for i, e := range got.Events {
			if e.Kind == kind && (id == "" || e.ID == id) {
				return i
			}
		}
		return -1
	}
	require := func(ok bool, what string) {
		t.Helper()
		if !ok {
			t.Fatalf("%s: %s; trace=%+v", story, what, got)
		}
	}
	switch story {
	case "US-001":
		require(got.SendError == nil, "input submission failed")
		require(find("barrier", "") >= 0 && find("complete", "root") > find("barrier", ""), "send acknowledgement confused with task completion")
	case "US-002":
		require(find("tool", "") >= 0 && find("complete", "root") > find("tool", ""), "tool completion must remain separate from task completion")
	case "US-003":
		found := false
		for _, e := range got.Events {
			if e.Kind == "associated" && e.ID == "child" && e.Parent == "root" {
				found = true
			}
		}
		require(found, "confirmed child parent lost")
		require(find("native", "child") >= 0, "child native source lost")
	case "US-004":
		for _, pair := range [][2]string{{"child", "root"}, {"sibling", "root"}, {"grand", "child"}} {
			found := false
			for _, e := range got.Events {
				if e.Kind == "associated" && e.ID == pair[0] && e.Parent == pair[1] {
					found = true
				}
			}
			require(found, "nested/parallel relation lost: "+pair[0])
		}
		require(find("gap", "") >= 0, "unknown source must produce visible uncertainty")
		require(find("associated", "unknown") < 0, "unknown parent guessed")
	case "US-005":
		require(find("complete", "root") >= 0 && find("native", "child") > find("complete", "root"), "child observation ended at root completion")
	case "US-006", "US-006-allow", "US-007":
		require(got.AnswerError == nil && got.RetryError != nil, "original handle must be single use")
		require(got.AnswerCount == 1 && got.AnswerID == "original", "answer correlation/uniqueness lost")
		if story == "US-006" {
			require(got.AnswerDecision == "deny", "native permission refusal changed")
		}
		if story == "US-006-allow" {
			require(got.AnswerDecision == "allow", "native permission approval changed")
		}
		if story == "US-007" {
			require(got.AnswerText == "free text / 自由文本", "answer not mapped to original question")
		}
	case "US-008":
		require(errors.Is(got.CanceledError, context.Canceled), "canceled answer must retain native context cancellation")
		require(got.AnswerError == nil && got.AnswerCount == 1, "pre-canceled answer must leave original handle available under native contract")
	case "US-008-close":
		require(got.CloseError == nil && got.AnswerError != nil && got.RetryError != nil, "closing while waiting for an answer must invalidate the original handle")
	case "US-009":
		require(got.OverflowError != nil, "slow consumption silently lost events")
	case "US-010":
		unknown := 0
		for _, e := range got.Events {
			if e.Kind == "unknown" {
				unknown++
				require(strings.Contains(e.Raw, `"future":{"big":9007199254740993,"nullable":null}`), "unknown payload changed")
			}
		}
		require(unknown == 2, "unknown no-ID arrivals must survive independently")
	case "US-011":
		require(find("gap", "") >= 0, "history recovery gap lost")
		require(find("associated", "child") >= 0 && find("created", "child") < 0, "history association invented creation")
		require(got.InputCount == 0 && got.AnswerCount == 0, "recovery replayed input/answer")
	case "US-012":
		require(got.InterruptError == nil && got.CloseError == nil, "interrupt/close failed")
		require(find("barrier", "") >= 0 && find("complete", "root") > find("barrier", ""), "interrupt acknowledgement fabricated completion")
	}
}
