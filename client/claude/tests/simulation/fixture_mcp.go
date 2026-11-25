package claudesim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/zoumo/agent-sdk-go/client/claude"
	"github.com/zoumo/agent-sdk-go/client/claude/types"
)

const fixtureMCPName = "fixture"

type fixtureMCP struct {
	usesManifest bool
	manifest     types.SDKMCPServerManifest
}

func newFixtureMCP(usesManifest bool) fixtureMCP {
	// Both material generations use the same local tool. The TS host declares
	// a literal path and advertises the handshake results on initialize.
	initialize := `{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"fixture","version":"1.0.0"}}`
	list := `{"tools":[{"name":"fixture_read","description":"Read only the synthetic numbers.txt file.","inputSchema":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}]}`
	if usesManifest {
		initialize = `{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"fixture","version":"1.0.0"}}`
		list = `{"tools":[{"name":"fixture_read","description":"Read only the synthetic numbers.txt file.","inputSchema":{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"path":{"type":"string","const":"numbers.txt"}},"required":["path"]},"execution":{"taskSupport":"forbidden"}}]}`
	}
	return fixtureMCP{usesManifest: usesManifest, manifest: types.SDKMCPServerManifest{
		InitializeResult: json.RawMessage(initialize),
		ToolsListResult:  json.RawMessage(list),
	}}
}

func (f fixtureMCP) reply(ctx context.Context, workspace string, incoming claude.IncomingRequest) error {
	request, err := incoming.MCPRequest()
	if err != nil {
		return err
	}
	var rpc struct {
		JSONRPC, Method string
		ID              json.RawMessage
		Params          struct {
			Name      string
			Arguments struct{ Path string }
		}
	}
	if err := json.Unmarshal(request.Message, &rpc); err != nil {
		return err
	}
	if request.ServerName != fixtureMCPName || rpc.JSONRPC != "2.0" {
		return errors.New("invalid fixture MCP request")
	}
	response := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id,omitempty"`
		Result  any             `json:"result"`
	}{JSONRPC: "2.0", ID: rpc.ID}
	switch rpc.Method {
	case "initialize":
		response.Result = f.manifest.InitializeResult
	case "notifications/initialized":
		if f.usesManifest && rpc.ID == nil {
			response.ID = json.RawMessage("0")
		}
		response.Result = struct{}{}
	case "tools/list":
		response.Result = f.manifest.ToolsListResult
	case "tools/call":
		if rpc.Params.Name != "fixture_read" || rpc.Params.Arguments.Path != "numbers.txt" {
			return errors.New("fixture MCP only reads numbers.txt")
		}
		root, err := os.OpenRoot(workspace)
		if err != nil {
			return err
		}
		defer root.Close()
		contents, err := root.ReadFile("numbers.txt")
		if err != nil {
			return err
		}
		result := map[string]any{"content": []map[string]string{{"type": "text", "text": string(contents)}}}
		if !f.usesManifest {
			result["isError"] = false
		}
		response.Result = result
	default:
		return fmt.Errorf("unsupported fixture MCP method %q", rpc.Method)
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return incoming.ReplyMCP(ctx, payload)
}
