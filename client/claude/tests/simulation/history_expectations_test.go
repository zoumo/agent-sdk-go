package claudesim

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecordedHistoryExpectationsKeepNativeIdentityAndContent(t *testing.T) {
	raw := []byte(`{"type":"user","uuid":"user","sessionId":"root","message":{"content":"request"}}
{"type":"progress","uuid":"progress","sessionId":"root"}
{"type":"assistant","uuid":"answer","sessionId":"root","isSidechain":true,"message":{"content":[{"type":"future","n":9007199254740993}]}}
`)
	main, err := recordedHistoryMessages(raw, "root", false)
	require.NoError(t, err)
	require.Len(t, main, 1)
	child, err := recordedHistoryMessages(raw, "root", true)
	require.NoError(t, err)
	require.Len(t, child, 2)
	require.Equal(t, "answer", child[1].UUID)
	require.Equal(t, "root", child[1].SessionID)
	require.JSONEq(t, `{"content":[{"type":"future","n":9007199254740993}]}`, string(child[1].Message))
	_, err = recordedHistoryMessages(raw, "other", true)
	require.ErrorContains(t, err, "invalid native identity")
	_, err = recordedHistoryMessages(append(raw, raw...), "root", true)
	require.ErrorContains(t, err, "invalid native identity")
}
