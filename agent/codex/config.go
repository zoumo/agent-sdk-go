package agentcodex

import (
	"encoding/json"
	"slices"

	"github.com/zoumo/agent-sdk-go/codex"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

// Config fixes the explicitly selected Codex app-server process and its native
// initialization. The provider owns exactly one connection created from it.
type Config struct {
	Stdio codex.StdioOptions
}

func cloneStdioOptions(value codex.StdioOptions) codex.StdioOptions {
	cloned := value
	cloned.Args = slices.Clone(value.Args)
	cloned.Env = slices.Clone(value.Env)
	cloned.Initialize.ClientInfo.UnknownFields = cloneRawMessages(
		value.Initialize.ClientInfo.UnknownFields,
	)
	cloned.Initialize.UnknownFields = cloneRawMessages(value.Initialize.UnknownFields)
	if capabilities, ok := value.Initialize.Capabilities.Get(); ok {
		capabilities.UnknownFields = cloneRawMessages(capabilities.UnknownFields)
		capabilities.Extensions = cloneOptionalRawMessages(capabilities.Extensions)
		capabilities.OptOutNotificationMethods = cloneOptionalStrings(
			capabilities.OptOutNotificationMethods,
		)
		cloned.Initialize.Capabilities = types.Some(capabilities)
	} else if value.Initialize.Capabilities.IsNull() {
		cloned.Initialize.Capabilities = types.Null[types.InitializeCapabilities]()
	}
	return cloned
}

func cloneOptionalRawMessages(
	value types.Optional[map[string]json.RawMessage],
) types.Optional[map[string]json.RawMessage] {
	if value.IsNull() {
		return types.Null[map[string]json.RawMessage]()
	}
	items, ok := value.Get()
	if !ok {
		return types.Optional[map[string]json.RawMessage]{}
	}
	return types.Some(cloneRawMessages(items))
}

func cloneOptionalStrings(value types.Optional[[]string]) types.Optional[[]string] {
	if value.IsNull() {
		return types.Null[[]string]()
	}
	items, ok := value.Get()
	if !ok {
		return types.Optional[[]string]{}
	}
	return types.Some(slices.Clone(items))
}

func cloneRawMessages(value map[string]json.RawMessage) map[string]json.RawMessage {
	if value == nil {
		return nil
	}
	cloned := make(map[string]json.RawMessage, len(value))
	for key, raw := range value {
		cloned[key] = append(json.RawMessage(nil), raw...)
	}
	return cloned
}
