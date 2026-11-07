package agentcodex

import (
	"fmt"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
	codextypes "github.com/zoumo/agent-sdk-go/codex/types"
)

type unifiedToolItem struct {
	id           string
	kind         string
	name         string
	title        string
	callStatus   string
	resultStatus string
	rawInput     any
	rawOutput    *any
	content      []agent.ContentBlock
	exitCode     *int32
	durationMs   *float64
	output       *agent.ToolOutput
	meta         map[string]any
}

func (s *session) toolCall(
	received codex.Received,
	startedAtMs int64,
	item codextypes.ThreadItem,
) ([]agent.Event, error) {
	tool, known := unifiedCodexTool(item)
	if !known {
		return s.itemGap(received, item)
	}
	if tool.id == "" {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	event, err := s.newEvent(
		received,
		received.Event.Method,
		tool.id,
		agent.ToolCallPayload{
			ID:         tool.id,
			Kind:       tool.kind,
			Name:       tool.name,
			Title:      tool.title,
			RawInput:   tool.rawInput,
			Content:    tool.content,
			ExitCode:   tool.exitCode,
			DurationMs: tool.durationMs,
			Status:     nonemptyToolStatus(tool.callStatus),
		},
		unixMilliOrNow(startedAtMs),
		tool.meta,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func (s *session) toolResult(
	received codex.Received,
	completedAtMs int64,
	item codextypes.ThreadItem,
) ([]agent.Event, error) {
	tool, known := unifiedCodexTool(item)
	if !known {
		return s.itemGap(received, item)
	}
	if tool.id == "" {
		return nil, invalidNativeEvent(received.Event.Method)
	}
	var rawInput *any
	if tool.rawInput != nil {
		value := tool.rawInput
		rawInput = &value
	}
	output := tool.output
	if output == nil {
		output = unavailableToolOutput()
	}
	event, err := s.newEvent(
		received,
		received.Event.Method,
		tool.id,
		agent.ToolResultPayload{
			ID:         tool.id,
			Kind:       tool.kind,
			Name:       tool.name,
			Title:      tool.title,
			RawInput:   rawInput,
			RawOutput:  tool.rawOutput,
			Content:    tool.content,
			ExitCode:   tool.exitCode,
			DurationMs: tool.durationMs,
			Output:     output,
			Status:     nonemptyToolStatus(tool.resultStatus),
		},
		unixMilliOrNow(completedAtMs),
		tool.meta,
	)
	if err != nil {
		return nil, err
	}
	return []agent.Event{event}, nil
}

func unifiedCodexTool(item codextypes.ThreadItem) (unifiedToolItem, bool) {
	switch {
	case item.CommandExecution != nil:
		return commandTool(item.CommandExecution), true
	case item.FileChange != nil:
		return fileChangeTool(item.FileChange), true
	case item.FunctionCallOutput != nil:
		return functionTool(item.FunctionCallOutput), true
	case item.MCPToolCall != nil:
		return mcpTool(item.MCPToolCall), true
	case item.DynamicToolCall != nil:
		return dynamicTool(item.DynamicToolCall), true
	case item.CollabAgentToolCall != nil:
		return collabTool(item.CollabAgentToolCall), true
	case item.WebSearch != nil:
		return webSearchTool(item.WebSearch), true
	case item.ImageView != nil:
		return imageViewTool(item.ImageView), true
	case item.Sleep != nil:
		return sleepTool(item.Sleep), true
	case item.ImageGeneration != nil:
		return imageGenerationTool(item.ImageGeneration), true
	default:
		return unifiedToolItem{}, false
	}
}

func commandTool(item *codextypes.CommandExecutionThreadItem) unifiedToolItem {
	duration := optionalFloat(item.DurationMs)
	exitCode := optionalInt32(item.ExitCode)
	tool := unifiedToolItem{
		id:           item.ID,
		kind:         "execute",
		name:         "command",
		title:        item.Command,
		callStatus:   toolStatus(string(item.Status)),
		resultStatus: toolStatus(string(item.Status)),
		rawInput: map[string]any{
			"command": item.Command,
			"cwd":     item.Cwd,
		},
		exitCode:   exitCode,
		durationMs: duration,
		output:     unavailableToolOutput(),
	}
	if value, present := item.AggregatedOutput.Get(); present {
		tool.rawOutput = anyValuePointer(value)
		tool.content = []agent.ContentBlock{agent.TextBlock{Text: value}}
		tool.output = providedToolOutput()
	}
	return tool
}

func fileChangeTool(item *codextypes.FileChangeThreadItem) unifiedToolItem {
	return unifiedToolItem{
		id:           item.ID,
		kind:         "edit",
		name:         "file_change",
		callStatus:   toolStatus(string(item.Status)),
		resultStatus: toolStatus(string(item.Status)),
		rawInput:     map[string]any{"changes": item.Changes},
		output:       unavailableToolOutput(),
	}
}

func functionTool(item *codextypes.FunctionCallOutputThreadItem) unifiedToolItem {
	return unifiedToolItem{
		id:           item.ID,
		kind:         "function",
		name:         qualifiedToolName(item.Namespace, item.Name),
		callStatus:   "in_progress",
		resultStatus: "completed",
		rawOutput:    anyValuePointer(item.Output),
		output:       providedToolOutput(),
	}
}

func mcpTool(item *codextypes.MCPToolCallThreadItem) unifiedToolItem {
	tool := unifiedToolItem{
		id:           item.ID,
		kind:         "mcp",
		name:         qualifiedToolName(optionalString(item.Server), item.Tool),
		title:        item.Tool,
		callStatus:   toolStatus(string(item.Status)),
		resultStatus: toolStatus(string(item.Status)),
		rawInput:     item.Arguments,
		durationMs:   optionalFloat(item.DurationMs),
		output:       unavailableToolOutput(),
	}
	if result, present := item.Result.Get(); present {
		tool.rawOutput = anyValuePointer(result)
		tool.output = providedToolOutput()
	} else if itemError, present := item.Error.Get(); present {
		tool.rawOutput = anyValuePointer(itemError)
		tool.output = providedToolOutput()
	}
	return tool
}

func dynamicTool(item *codextypes.DynamicToolCallThreadItem) unifiedToolItem {
	tool := unifiedToolItem{
		id:           item.ID,
		kind:         "dynamic",
		name:         qualifiedToolName(item.Namespace, item.Tool),
		title:        item.Tool,
		callStatus:   toolStatus(string(item.Status)),
		resultStatus: toolStatus(string(item.Status)),
		rawInput:     item.Arguments,
		durationMs:   optionalFloat(item.DurationMs),
		output:       unavailableToolOutput(),
	}
	if content, present := item.ContentItems.Get(); present {
		tool.rawOutput = anyValuePointer(content)
		tool.output = providedToolOutput()
	}
	return tool
}

func collabTool(item *codextypes.CollabAgentToolCallThreadItem) unifiedToolItem {
	rawInput := map[string]any{
		"agentsStates":      item.AgentsStates,
		"receiverThreadIds": item.ReceiverThreadIDs,
		"senderThreadId":    item.SenderThreadID,
		"tool":              item.Tool,
	}
	if value, present := item.Model.Get(); present {
		rawInput["model"] = value
	}
	if value, present := item.Prompt.Get(); present {
		rawInput["prompt"] = value
	}
	if value, present := item.ReasoningEffort.Get(); present {
		rawInput["reasoningEffort"] = value
	}
	return unifiedToolItem{
		id:           item.ID,
		kind:         "collaboration",
		name:         string(item.Tool),
		callStatus:   toolStatus(string(item.Status)),
		resultStatus: toolStatus(string(item.Status)),
		rawInput:     rawInput,
		rawOutput:    anyValuePointer(item.AgentsStates),
		output:       providedToolOutput(),
		meta: map[string]any{
			"agentReceiverThreadIds": append([]string(nil), item.ReceiverThreadIDs...),
			"agentSenderThreadId":    item.SenderThreadID,
		},
	}
}

func webSearchTool(item *codextypes.WebSearchThreadItem) unifiedToolItem {
	rawInput := map[string]any{
		"query": item.Query,
	}
	if action, present := item.Action.Get(); present {
		rawInput["action"] = action
	}
	tool := unifiedToolItem{
		id:           item.ID,
		kind:         "search",
		name:         "web_search",
		title:        item.Query,
		callStatus:   "in_progress",
		resultStatus: "completed",
		rawInput:     rawInput,
		output:       unavailableToolOutput(),
	}
	if results, present := item.Results.Get(); present {
		tool.rawOutput = anyValuePointer(results)
		tool.output = providedToolOutput()
	}
	return tool
}

func imageViewTool(item *codextypes.ImageViewThreadItem) unifiedToolItem {
	return unifiedToolItem{
		id:           item.ID,
		kind:         "read",
		name:         "image_view",
		title:        item.Path,
		callStatus:   "in_progress",
		resultStatus: "completed",
		rawInput:     map[string]any{"path": item.Path},
		output:       unavailableToolOutput(),
	}
}

func sleepTool(item *codextypes.SleepThreadItem) unifiedToolItem {
	return unifiedToolItem{
		id:           item.ID,
		kind:         "wait",
		name:         "sleep",
		title:        fmt.Sprintf("Sleep for %dms", item.DurationMs),
		callStatus:   "in_progress",
		resultStatus: "completed",
		rawInput:     map[string]any{"durationMs": item.DurationMs},
		output:       unavailableToolOutput(),
	}
}

func imageGenerationTool(item *codextypes.ImageGenerationThreadItem) unifiedToolItem {
	title := "Generate image"
	rawInput := map[string]any{}
	if prompt, present := item.RevisedPrompt.Get(); present {
		title = prompt
		rawInput["revisedPrompt"] = prompt
	}
	if transparent, present := item.TransparentBackground.Get(); present {
		rawInput["transparentBackground"] = transparent
	}
	tool := unifiedToolItem{
		id:           item.ID,
		kind:         "generate",
		name:         "image_generation",
		title:        title,
		callStatus:   toolStatus(item.Status),
		resultStatus: toolStatus(item.Status),
		rawInput:     rawInput,
		output:       unavailableToolOutput(),
	}
	if failure, present := item.Failure.Get(); present {
		tool.rawOutput = anyValuePointer(failure)
		tool.output = providedToolOutput()
	} else if item.Result != "" {
		tool.rawOutput = anyValuePointer(item.Result)
		tool.output = providedToolOutput()
	}
	return tool
}

func qualifiedToolName(namespace codextypes.Optional[string], name string) string {
	if value, present := namespace.Get(); present && value != "" {
		return value + "." + name
	}
	return name
}

func optionalString(value string) codextypes.Optional[string] {
	if value == "" {
		return codextypes.Optional[string]{}
	}
	return codextypes.Some(value)
}

func optionalFloat(value codextypes.Optional[int64]) *float64 {
	converted, present := value.Get()
	if !present {
		return nil
	}
	result := float64(converted)
	return &result
}

func optionalInt32(value codextypes.Optional[int32]) *int32 {
	result, present := value.Get()
	if !present {
		return nil
	}
	return &result
}

func anyValuePointer(value any) *any {
	return new(value)
}

func providedToolOutput() *agent.ToolOutput {
	return &agent.ToolOutput{
		Availability: "provided",
		Completeness: "retained",
	}
}

func unavailableToolOutput() *agent.ToolOutput {
	return &agent.ToolOutput{
		Availability: "unavailable",
		Completeness: "unknown",
	}
}

func nonemptyToolStatus(status string) string {
	if status == "" {
		return "unknown"
	}
	return status
}
