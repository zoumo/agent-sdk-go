package simulate

import (
	"github.com/zoumo/agent-sdk-go/replay"
)

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
	identity, err := replay.BuildIdentity()
	if err != nil {
		return toolIdentity{}, err
	}
	return toolIdentity{
		Source:         "embedded",
		Version:        identity.Version,
		Module:         identity.Module,
		ModuleSum:      identity.ModuleSum,
		ModuleRevision: identity.ModuleRevision,
		ModuleTime:     identity.ModuleTime,
		GoVersion:      identity.GoVersion,
	}, nil
}
