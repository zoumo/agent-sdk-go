// This consumer checks public API imports and configuration without opening a client.
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
	var _ func(context.Context, codex.SessionOptions) (*codex.Session, error) = codex.Open
	var _ func(context.Context, codex.LoadOptions) (*codex.Session, error) = codex.Load
	var _ func(*codex.Session, context.Context, codex.Input) (codex.Submission, error) = (*codex.Session).Submit
	var _ func(*codex.Session, context.Context) (codex.Received, error) = (*codex.Session).Receive
	var _ func(codex.IncomingRequest, context.Context, types.ToolRequestUserInputResponse) error = codex.IncomingRequest.Answer

	_ = types.Event{}
	if reflect.TypeOf(params).PkgPath() != "github.com/zoumo/agent-sdk-go/codex/types" {
		panic("unexpected native package identity")
	}
	if _, err := json.Marshal(params); err != nil {
		panic(err)
	}
	fmt.Println(opts.Initialize.ClientInfo.Name, reflect.TypeOf(params).PkgPath())
}
