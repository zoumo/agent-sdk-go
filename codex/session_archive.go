package codex

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/zoumo/agent-sdk-go/codex/types"
)

// Archive requests native archival of the Session's fixed main thread.
// Success is native RPC acceptance, not proof that the thread is already
// archived; observe thread/archived through Receive for that separate fact.
// Archiving does not delete the transcript, close this Session or another
// Session, or select an arbitrary thread. The native server may shut down
// loaded execution and archive spawned descendant threads.
func (s *Session) Archive(ctx context.Context) (types.ThreadArchiveResponse, error) {
	var response types.ThreadArchiveResponse
	err := s.client.callOwned(ctx, "thread/archive", types.ThreadArchiveParams{ThreadID: s.root}, &response, false, nil, s)
	return response, err
}

// Unarchive requests native restoration of the Session's fixed main thread.
// The returned response preserves the native restored Thread and unknown
// fields. Success is RPC acceptance; observe thread/unarchived through Receive
// for the separate native notification. A native response for another thread
// is a protocol failure rather than a replacement main identity.
func (s *Session) Unarchive(ctx context.Context) (types.ThreadUnarchiveResponse, error) {
	result := threadUnarchiveResult{expectedID: s.root}
	err := s.client.callOwned(ctx, "thread/unarchive", types.ThreadUnarchiveParams{ThreadID: s.root}, &result, false, nil, s)
	return result.response, err
}

type threadUnarchiveResult struct {
	expectedID string
	response   types.ThreadUnarchiveResponse
}

func (r *threadUnarchiveResult) UnmarshalJSON(data []byte) error {
	var response types.ThreadUnarchiveResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}
	if response.Thread.ID != r.expectedID {
		return errors.New("unarchived thread ID does not match the requested thread ID")
	}
	r.response = response
	return nil
}
