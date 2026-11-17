package eventstream

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
)

func TestExplicitCloseDiscardsUnclaimedBacklog(t *testing.T) {
	for _, naturalFirst := range []bool{false, true} {
		name := "explicit"
		if naturalFirst {
			name = "natural_then_explicit"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var stream *Stream
				stream = New(Hooks{
					CloseNative: func() error {
						stream.ObservationEnded(nil)
						return nil
					},
					CloseEvents: func(End) []agent.Event {
						return []agent.Event{{EventID: "closure"}}
					},
				})
				defer func() { assert.NoError(t, stream.events.Abort(nil)) }()
				require.NoError(t, stream.Publish([]agent.Event{{EventID: "accepted"}}))
				if naturalFirst {
					stream.ObservationEnded(nil)
					<-stream.Done()
					assert.Equal(t, 2, stream.events.Stats().Items)
				}
				require.NoError(t, stream.Close(context.Background()))
				_, err := stream.Claim()
				require.Error(t, err)
				assert.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
				assert.Zero(t, stream.events.Stats().Items, "no consumer can drain these events")
			})
		})
	}
}
