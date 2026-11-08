package agent

import (
	"encoding/json"
	"math"
)

func validatePayload(payload Payload) error {
	switch value := payload.(type) {
	case UserMessage:
		return validateMessagePayload(MessagePayload(value))
	case AgentMessage:
		return validateMessagePayload(MessagePayload(value))
	case AgentThinking:
		return validateMessagePayload(MessagePayload(value))
	case MessageChunk:
		return validateMessagePayload(MessagePayload(value))
	case ThinkingChunk:
		return validateMessagePayload(MessagePayload(value))
	case ToolCallPayload:
		if value.ID == "" || value.Kind == "" {
			return invalidArgument("invalid_tool_call", "tool call requires ID and kind")
		}
		if err := validateToolDetails(
			value.Status,
			value.RawInput,
			value.RawOutput,
			value.Content,
			value.DurationMs,
			value.Output,
		); err != nil {
			return err
		}
	case ToolResultPayload:
		if value.ID == "" || value.Kind == "" || value.Status == "" {
			return invalidArgument(
				"invalid_tool_result",
				"tool result requires ID, kind, and status",
			)
		}
		if err := validateOptionalJSON(value.RawInput); err != nil {
			return invalidArgument("invalid_tool_input", "tool result input is not JSON encodable")
		}
		if err := validateToolDetails(
			value.Status,
			nil,
			value.RawOutput,
			value.Content,
			value.DurationMs,
			value.Output,
		); err != nil {
			return err
		}
	case ToolChunkPayload:
		if value.ID == "" || len(value.Content) == 0 {
			return invalidArgument("invalid_tool_chunk", "tool chunk requires ID and content")
		}
		if err := validateOptionalContent(value.Content); err != nil {
			return err
		}
	case StateUpdatePayload:
		for _, state := range []*SessionState{value.PreviousState, value.State} {
			if state != nil && !validSessionState(*state) {
				return invalidArgument("invalid_session_state", "state value is not supported")
			}
		}
		if value.Error != nil {
			if value.Error.Code != nil {
				if _, ok := value.Error.Code.(string); !ok {
					if _, ok := value.Error.Code.(json.Number); !ok {
						return invalidArgument(
							"invalid_observed_error",
							"observed error code must be a string or JSON number",
						)
					}
				}
			}
			if err := validateJSONValue(value.Error.Code); err != nil {
				return invalidArgument(
					"invalid_observed_error",
					"observed error code is not JSON encodable",
				)
			}
		}
	case SessionErrorPayload:
		if value.Message == "" {
			return invalidArgument("empty_session_error", "session error message is required")
		}
	case InfoUpdatePayload:
		if value.Title == nil && value.UpdatedAt == nil {
			return invalidArgument("empty_info_update", "info update requires a changed field")
		}
		if value.UpdatedAt != nil {
			if err := validateJSONValue(*value.UpdatedAt); err != nil {
				return invalidArgument(
					"invalid_info_update",
					"info update timestamp is not JSON encodable",
				)
			}
		}
	case ConfigOptionUpdatePayload:
		if value.ConfigOptions == nil {
			return invalidArgument("missing_config_options", "config update requires options")
		}
		for _, option := range value.ConfigOptions {
			if err := validateConfigOption(option); err != nil {
				return err
			}
		}
	case AvailableCommandsUpdatePayload:
		if value.AvailableCommands == nil {
			return invalidArgument("missing_available_commands", "command update requires commands")
		}
		for _, command := range value.AvailableCommands {
			if command.Name == "" {
				return invalidArgument("invalid_available_command", "command name is required")
			}
		}
	case UsageUpdatePayload:
		if value.Used < 0 || value.Size < 0 {
			return invalidArgument("invalid_usage", "usage values must not be negative")
		}
		if value.Cost != nil {
			if value.Cost.Amount == "" {
				return invalidArgument("missing_usage_amount", "usage cost amount is required")
			}
			if err := validateJSONValue(value.Cost.Amount); err != nil {
				return invalidArgument(
					"invalid_usage_amount",
					"usage cost amount is not a JSON number",
				)
			}
		}
	case InteractionRequestedPayload:
		if value.Interaction == "" {
			return invalidArgument("missing_interaction_ref", "interaction reference is required")
		}
		if err := value.Request.Validate(); err != nil {
			return err
		}
	case InteractionClosedPayload:
		if value.Interaction == "" {
			return invalidArgument("missing_interaction_ref", "interaction reference is required")
		}
		if !validInteractionOutcome(value.Outcome) {
			return invalidArgument("invalid_interaction_outcome", "interaction outcome is not supported")
		}
	default:
		return invalidArgument("unsupported_payload", "event payload is not supported")
	}
	return nil
}

func validateMessagePayload(payload MessagePayload) error {
	if payload.MessageID == "" || len(payload.Content) == 0 {
		return invalidArgument("invalid_message", "message requires ID and content")
	}
	return validateOptionalContent(payload.Content)
}

func validateToolDetails(
	status string,
	rawInput any,
	rawOutput *any,
	content []ContentBlock,
	durationMs *float64,
	output *ToolOutput,
) error {
	if status != "" && !validToolStatus(status) {
		return invalidArgument("invalid_tool_status", "tool status is not supported")
	}
	if err := validateJSONValue(rawInput); err != nil {
		return invalidArgument("invalid_tool_input", "tool input is not JSON encodable")
	}
	if rawOutput != nil {
		if err := validateJSONValue(*rawOutput); err != nil {
			return invalidArgument("invalid_tool_output", "tool output is not JSON encodable")
		}
	}
	if err := validateOptionalContent(content); err != nil {
		return err
	}
	if durationMs != nil {
		if *durationMs < 0 ||
			math.IsNaN(*durationMs) ||
			math.IsInf(*durationMs, 0) {
			return invalidArgument(
				"invalid_tool_duration",
				"tool duration must be finite and nonnegative",
			)
		}
	}
	return validateToolOutput(output)
}

func validateToolOutput(output *ToolOutput) error {
	if output == nil {
		return nil
	}
	switch output.Availability {
	case "provided", "unavailable":
	default:
		return invalidArgument("invalid_tool_output", "tool output availability is not supported")
	}
	switch output.Completeness {
	case "retained", "truncated", "unknown":
	default:
		return invalidArgument("invalid_tool_output", "tool output completeness is not supported")
	}
	if output.Truncation != nil {
		if output.Truncation.RetainedBytes < 0 {
			return invalidArgument("invalid_tool_truncation", "retained bytes must not be negative")
		}
		if output.Truncation.OmittedBytes != nil && *output.Truncation.OmittedBytes < 0 {
			return invalidArgument("invalid_tool_truncation", "omitted bytes must not be negative")
		}
	}
	return nil
}

func validateConfigOption(option ConfigOption) error {
	if option.ID == "" || option.Name == "" || option.Type == "" {
		return invalidArgument("invalid_config_option", "config option identity is required")
	}
	if err := validateJSONValue(option.CurrentValue); err != nil {
		return invalidArgument(
			"invalid_config_value",
			"config option value is not JSON encodable",
		)
	}
	switch option.Type {
	case "boolean":
		if _, ok := option.CurrentValue.(bool); !ok || option.Options != nil {
			return invalidArgument("invalid_config_option", "boolean config option is invalid")
		}
	case "select":
		if _, ok := option.CurrentValue.(string); !ok {
			return invalidArgument("invalid_config_option", "select config value must be a string")
		}
		switch options := option.Options.(type) {
		case []ConfigOptionValue:
			if options == nil {
				return invalidArgument("invalid_config_option", "select config options are required")
			}
		case []ConfigOptionGroup:
			if options == nil {
				return invalidArgument("invalid_config_option", "select config groups are required")
			}
			for _, group := range options {
				if group.Group == "" || group.Name == "" || group.Options == nil {
					return invalidArgument("invalid_config_option", "config option group is invalid")
				}
			}
		default:
			return invalidArgument("invalid_config_option", "select config options are invalid")
		}
	default:
		return invalidArgument("unsupported_config_option", "config option type is not supported")
	}
	return nil
}

func validSessionState(state SessionState) bool {
	switch state {
	case StateIdle, StateRunning, StateRequiresAction:
		return true
	default:
		return false
	}
}

func validInteractionOutcome(outcome InteractionOutcome) bool {
	switch outcome {
	case InteractionAnswered,
		InteractionCompleted,
		InteractionCancelled,
		InteractionExpired,
		InteractionFailed:
		return true
	default:
		return false
	}
}

func validToolStatus(status string) bool {
	switch status {
	case "pending", "in_progress", "completed", "failed", "declined", "unknown":
		return true
	default:
		return false
	}
}

func validateOptionalJSON(value any) error {
	if value == nil {
		return nil
	}
	return validateJSONValue(value)
}
