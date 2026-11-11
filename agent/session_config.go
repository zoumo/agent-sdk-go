package agent

import (
	"context"
	"strings"
	"unicode/utf8"
)

// SessionConfig contains provider-native configuration for a main session. An
// empty field is omitted and is not a request to clear or restore a default.
// Nonempty values are passed literally without trimming, aliasing or
// cross-provider conversion.
type SessionConfig struct {
	Model  string
	Effort string
}

// Validate checks session configuration before native side effects. Empty
// values are valid because they omit the option.
func (c SessionConfig) Validate() error {
	if err := validateConfigValue(ConfigModel, c.Model); err != nil {
		return err
	}
	return validateConfigValue(ConfigEffort, c.Effort)
}

func validateConfigValue(
	id ConfigID,
	value string,
) error {
	if value == "" {
		return nil
	}
	name := "configuration value"
	switch id {
	case ConfigModel:
		name = "model"
	case ConfigEffort:
		name = "reasoning effort"
	}
	if !utf8.ValidString(value) {
		return &Error{
			Code:    ErrorInvalidArgument,
			Reason:  ErrorReasonConfigValueInvalidUTF8,
			Message: name + " must be valid UTF-8",
		}
	}
	if strings.ContainsRune(value, '\x00') {
		return &Error{
			Code:    ErrorInvalidArgument,
			Reason:  ErrorReasonConfigValueContainsNUL,
			Message: name + " must not contain NUL",
		}
	}
	return nil
}

// ConfigID identifies one provider-neutral runtime session configuration value.
type ConfigID string

const (
	ConfigModel  ConfigID = "model"
	ConfigEffort ConfigID = "effort"
)

// ConfigChange requests one literal value update for one configuration ID.
type ConfigChange struct {
	ID    ConfigID
	Value string
}

// Validate checks a runtime configuration change before native side effects.
// Empty runtime values are invalid; they do not clear or restore a default.
func (c ConfigChange) Validate() error {
	switch c.ID {
	case ConfigModel, ConfigEffort:
	default:
		return &Error{
			Code:    ErrorInvalidArgument,
			Reason:  ErrorReasonConfigIDUnknown,
			Message: "configuration ID is not supported",
		}
	}
	if c.Value == "" {
		return &Error{
			Code:    ErrorInvalidArgument,
			Reason:  ErrorReasonConfigValueEmpty,
			Message: "configuration value must be nonempty",
		}
	}
	return validateConfigValue(c.ID, c.Value)
}

// ConfigReceipt reports the change requested by this call and the native
// acceptance stages observed for it. Change echoes the request for every
// result, including invalid and closed-session failures; Acceptance.Sent
// distinguishes a change sent to the provider from one rejected before a
// native write. It does not claim that a configuration value is currently
// effective.
type ConfigReceipt struct {
	Change     ConfigChange
	Acceptance Acceptance
}

// ConfigController updates one main-session configuration value at a time.
// Updates affect provider-determined later work; they do not wait for idle,
// interrupt active work, restart the process, retry, or configure child
// sessions. Callers that require ordering must serialize calls.
type ConfigController interface {
	SetConfig(
		ctx context.Context,
		change ConfigChange,
	) (ConfigReceipt, error)
}
