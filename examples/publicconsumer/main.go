// Publicconsumer checks the SDK import boundary without connecting to a server.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	codex "github.com/zoumo/codex-sdk-go"
	"github.com/zoumo/codex-sdk-go/types"
)

func main() {
	opts := codex.StdioOptions{Initialize: types.InitializeParams{
		ClientInfo: types.ClientInfo{Name: "publicconsumer", Version: "1"},
	}}
	params := types.ThreadStartParams{Ephemeral: types.Some(true)}
	var _ func(*codex.Client, context.Context, types.ThreadStartParams) (types.ThreadStartResponse, error) = (*codex.Client).StartThread
	_ = codex.Event{}
	_ = codex.TreeEvent{}
	if reflect.TypeOf(params).PkgPath() != "github.com/zoumo/codex-sdk-go/types" {
		panic("unexpected native package identity")
	}
	if _, err := json.Marshal(params); err != nil {
		panic(err)
	}
	fmt.Println(opts.Initialize.ClientInfo.Name, reflect.TypeOf(params).PkgPath())
}
