// Public API signatures and configuration, without opening a client.
package codex_test

import (
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
	_ = codex.Open
	_ = codex.SessionOptions{ChildDetailPolicy: codex.ChildDetailSuppress}
	_ = codex.SessionOptions{ChildDetailPolicy: codex.ChildDetailPreserve}
	_ = (*codex.Session).Submit
	_ = (*codex.Session).Receive
	_ = (*codex.Session).Archive
	_ = (*codex.Session).Unarchive
	_ = codex.IncomingRequest.Answer

	_ = (*codex.Client).ListThreads
	_ = (*codex.Client).ReadThread
	_ = (*codex.Client).ListThreadTurns
	_ = (*codex.Client).ListThreadItems

	_ = types.Event{}
	assert.Equal(t, "github.com/zoumo/agent-sdk-go/codex/types", reflect.TypeOf(params).PkgPath())
	_, err := json.Marshal(params)
	require.NoError(t, err)
	assert.Equal(t, "publicconsumer", opts.Initialize.ClientInfo.Name)
}
