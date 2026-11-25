package codexsim

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/zoumo/agent-sdk-go/client/codex"
	"github.com/zoumo/agent-sdk-go/client/codex/types"
)

func submitRecordedTurn(ctx context.Context, client *codex.Client, session *codex.Session, raw json.RawMessage) (types.TurnStartResponse, error) {
	var params types.TurnStartParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return types.TurnStartResponse{}, err
	}
	if params.ThreadID != session.ThreadID() {
		return types.TurnStartResponse{}, errors.New("foreign input thread")
	}
	if len(params.UnknownFields) != 0 {
		// The Rust driver explicitly serializes this unset option. The native
		// DTO preserves it, while Session.Submit has no field for it.
		if len(params.UnknownFields) != 1 || string(params.UnknownFields["responsesapiClientMetadataPatch"]) != "null" {
			return types.TurnStartResponse{}, errors.New("unsupported recorded turn options")
		}
		for _, part := range params.Input {
			if len(part.Unknown) != 0 {
				return types.TurnStartResponse{}, codex.ErrInvalidInput
			}
		}
		var response types.TurnStartResponse
		err := client.Call(ctx, "turn/start", params, &response)
		return response, err
	}
	var input codex.Input
	if err := json.Unmarshal(raw, &input); err != nil {
		return types.TurnStartResponse{}, err
	}
	submission, err := session.Submit(ctx, input)
	return submission.Response, err
}
