package agent

import "reflect"

// InteractionRef is an opaque reference to one live native request. It grants
// no authority outside the adapter and session that created it.
type InteractionRef string

type InteractionKind string

const (
	InteractionPermission InteractionKind = "permission"
	InteractionForm       InteractionKind = "form"
	InteractionURL        InteractionKind = "url"
)

type PermissionOption struct {
	OptionID string               `json:"optionId"`
	Name     string               `json:"name"`
	Kind     PermissionOptionKind `json:"kind"`
	Scope    PermissionScope      `json:"scope,omitempty"`
}

type PermissionOptionKind string

const (
	PermissionAllow  PermissionOptionKind = "allow"
	PermissionReject PermissionOptionKind = "reject"
	PermissionCancel PermissionOptionKind = "cancel"
)

// Deprecated: These kinds preserve historical events. The old "always" kinds
// did not distinguish session approval from a persistent policy change.
const (
	PermissionAllowOnce    PermissionOptionKind = "allow_once"
	PermissionAllowAlways  PermissionOptionKind = "allow_always"
	PermissionRejectOnce   PermissionOptionKind = "reject_once"
	PermissionRejectAlways PermissionOptionKind = "reject_always"
)

// PermissionScope is the validity period of an allow or reject option.
type PermissionScope string

const (
	PermissionScopeOnce    PermissionScope = "once"
	PermissionScopeTurn    PermissionScope = "turn"
	PermissionScopeSession PermissionScope = "session"
	PermissionScopeAlways  PermissionScope = "always"
)

//nolint:recvcheck // JSON decoding mutates a request; reads and encoding accept values.
type InteractionRequest struct {
	Kind            InteractionKind
	ToolCallID      string
	Title           string
	Options         []PermissionOption
	Content         []ContentBlock
	Message         string
	URL             string
	RequestedSchema map[string]any
}

// Validate checks the fields required by the request's interaction kind and
// rejects fields that belong to another kind.
func (r InteractionRequest) Validate() error {
	switch r.Kind {
	case InteractionPermission:
		if len(r.Options) == 0 {
			return invalidArgument("missing_permission_options", "permission request requires options")
		}
		if r.Message != "" || r.URL != "" || r.RequestedSchema != nil {
			return invalidArgument(
				"invalid_permission_fields",
				"permission request contains fields for another interaction kind",
			)
		}
		if err := validatePermissionOptions(r.Options); err != nil {
			return err
		}
		return validateOptionalContent(r.Content)
	case InteractionForm:
		if r.Message == "" {
			return invalidArgument("missing_form_message", "form request requires a message")
		}
		if r.RequestedSchema == nil {
			return invalidArgument("missing_form_schema", "form request requires a schema")
		}
		if r.ToolCallID != "" || r.Title != "" || r.Options != nil || r.Content != nil || r.URL != "" {
			return invalidArgument(
				"invalid_form_fields",
				"form request contains fields for another interaction kind",
			)
		}
		if err := validateJSONValue(r.RequestedSchema); err != nil {
			return invalidArgument("invalid_form_schema", "form schema is not JSON encodable")
		}
		return nil
	case InteractionURL:
		if r.Message == "" || r.URL == "" {
			return invalidArgument("missing_url_request_fields", "URL request requires a message and URL")
		}
		if r.ToolCallID != "" || r.Title != "" || r.Options != nil || r.Content != nil ||
			r.RequestedSchema != nil {
			return invalidArgument(
				"invalid_url_fields",
				"URL request contains fields for another interaction kind",
			)
		}
		return nil
	default:
		return invalidArgument("unsupported_interaction_kind", "interaction kind is not supported")
	}
}

type Answer interface {
	answerKind() InteractionKind
}

type PermissionOutcome string

const (
	PermissionSelected  PermissionOutcome = "selected"
	PermissionCancelled PermissionOutcome = "cancelled" //nolint:misspell // Native contract spelling.
)

type PermissionAnswer struct {
	Outcome  PermissionOutcome
	OptionID string
}

func (PermissionAnswer) answerKind() InteractionKind { return InteractionPermission }

type FormOutcome string

const (
	FormAccept  FormOutcome = "accept"
	FormDecline FormOutcome = "decline"
	FormCancel  FormOutcome = "cancel"
)

type FormAnswer struct {
	Outcome FormOutcome
	Content map[string]any
}

func (FormAnswer) answerKind() InteractionKind { return InteractionForm }

type URLOutcome string

const (
	URLAccept  URLOutcome = "accept"
	URLDecline URLOutcome = "decline"
	URLCancel  URLOutcome = "cancel"
)

type URLAnswer struct {
	Outcome URLOutcome
}

func (URLAnswer) answerKind() InteractionKind { return InteractionURL }

// MatchAnswer returns answer as one of the concrete answer values after
// checking that it matches the live request kind and that its outcome has the
// fields required by that branch.
func MatchAnswer(kind InteractionKind, answer Answer) (Answer, error) {
	concrete, err := concreteAnswer(answer)
	if err != nil {
		return nil, err
	}
	if concrete.answerKind() != kind {
		return nil, invalidArgument(
			"answer_kind_mismatch",
			"answer does not match interaction kind",
		)
	}

	switch value := concrete.(type) {
	case PermissionAnswer:
		switch value.Outcome {
		case PermissionSelected:
			if value.OptionID == "" {
				return nil, invalidArgument(
					"missing_permission_option_id",
					"selected permission answer requires an option ID",
				)
			}
		case PermissionCancelled:
			if value.OptionID != "" {
				return nil, invalidArgument(
					"unexpected_permission_option_id",
					"canceled permission answer must not select an option",
				)
			}
		default:
			return nil, invalidArgument(
				"invalid_permission_outcome",
				"permission answer outcome is not supported",
			)
		}
	case FormAnswer:
		switch value.Outcome {
		case FormAccept:
			if value.Content == nil {
				return nil, invalidArgument(
					"missing_form_answer",
					"accepted form answer requires content",
				)
			}
			if err := validateJSONValue(value.Content); err != nil {
				return nil, invalidArgument(
					ErrorReasonInvalidFormAnswer,
					"form answer content is not JSON encodable",
				)
			}
		case FormDecline, FormCancel:
			if value.Content != nil {
				return nil, invalidArgument(
					"unexpected_form_answer",
					"declined or canceled form answer must not contain content",
				)
			}
		default:
			return nil, invalidArgument(
				ErrorReasonInvalidFormOutcome,
				"form answer outcome is not supported",
			)
		}
	case URLAnswer:
		switch value.Outcome {
		case URLAccept, URLDecline, URLCancel:
		default:
			return nil, invalidArgument(
				"invalid_url_outcome",
				"URL answer outcome is not supported",
			)
		}
	default:
		return nil, invalidArgument(ErrorReasonUnsupportedAnswer, "answer kind is not supported")
	}
	return concrete, nil
}

// ValidateAnswer checks that answer matches the live request kind and that its
// outcome has the fields required by that branch.
func ValidateAnswer(kind InteractionKind, answer Answer) error {
	_, err := MatchAnswer(kind, answer)
	return err
}

func concreteAnswer(answer Answer) (Answer, error) {
	if answer == nil {
		return nil, invalidArgument(ErrorReasonMissingAnswer, "answer is required")
	}
	switch value := answer.(type) {
	case PermissionAnswer, FormAnswer, URLAnswer:
		return answer, nil
	case *PermissionAnswer:
		if value != nil {
			return *value, nil
		}
	case *FormAnswer:
		if value != nil {
			return *value, nil
		}
	case *URLAnswer:
		if value != nil {
			return *value, nil
		}
	default:
		// Embedded Answer interfaces can promote answerKind even when nil.
		// Reject wrappers without invoking a method on caller-owned values.
		reflected := reflect.ValueOf(answer)
		if reflected.Kind() != reflect.Pointer || !reflected.IsNil() {
			return nil, invalidArgument(ErrorReasonUnsupportedAnswer, "answer kind is not supported")
		}
	}
	return nil, invalidArgument(ErrorReasonMissingAnswer, "answer is required")
}

type InteractionOutcome string

const (
	InteractionAnswered  InteractionOutcome = "answered"
	InteractionCompleted InteractionOutcome = "completed"
	InteractionCancelled InteractionOutcome = "cancelled" //nolint:misspell // Published event value.
	InteractionExpired   InteractionOutcome = "expired"
	InteractionFailed    InteractionOutcome = "failed"
)

func validatePermissionOptions(options []PermissionOption) error {
	seen := make(map[string]struct{}, len(options))
	hasCancel := false
	for _, option := range options {
		if option.OptionID == "" {
			return invalidArgument("missing_permission_option_id", "permission option ID is required")
		}
		if _, exists := seen[option.OptionID]; exists {
			return invalidArgument("duplicate_permission_option_id", "permission option IDs must be unique")
		}
		seen[option.OptionID] = struct{}{}

		switch option.Kind {
		case PermissionAllow, PermissionReject:
			if !validPermissionScope(option.Scope) {
				return invalidArgument("invalid_permission_option_scope", "allow and reject require a supported scope")
			}
		case PermissionCancel:
			if hasCancel {
				return invalidArgument("duplicate_permission_cancel", "permission request allows only one cancel option")
			}
			hasCancel = true
			if option.Scope != "" {
				return invalidArgument("unexpected_permission_option_scope", "cancel option cannot have a scope")
			}
		case PermissionAllowOnce, PermissionAllowAlways, PermissionRejectOnce, PermissionRejectAlways:
			if option.Scope != "" {
				return invalidArgument("unexpected_permission_option_scope", "historical permission options cannot have a scope")
			}
			continue
		default:
			return invalidArgument("invalid_permission_option_kind", "permission option kind is not supported")
		}
		if option.Name == "" {
			return invalidArgument("missing_permission_option_name", "permission option name is required")
		}
	}
	return nil
}

func validPermissionScope(s PermissionScope) bool {
	switch s {
	case PermissionScopeOnce, PermissionScopeTurn, PermissionScopeSession, PermissionScopeAlways:
		return true
	default:
		return false
	}
}

func validateOptionalContent(content []ContentBlock) error {
	for _, block := range content {
		if err := block.Validate(); err != nil {
			return err
		}
	}
	return nil
}
