package integration_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// Deliberately strict independent peer: no execution or Session operation exists.
func historyQueryScenarioPeer() error {
	const thread = `{"id":"saved","sessionId":"s","cliVersion":"0.162.1","createdAt":1,"updatedAt":2,"cwd":"/server","ephemeral":false,"modelProvider":"synthetic","preview":"preview","projectId":null,"source":"cli","status":{"type":"notLoaded"},"turns":[]}`
	var methods []string
	scan := bufio.NewScanner(os.Stdin)
	for scan.Scan() {
		var m struct {
			ID     json.RawMessage
			Method string
			Params struct{ Cursor string }
		}
		if err := json.Unmarshal(scan.Bytes(), &m); err != nil {
			return err
		}
		methods = append(methods, m.Method)
		result := `{}`
		switch m.Method {
		case "initialize":
			result = `{"userAgent":"synthetic","codexHome":"/server","platformFamily":"unix","platformOs":"linux"}`
		case "initialized":
			continue
		case "thread/list":
			result = `{"data":[` + thread + `],"nextCursor":null}`
		case "thread/read":
			result = `{"thread":` + thread + `,"readState":{"future":true}}`
		case "thread/turns/list":
			if m.Params.Cursor == "" {
				result = `{"data":[{"id":"turn","items":[],"itemsView":"summary","status":"completed","error":null}],"nextCursor":"opaque"}`
			} else {
				result = `{"data":[],"nextCursor":null}`
			}
		case "thread/items/list":
			result = `{"data":[{"turnId":"turn","item":{"type":"futureItem","id":"item","n":9007199254740993}}],"nextCursor":null}`
		case "fixture/query-trace":
			raw, err := json.Marshal(methods)
			if err != nil {
				return err
			}
			result = string(raw)
		default:
			return fmt.Errorf("unexpected history operation %q", m.Method)
		}
		fmt.Fprintf(os.Stdout, `{"id":%s,"result":%s}`+"\n", m.ID, result)
	}
	return scan.Err()
}
