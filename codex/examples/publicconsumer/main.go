// Publicconsumer checks the SDK import boundary without connecting to a server.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	codex "github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

func main() {
	opts := codex.StdioOptions{Initialize: types.InitializeParams{
		ClientInfo: types.ClientInfo{Name: "publicconsumer", Version: "1"},
	}}
	params := types.ThreadStartParams{Ephemeral: types.Some(true)}
	var _ func(*codex.Client, context.Context, types.ThreadStartParams) (types.ThreadStartResponse, error) = (*codex.Client).StartThread
	var _ func(*codex.Client, context.Context, string, types.SubscriptionOptions) (*codex.Subscription, error) = (*codex.Client).Subscribe
	var _ func(*codex.Subscription) <-chan types.Event = (*codex.Subscription).Events
	_ = types.Event{}
	if reflect.TypeOf(params).PkgPath() != "github.com/zoumo/agent-sdk-go/codex/types" {
		panic("unexpected native package identity")
	}
	if _, err := json.Marshal(params); err != nil {
		panic(err)
	}
	fmt.Println(opts.Initialize.ClientInfo.Name, reflect.TypeOf(params).PkgPath())
}
