package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/codex/types"
)

func TestFailedResumeDiscardsEarlyNotifications(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sdkReader, peerWriter := io.Pipe()
		peerReader, sdkWriter := io.Pipe()
		var client *Client
		owners := make(chan *Session, 3)
		peerDone := make(chan error, 1)
		go func() {
			defer peerWriter.Close()
			defer peerReader.Close()
			scanner := bufio.NewScanner(peerReader)
			for scanner.Scan() {
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
				}
				if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
					peerDone <- err
					return
				}
				var response string
				switch request.Method {
				case "initialize":
					response = `{"userAgent":"synthetic","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}`
				case "thread/resume":
					client.mu.Lock()
					owners <- client.ownership.ownerLocked("resume-target")
					client.mu.Unlock()
					if _, err := fmt.Fprintln(peerWriter, `{"method":"future/early","params":{"threadId":"resume-target"}}`); err != nil {
						peerDone <- err
						return
					}
					if _, err := fmt.Fprintf(peerWriter, "{\"id\":%s,\"error\":{\"code\":-32603,\"message\":\"failed after notification\"}}\n", request.ID); err != nil {
						peerDone <- err
						return
					}
					continue
				case "thread/unsubscribe":
					response = `{"status":"unsubscribed"}`
				default:
					continue
				}
				if _, err := fmt.Fprintf(peerWriter, "{\"id\":%s,\"result\":%s}\n", request.ID, response); err != nil {
					peerDone <- err
					return
				}
			}
			peerDone <- scanner.Err()
		}()
		var err error
		client, err = NewStreams(context.Background(), StreamOptions{}, StreamTransport{
			Reader: sdkReader, Writer: sdkWriter,
		})
		require.NoError(t, err)
		defer client.Close()
		for range 3 {
			s, openErr := Open(context.Background(), SessionOptions{
				Client: client, Resume: &types.ThreadResumeParams{ThreadID: "resume-target"},
			})
			require.Error(t, openErr)
			require.Nil(t, s)
			var call *CallError
			require.ErrorAs(t, openErr, &call)
			assert.Equal(t, Responded, call.Dispatch)
			owner := <-owners
			require.NotNil(t, owner)
			// Keep the test leak-free even when the resource assertion fails.
			assert.Zero(t, owner.queue.Stats().Items, "failed Open never returns a consumer")
			require.NoError(t, owner.Abort())
		}
		require.NoError(t, client.Close())
		require.NoError(t, <-peerDone)
	})
}
