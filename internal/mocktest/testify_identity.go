package mocktest

import (
	"errors"
	"fmt"
	"go/version"
	"runtime"

	agenttestify "github.com/zoumo/agent-sdk-testify"
)

const (
	testifyModule         = "github.com/zoumo/agent-sdk-testify"
	testifyModuleVersion  = "v0.4.1-0.20261125153028-ec0d525e6731"
	testifyModuleSum      = "h1:fegRemza8vNFIrUgqRQ6bqxXK1+lnCLDw5D+ACYY3EY="
	testifyModuleRevision = "ec0d525e6731e9d9d658d9e440a6843b1451e100"
	testifyModuleTime     = "2026-11-25T15:30:28Z"
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

func validateEmbeddedTestifyIdentity(tool agenttestify.ToolIdentity) error {
	want := embeddedTestifyIdentity()
	switch {
	case tool.Source != agenttestify.ToolSourceEmbedded:
		return fmt.Errorf("embedded testify identity required, got %q", tool.Source)
	case tool.Module != testifyModule:
		return fmt.Errorf("embedded testify identity module %q, want %q", tool.Module, testifyModule)
	case tool.Version != want.Version:
		return fmt.Errorf("embedded testify identity version %q, want %q", tool.Version, want.Version)
	case tool.ModuleSum != want.Sum:
		return fmt.Errorf("embedded testify identity module sum %q, want %q", tool.ModuleSum, want.Sum)
	case tool.ModuleRevision != "" && tool.ModuleRevision != want.Revision:
		return fmt.Errorf("embedded testify identity module revision %q, want %q", tool.ModuleRevision, want.Revision)
	case tool.ModuleTime != "" && tool.ModuleTime != want.Time:
		return fmt.Errorf("embedded testify identity module time %q, want %q", tool.ModuleTime, want.Time)
	case !version.IsValid(tool.GoVersion):
		return fmt.Errorf("embedded testify identity has invalid Go version %q", tool.GoVersion)
	case tool.Command != "" || len(tool.Args) != 0 || tool.SHA256 != "":
		return errors.New("embedded testify identity declares executable fields")
	}
	return nil
}
