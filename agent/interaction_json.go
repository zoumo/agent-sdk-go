package agent

import "encoding/json"

type permissionRequestJSON struct {
	Kind       InteractionKind    `json:"kind"`
	ToolCallID string             `json:"toolCallId"`
	Title      string             `json:"title,omitempty"`
	Options    []PermissionOption `json:"options"`
	Content    []ContentBlock     `json:"content,omitempty"`
}

type formRequestJSON struct {
	Kind            InteractionKind `json:"kind"`
	Message         string          `json:"message"`
	RequestedSchema map[string]any  `json:"requestedSchema"`
}

type urlRequestJSON struct {
	Kind    InteractionKind `json:"kind"`
	Message string          `json:"message"`
	URL     string          `json:"url"`
}

func (r InteractionRequest) MarshalJSON() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	switch r.Kind {
	case InteractionPermission:
		return json.Marshal(permissionRequestJSON{
			Kind:       r.Kind,
			ToolCallID: r.ToolCallID,
			Title:      r.Title,
			Options:    r.Options,
			Content:    r.Content,
		})
	case InteractionForm:
		return json.Marshal(formRequestJSON{
			Kind:            r.Kind,
			Message:         r.Message,
			RequestedSchema: r.RequestedSchema,
		})
	case InteractionURL:
		return json.Marshal(urlRequestJSON{
			Kind:    r.Kind,
			Message: r.Message,
			URL:     r.URL,
		})
	default:
		return nil, invalidArgument(
			"unsupported_interaction_kind",
			"interaction kind is not supported",
		)
	}
}

func (r *InteractionRequest) UnmarshalJSON(data []byte) error {
	var wire struct {
		Kind            InteractionKind    `json:"kind"`
		ToolCallID      string             `json:"toolCallId"`
		Title           string             `json:"title"`
		Options         []PermissionOption `json:"options"`
		Content         []json.RawMessage  `json:"content"`
		Message         string             `json:"message"`
		URL             string             `json:"url"`
		RequestedSchema map[string]any     `json:"requestedSchema"`
	}
	if err := decodeStrictJSON(data, &wire); err != nil {
		return invalidArgument("invalid_interaction_json", "decode interaction request: "+err.Error())
	}

	var content []ContentBlock
	if wire.Content != nil {
		content = make([]ContentBlock, len(wire.Content))
		for index, raw := range wire.Content {
			if err := content[index].UnmarshalJSON(raw); err != nil {
				return invalidArgument("invalid_interaction_content", err.Error())
			}
		}
	}
	*r = InteractionRequest{
		Kind:            wire.Kind,
		ToolCallID:      wire.ToolCallID,
		Title:           wire.Title,
		Options:         wire.Options,
		Content:         content,
		Message:         wire.Message,
		URL:             wire.URL,
		RequestedSchema: wire.RequestedSchema,
	}
	return r.Validate()
}
