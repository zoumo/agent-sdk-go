package codextest

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/codex"
	"github.com/zoumo/agent-sdk-go/internal/testsupport"
)

func Requests(t *testing.T, s *codex.Session, n int) []*codex.IncomingRequest {
	t.Helper()
	var all []*codex.IncomingRequest
	ctx := testsupport.Deadline(t)
	for len(all) < n {
		r, e := s.Receive(ctx)
		require.NoError(t, e)
		if r.Request != nil {
			_, err := r.Request.Data()
			require.NoError(t, err)
			all = append(all, r.Request)
		}
	}
	return all
}
