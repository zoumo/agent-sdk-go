package claude

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/claude/types"
)

func TestSessionIDAssignsNativeIdentityBeforeInput(t *testing.T) {
	const assigned = "11111111-1111-4111-8111-111111111111"
	options := fixtureOptions(t, "tree")
	options.SessionID = assigned
	args, err := NativeArgs(SessionOptions(options))
	require.NoError(t, err)
	require.Contains(t, args, "--session-id="+assigned)
	session, err := Open(context.Background(), SessionOptions(options))
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	sessionID, known := session.SessionID()
	require.True(t, known)
	require.Equal(t, assigned, sessionID)
	require.Empty(t, session.RequestedResumeID())
	require.NoError(t, session.Close())
	sessionID, known = session.SessionID()
	require.True(t, known)
	require.Equal(t, assigned, sessionID)
}

func TestSessionIDAndResumeIDAreMutuallyExclusiveBeforeLaunch(t *testing.T) {
	options := SessionOptions{CLIPath: "/nonexistent", SessionID: "assigned", ResumeID: "resumed"}
	_, err := Open(context.Background(), options)
	require.ErrorIs(t, err, ErrInvalidInput)
	_, err = NativeArgs(options)
	require.ErrorIs(t, err, ErrInvalidInput)
}

func TestAssignedRootRetainsVerifiedSubagentHistory(t *testing.T) {
	const assigned = "11111111-1111-4111-8111-111111111111"
	session, err := newSession(Options{
		SessionID: assigned,
		Subagents: SubagentOptions{History: []SubagentHistory{
			{SessionID: assigned, TaskID: "parent"},
			{SessionID: assigned, TaskID: "child", ParentTaskID: "parent"},
		}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { session.finish(nil) })
	record, err := session.Receive(context.Background())
	require.NoError(t, err)
	require.Equal(t, "caller-verified-history", record.Observation.Evidence)
	require.Equal(t, "parent", record.Observation.Parent.TaskID)
	require.Equal(t, assigned, record.Observation.Source.SessionID)
}

func TestStartupForwardingCoverageMatchesInitializeRequest(t *testing.T) {
	for _, identity := range []string{"", "11111111-1111-4111-8111-111111111111"} {
		for _, test := range []struct {
			name       string
			initialize *types.InitializeOptions
			requested  bool
		}{
			{"default", nil, true},
			{"enabled", &types.InitializeOptions{ForwardSubagentText: new(true)}, true},
			{"disabled", &types.InitializeOptions{ForwardSubagentText: new(false)}, false},
			{"omitted", &types.InitializeOptions{}, false},
		} {
			t.Run(identity+"/"+test.name, func(t *testing.T) {
				session, err := newSession(Options{SessionID: identity, Initialize: test.initialize})
				require.NoError(t, err)
				t.Cleanup(func() { session.finish(nil) })
				if !test.requested {
					require.Zero(t, session.fifo.Stats().Items)
					return
				}
				require.Equal(t, 1, session.fifo.Stats().Items)
				record, err := session.Receive(t.Context())
				require.NoError(t, err)
				require.Equal(t, ReceivedObservation, record.Kind)
				require.NotNil(t, record.Observation)
				require.Equal(t, "forwarding-not-confirmed", record.Observation.Reason)
				require.Equal(t, "initialize-forwardSubagentText-is-a-request", record.Observation.Evidence)
				require.Zero(t, session.fifo.Stats().Items)
			})
		}
	}
}
