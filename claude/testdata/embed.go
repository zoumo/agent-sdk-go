// Package fixturedata embeds sanitized recordings and explicitly synthetic fixtures
// for offline tooling. Native SDK runtime packages do not depend on this package.
package fixturedata

import "embed"

// Files contains the single published copy of offline fixture evidence.
//
//go:embed recordings synthetic-faults
var Files embed.FS
