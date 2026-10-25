package codex_test

import (
	"context"

	"github.com/stretchr/testify/require"
	codex "github.com/zoumo/agent-sdk-go/codex"
)

func (obs *observationSuite) TestChildDetailInvalidBeforeStartupOrRPC() {
	t := obs.T()
	f := obs.fixture(t, "request-normal", codex.Limits{})
	// The initialize response can precede the peer reading initialized. Establish
	// a complete baseline before asserting that invalid Open calls send no RPC.
	emitRequests(t, f.c)
	before := len(trace(t, f.trace))
	for _, policy := range []codex.ChildDetailPolicy{"Suppress", "unknown"} {
		for _, options := range []codex.SessionOptions{
			{Client: f.c, ChildDetailPolicy: policy},
			{Stdio: &codex.StdioOptions{Executable: "/does-not-exist"}, ChildDetailPolicy: policy},
		} {
			s, err := codex.Open(context.Background(), options)
			require.Nil(t, s)
			require.ErrorIs(t, err, codex.ErrInvalidInput)
		}
	}
	require.Len(t, trace(t, f.trace), before)
}
