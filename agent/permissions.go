package agent

// PermissionPreset selects the approval and sandbox settings for one session
// open or resume. Each adapter maps the preset to its native controls.
type PermissionPreset string

const (
	PermissionPresetManualReview PermissionPreset = "manualReview"
	PermissionPresetAutoReview   PermissionPreset = "autoReview"
	PermissionPresetFullAccess   PermissionPreset = "fullAccess"

	// DefaultPermissionPreset applies to unified Core.Open and Core.Resume when the preset is omitted.
	DefaultPermissionPreset = PermissionPresetFullAccess
)
