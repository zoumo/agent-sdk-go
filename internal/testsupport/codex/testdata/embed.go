// Package fixturedata embeds independently authored synthetic integration inputs.
// Native SDK runtime packages do not depend on this package.
package fixturedata

import "embed"

// Files contains synthetic protocol inputs; none is a provider recording.
//
//go:embed integration
var Files embed.FS
