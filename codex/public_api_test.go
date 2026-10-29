// Public API signatures and configuration, without opening a client.
package codex_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func TestPublicAPIConfiguration(t *testing.T) {
	opts := codex.StdioOptions{Initialize: types.InitializeParams{
		ClientInfo: types.ClientInfo{Name: "publicconsumer", Version: "1"},
	}}
	params := types.ThreadStartParams{Ephemeral: types.Some(true)}
	var _ func(context.Context, codex.SessionOptions) (*codex.Session, error) = codex.Open
	_ = codex.SessionOptions{ChildDetailPolicy: codex.ChildDetailSuppress}
	_ = codex.SessionOptions{ChildDetailPolicy: codex.ChildDetailPreserve}
	var _ func(*codex.Session, context.Context, codex.Input) (codex.Submission, error) = (*codex.Session).Submit
	var _ func(*codex.Session, context.Context) (codex.Received, error) = (*codex.Session).Receive
	var _ func(codex.IncomingRequest, context.Context, types.ToolRequestUserInputResponse) error = codex.IncomingRequest.Answer

	var _ func(*codex.Client, context.Context, types.ThreadListParams) (types.ThreadListResponse, error) = (*codex.Client).ListThreads
	var _ func(*codex.Client, context.Context, types.ThreadReadParams) (types.ThreadReadResponse, error) = (*codex.Client).ReadThread
	var _ func(*codex.Client, context.Context, types.ThreadTurnsListParams) (types.ThreadTurnsListResponse, error) = (*codex.Client).ListThreadTurns
	var _ func(*codex.Client, context.Context, types.ThreadItemsListParams) (types.ThreadItemsListResponse, error) = (*codex.Client).ListThreadItems

	_ = types.Event{}
	assert.Equal(t, "github.com/zoumo/agent-sdk-go/codex/types", reflect.TypeOf(params).PkgPath())
	_, err := json.Marshal(params)
	require.NoError(t, err)
	assert.Equal(t, "publicconsumer", opts.Initialize.ClientInfo.Name)
}
