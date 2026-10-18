// SPDX-License-Identifier: Apache-2.0

package claude

import "github.com/zoumo/claude-agent-sdk-go/types"

// Event and its views retain their canonical definitions and JSON codecs in types.
// These aliases preserve source compatibility and type identity across imports.
type (
	Event                = types.Event
	SystemEvent          = types.SystemEvent
	Message              = types.Message
	Content              = types.Content
	ContentBlock         = types.ContentBlock
	TextBlock            = types.TextBlock
	ThinkingBlock        = types.ThinkingBlock
	ToolUseBlock         = types.ToolUseBlock
	ToolResultBlock      = types.ToolResultBlock
	ResultEvent          = types.ResultEvent
	StreamEvent          = types.StreamEvent
	StreamDelta          = types.StreamDelta
	ControlRequest       = types.ControlRequest
	ControlCancelRequest = types.ControlCancelRequest
)

func parseEvent(frame []byte) (Event, error) { return types.ParseEvent(frame) }
