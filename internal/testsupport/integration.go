package testsupport

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Deadline(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func Wait(t *testing.T, ctx context.Context, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-ctx.Done():
		require.NoError(t, ctx.Err())
	}
}

// Adapters deliberately retain native clients, native types and original answer
// handles locally. Only detached facts reach the suite acceptance assertions.

func Marshal(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	require.NoError(t, e)
	return b
}

func ObservedWire(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &envelope))
	wire := envelope["wire"]
	var f map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wire, &f))
	if p, ok := f["params"]; ok {
		wire = p
	}
	var result map[string]any
	d := json.NewDecoder(strings.NewReader(string(wire)))
	d.UseNumber()
	require.NoError(t, d.Decode(&result))
	return result
}

func CountFrames(t *testing.T, path, method string) int {
	t.Helper()
	n := 0
	for _, b := range PeerFrames(t, path) {
		var f struct{ Method, Type string }
		require.NoError(t, json.Unmarshal(b, &f))
		if f.Method == method || f.Type == method {
			n++
		}
	}
	return n
}

// Trace reads only bytes the native peer actually received. Assertions never read
// private Session membership, queue length, or permission registry state.
func PeerFrames(t *testing.T, path string) []json.RawMessage {
	t.Helper()
	data, e := os.ReadFile(path)
	require.NoError(t, e)
	var frames []json.RawMessage
	for _, line := range splitLines(data) {
		frames = append(frames, json.RawMessage(line))
	}
	return frames
}

func splitLines(b []byte) [][]byte {
	var result [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				result = append(result, b[start:i])
			}
			start = i + 1
		}
	}
	return result
}

func CountAnswers(t *testing.T, path, provider string) int {
	t.Helper()
	n := 0
	for _, b := range PeerFrames(t, path) {
		var f struct {
			ID           json.RawMessage
			Method, Type string
		}
		require.NoError(t, json.Unmarshal(b, &f))
		if provider == "codex" && len(f.ID) > 0 && f.Method == "" || provider == "claude" && f.Type == "control_response" {
			n++
		}
	}
	return n
}

func CheckAnswerWires(t *testing.T, path, scenario, provider string) {
	t.Helper()
	var answers []string
	for _, b := range PeerFrames(t, path) {
		var f struct {
			ID           json.RawMessage
			Method, Type string
		}
		require.NoError(t, json.Unmarshal(b, &f))
		if provider == "codex" && len(f.ID) > 0 && f.Method == "" || provider == "claude" && f.Type == "control_response" {
			answers = append(answers, string(b))
		}
	}
	require.NotEmpty(t, answers)
	for i, b := range answers {
		var f struct {
			ID       string
			Response struct {
				RequestID string `json:"request_id"`
			}
		}
		require.NoError(t, json.Unmarshal([]byte(b), &f))
		want := "first"
		if i == 1 || scenario == "session-canceled-approval" {
			want = "second"
		}
		if provider == "codex" {
			assert.Equal(t, want, f.ID)
		} else {
			assert.Equal(t, want, f.Response.RequestID)
		}
	}

	if scenario == "session-approval" {
		if provider == "codex" {
			assert.Contains(t, answers[0], `"decline"`)
			assert.Contains(t, answers[1], `"execpolicy_amendment":["echo","fixture"]`)
		} else {
			assert.Contains(t, answers[0], `"message":"user refused"`)
			assert.Contains(t, answers[0], `"interrupt":true`)
			assert.Contains(t, answers[1], `"updatedPermissions"`)
			assert.Contains(t, answers[1], `echo modified`)
		}
	}
	if scenario == "session-question" {
		assert.Contains(t, answers[0], `A`)
		assert.Contains(t, answers[1], `free text / 自由文本`)
		if provider == "codex" {
			var first, second struct {
				Result struct {
					Answers map[string]struct{ Answers []string }
				}
			}
			require.NoError(t, json.Unmarshal([]byte(answers[0]), &first))
			require.NoError(t, json.Unmarshal([]byte(answers[1]), &second))
			assert.Equal(t, []string{"A"}, first.Result.Answers["q"].Answers)
			assert.Equal(t, []string{"free text / 自由文本"}, second.Result.Answers["q"].Answers)
		} else {
			var w struct {
				Response struct {
					Response struct {
						UpdatedInput struct{ Answers map[string]string } `json:"updatedInput"`
					}
				}
			}
			require.NoError(t, json.Unmarshal([]byte(answers[1]), &w))
			assert.Equal(t, "free text / 自由文本", w.Response.Response.UpdatedInput.Answers["Choose?"])
		}
		if provider == "claude" {
			assert.Contains(t, answers[1], `"questions"`)
			assert.Contains(t, answers[1], `"future":42`)
		}
	}
}

// Compare complete native input/answer order from peer-observed bytes. Detached
// trace facts cannot create an answer handle or hide an extra control/retry.
func CheckFaultControlOrder(t *testing.T, path, provider string, want []string) {
	t.Helper()
	var got []string
	for _, b := range PeerFrames(t, path) {
		var f struct {
			Method, Type string
			ID           json.RawMessage
			Request      struct{ Subtype string }
			Response     struct {
				RequestID string `json:"request_id"`
			}
		}
		require.NoError(t, json.Unmarshal(b, &f))
		if provider == "codex" {
			if f.Method != "" {
				got = append(got, f.Method)
			} else if len(f.ID) > 0 {
				var id string
				require.NoError(t, json.Unmarshal(f.ID, &id))
				got = append(got, "answer:"+id)
			}
		} else {
			switch f.Type {
			case "control_request":
				got = append(got, f.Request.Subtype)
			case "user":
				got = append(got, "user")
			case "control_response":
				got = append(got, "answer:"+f.Response.RequestID)
			}
		}
	}
	assert.Equal(t, want, got, "exact control order; no automatic answers, retries or extra input")
}
