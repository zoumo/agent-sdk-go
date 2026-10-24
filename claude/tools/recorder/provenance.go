package recorder

import (
	"fmt"
	"regexp"
	"runtime/debug"
)

type revisionEvidence struct {
	Revision, Source string
	Modified         *bool
}

var fullRevision = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Build metadata can certify only a clean checkout. A caller assertion remains
// explicitly unverified when the binary has no VCS metadata.
func recordingRevision(info *debug.BuildInfo, explicit string) (revisionEvidence, error) {
	var revision string
	clean := false
	if info != nil && info.Main.Path == "github.com/zoumo/agent-sdk-go" {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				if setting.Value == "true" {
					return revisionEvidence{}, fmt.Errorf("recorder: SDK build source is modified; build from a clean commit")
				}
				clean = setting.Value == "false"
			}
		}
	}
	if explicit != "" && !fullRevision.MatchString(explicit) {
		return revisionEvidence{}, fmt.Errorf("recorder: sdk-revision must be a full lowercase commit SHA")
	}
	if revision != "" {
		if !clean || !fullRevision.MatchString(revision) {
			return revisionEvidence{}, fmt.Errorf("recorder: build source is modified or unverified; build from a clean commit")
		}
		if explicit != "" && explicit != revision {
			return revisionEvidence{}, fmt.Errorf("recorder: sdk-revision conflicts with build VCS evidence")
		}
		unmodified := false
		return revisionEvidence{Revision: revision, Source: "go-build-vcs", Modified: &unmodified}, nil
	}
	if explicit == "" {
		return revisionEvidence{}, fmt.Errorf("recorder: build has no source revision; supply sdk-revision (recorded as unverified)")
	}
	return revisionEvidence{Revision: explicit, Source: "caller-supplied-unverified"}, nil
}
