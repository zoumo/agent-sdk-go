package agent

import (
	"context"
	"fmt"
	"slices"
)

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

// capabilityNames is the canonical capability surface queried by Supports and
// Validate.
var capabilityNames = [...]string{
	CapabilityTextInput,
	CapabilityImageInput,
	CapabilityAudioInput,
	CapabilityResourceInput,
	CapabilityResourceLinkInput,
	CapabilityPermission,
	CapabilityForm,
	CapabilityURL,
}

var capabilitySet = func() map[string]struct{} {
	set := make(map[string]struct{}, len(capabilityNames))
	for _, capability := range capabilityNames {
		set[capability] = struct{}{}
	}
	return set
}()

// Capabilities describes the optional support that a provider or session
// actually has. Missing and false values both mean that support is unavailable.
type Capabilities map[string]bool

// Supports reports whether capability is both defined by this contract and
// explicitly true. Unknown keys never imply support.
func (c Capabilities) Supports(capability string) bool {
	if _, declared := capabilitySet[capability]; !declared {
		return false
	}
	return c[capability]
}

// Validate rejects capability keys that are not declared by this contract.
func (c Capabilities) Validate() error {
	var unknown []string
	for capability := range c {
		if _, declared := capabilitySet[capability]; !declared {
			unknown = append(unknown, capability)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	if len(unknown) == 1 {
		return invalidArgument(
			"unknown_capability",
			fmt.Sprintf("unknown capability %q", unknown[0]),
		)
	}
	return invalidArgument(
		"unknown_capability",
		fmt.Sprintf("unknown capabilities %q", unknown),
	)
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
