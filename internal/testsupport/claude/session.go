package claudetest

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/claude"
	"github.com/zoumo/agent-sdk-go/internal/testsupport"
)

func Requests(t *testing.T, s *claude.Session, n int) []*claude.IncomingRequest {
	t.Helper()
	var all []*claude.IncomingRequest
	ctx := testsupport.Deadline(t)
	for len(all) < n {
		r, e := s.Receive(ctx)
		require.NoError(t, e)
		if r.Request != nil {
			all = append(all, r.Request)
		}
	}
	return all
}

func FixtureKind(b []byte) string {
	var x struct{ Fixture string }
	_ = json.Unmarshal(b, &x)
	return x.Fixture
}
