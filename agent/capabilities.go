package agent

import "context"

const (
	CapabilityTextInput         = "textInput"
	CapabilityImageInput        = "imageInput"
	CapabilityAudioInput        = "audioInput"
	CapabilityResourceInput     = "resourceInput"
	CapabilityResourceLinkInput = "resourceLinkInput"
	CapabilityPermission        = "permission"
	CapabilityForm              = "form"
	CapabilityURL               = "url"
)

// Capabilities describes the optional support that a provider or session
// actually has. Missing and false values both mean that support is unavailable.
type Capabilities map[string]bool

// Supports reports whether capability is both defined by this contract and
// explicitly true. Unknown keys never imply support.
func (c Capabilities) Supports(capability string) bool {
	switch capability {
	case CapabilityTextInput,
		CapabilityImageInput,
		CapabilityAudioInput,
		CapabilityResourceInput,
		CapabilityResourceLinkInput,
		CapabilityPermission,
		CapabilityForm,
		CapabilityURL:
		return c[capability]
	default:
		return false
	}
}

// ActionReceipt records the acceptance observed for a provider-specific action.
type ActionReceipt struct {
	Acceptance Acceptance
}

// Archiver is implemented only by sessions that can archive and unarchive
// their native main session.
type Archiver interface {
	Archive(ctx context.Context) (ActionReceipt, error)
	Unarchive(ctx context.Context) (ActionReceipt, error)
}

// ConfigurationReceipt identifies a requested native model and records the
// acceptance stages observed for the change.
type ConfigurationReceipt struct {
	Model      string
	Acceptance Acceptance
}

// ModelController is implemented only by sessions that support an explicit
// native model change.
type ModelController interface {
	SetModel(ctx context.Context, model string) (ConfigurationReceipt, error)
}
