package agentcodex

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/zoumo/agent-sdk-go/agent"
	"github.com/zoumo/agent-sdk-go/codex"
)

const mcpElicitationMethod = "mcpServer/elicitation/request"

// The native SDK exposes this method through its raw request handle. Only
// the standard form and URL branches of the app-server union are mapped here.
type mcpElicitationParams struct {
	ThreadID        string         `json:"threadId"`
	TurnID          *string        `json:"turnId"`
	ServerName      string         `json:"serverName"`
	Mode            string         `json:"mode"`
	Message         string         `json:"message"`
	RequestedSchema map[string]any `json:"requestedSchema"`
	URL             string         `json:"url"`
	ElicitationID   string         `json:"elicitationId"`
	Meta            any            `json:"_meta"`
}

// Match the nullable fields of McpServerElicitationRequestResponse. In
// particular, an accepted empty form has {} content, a tool approval has null.
type mcpElicitationResponse struct {
	Action  string            `json:"action"`
	Content map[string]any    `json:"content"`
	Meta    map[string]string `json:"_meta"`
}

func (s *session) mcpElicitation(
	received codex.Received, request codex.IncomingRequest, threadID string,
) ([]agent.Event, error) {
	var params mcpElicitationParams
	decoder := json.NewDecoder(bytes.NewReader(request.Params()))
	decoder.UseNumber()
	if err := decoder.Decode(&params); err != nil || params.ThreadID != threadID ||
		params.ServerName == "" || params.Message == "" || (params.TurnID != nil && *params.TurnID == "") {
		return s.unsupportedRequest(received, "invalid_native_request", "Codex MCP elicitation has invalid parameters")
	}
	interaction := &pendingInteraction{
		method: request.Method(), threadID: threadID, startedAt: time.Now().UTC(),
		blocking: true, request: request, received: received,
	}
	if params.TurnID != nil {
		interaction.turnID = *params.TurnID
	}
	var unified agent.InteractionRequest
	switch params.Mode {
	case "form":
		properties, ok := params.RequestedSchema["properties"].(map[string]any)
		if params.RequestedSchema["type"] != "object" || !ok {
			return s.unsupportedRequest(received, "invalid_native_request", "Codex MCP form requires an object schema with properties")
		}
		meta, _ := params.Meta.(map[string]any)
		if meta["codex_approval_kind"] == "mcp_tool_call" {
			// The pinned tool approval carries an empty form. A permission
			// selection cannot supply additional required form input.
			required, isArray := params.RequestedSchema["required"].([]any)
			if len(properties) != 0 || (params.RequestedSchema["required"] != nil && (!isArray || len(required) != 0)) {
				return s.unsupportedRequest(received, "invalid_native_request", "Codex MCP tool approval requests additional form fields")
			}
			options, choices, valid := mcpApprovalChoices(request, meta["persist"])
			if !valid {
				return s.unsupportedRequest(received, "invalid_native_request", "Codex MCP tool approval has invalid persist options")
			}
			unified = agent.InteractionRequest{
				Kind: agent.InteractionPermission, Title: params.Message, Options: options,
				Content: mcpApprovalContent(params.ServerName, meta),
			}
			interaction.choices = choices
		} else {
			unified = agent.InteractionRequest{
				Kind: agent.InteractionForm, Message: params.Message, RequestedSchema: params.RequestedSchema,
			}
		}
	case "url":
		if params.URL == "" || params.ElicitationID == "" {
			return s.unsupportedRequest(received, "invalid_native_request", "Codex MCP URL elicitation requires a URL and elicitation ID")
		}
		unified = agent.InteractionRequest{Kind: agent.InteractionURL, Message: params.Message, URL: params.URL}
	default:
		return s.unsupportedRequest(received, "native_request_unsupported", "Codex MCP elicitation mode is unsupported")
	}
	interaction.kind = unified.Kind
	return s.registerInteraction(interaction, unified)
}

func mcpApprovalContent(serverName string, meta map[string]any) []agent.ContentBlock {
	content := []agent.ContentBlock{{Text: &agent.ContentBlockText{Text: "MCP server: " + serverName}}}
	if description, ok := meta["tool_description"].(string); ok && description != "" {
		content = append(content, agent.ContentBlock{Text: &agent.ContentBlockText{Text: description}})
	}
	if args, ok := meta["tool_params"]; ok {
		// The decoder has already restricted args to JSON values.
		raw, err := json.Marshal(args)
		if err == nil {
			content = append(content, agent.ContentBlock{Text: &agent.ContentBlockText{Text: string(raw)}})
		}
	}
	return content
}

func mcpApprovalChoices(request codex.IncomingRequest, persist any) (
	[]agent.PermissionOption, map[string]func(context.Context) error, bool,
) {
	var scopes []string
	switch value := persist.(type) {
	case nil:
	case string:
		scopes = []string{value}
	case []any:
		for _, item := range value {
			scope, ok := item.(string)
			if !ok {
				return nil, nil, false
			}
			scopes = append(scopes, scope)
		}
	default:
		return nil, nil, false
	}

	var options []agent.PermissionOption
	choices := make(map[string]func(context.Context) error)
	add := func(option agent.PermissionOption, action string, meta map[string]string) {
		options = append(options, option)
		choices[option.OptionID] = func(ctx context.Context) error {
			return request.Handle().Answer(ctx, mcpElicitationResponse{Action: action, Meta: meta})
		}
	}
	add(permissionOption("allow-once", "Allow once", agent.PermissionAllow, agent.PermissionScopeOnce), "accept", nil)
	if slices.Contains(scopes, "session") {
		add(permissionOption("allow-session", "Allow for this session", agent.PermissionAllow, agent.PermissionScopeSession),
			"accept", map[string]string{"persist": "session"})
	}
	if slices.Contains(scopes, "always") {
		add(permissionOption("allow-always", "Always allow", agent.PermissionAllow, agent.PermissionScopeAlways),
			"accept", map[string]string{"persist": "always"})
	}
	add(permissionOption("reject-once", "Reject once", agent.PermissionReject, agent.PermissionScopeOnce), "decline", nil)
	add(permissionOption(permissionCancel, "Cancel", agent.PermissionCancel, ""), "cancel", map[string]string{"codex_approval_cancellation_reason": "user"})
	return options, choices, true
}

func mcpActionReply(request codex.IncomingRequest, action string, content map[string]any) (
	func(context.Context) error, agent.InteractionOutcome, error,
) {
	outcome := agent.InteractionAnswered
	if action == "cancel" {
		outcome = agent.InteractionCancelled
	}
	return func(ctx context.Context) error {
		return request.Handle().Answer(ctx, mcpElicitationResponse{Action: action, Content: content})
	}, outcome, nil
}
