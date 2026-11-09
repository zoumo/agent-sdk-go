package mocktest

import (
	"runtime"

	agenttestify "github.com/zoumo/agent-sdk-testify"
)

const (
	testifyModuleVersion  = "v0.0.0-20261109044915-b606272c88c1"
	testifyModuleSum      = "h1:PGGn9abQro2aCssr/tElOCkSHEg6wwNpXEhD5pPn8Ac="
	testifyModuleRevision = "b606272c88c12e113ef084dc12e0f3dcb22a8050"
	testifyModuleTime     = "2026-11-09T04:49:15Z"
)

func embeddedTestifyIdentity() *agenttestify.EmbeddedIdentity {
	return &agenttestify.EmbeddedIdentity{
		Version:   testifyModuleVersion,
		Sum:       testifyModuleSum,
		Revision:  testifyModuleRevision,
		Time:      testifyModuleTime,
		GoVersion: runtime.Version(),
	}
}

// EmbeddedIdentity returns a clone of the exact linked testify module identity
// used by SDK-owned test and simulation hosts.
func EmbeddedIdentity() *agenttestify.EmbeddedIdentity {
	identity := embeddedTestifyIdentity()
	return &agenttestify.EmbeddedIdentity{
		Version:   identity.Version,
		Sum:       identity.Sum,
		Revision:  identity.Revision,
		Time:      identity.Time,
		GoVersion: identity.GoVersion,
	}
}
