package main

import (
	"errors"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

type command struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Env     []string `json:"-"`
	Dir     string   `json:"-"`
}

type toolIdentity struct {
	Source         string `json:"source"`
	Version        string `json:"version"`
	Module         string `json:"module"`
	ModuleSum      string `json:"moduleSum"`
	ModuleRevision string `json:"moduleRevision"`
	ModuleTime     string `json:"moduleTime"`
	GoVersion      string `json:"goVersion"`
}

func discoverTool() (toolIdentity, error) {
	identity := mocktest.EmbeddedIdentity()
	if identity == nil || identity.Version == "" || identity.Sum == "" || identity.Revision == "" {
		return toolIdentity{}, errors.New("embedded agent-sdk-testify identity is incomplete")
	}
	return toolIdentity{
		Source:         "embedded",
		Version:        identity.Version,
		Module:         "github.com/zoumo/agent-sdk-testify",
		ModuleSum:      identity.Sum,
		ModuleRevision: identity.Revision,
		ModuleTime:     identity.Time,
		GoVersion:      identity.GoVersion,
	}, nil
}
