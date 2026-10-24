package recorder

import (
	"github.com/stretchr/testify/require"
	"runtime/debug"
	"testing"
)

func TestRecordingRevisionEvidence(t *testing.T) {
	const revision = "3f1776e4da94cc53d48e5029b20bd254dd727152"
	for _, tc := range []struct {
		name, explicit, vcs string
		modified            bool
		external            bool
		wantSource          string
		fail                bool
	}{
		{name: "no evidence", fail: true},
		{name: "invalid explicit", explicit: "HEAD", fail: true},
		{name: "caller assertion", explicit: revision, wantSource: "caller-supplied-unverified"},
		{name: "clean build", vcs: revision, wantSource: "go-build-vcs"},
		{name: "dirty build", vcs: revision, modified: true, fail: true},
		{name: "dirty caller cannot certify", vcs: revision, explicit: revision, modified: true, fail: true},
		{name: "external main without SDK evidence", external: true, vcs: revision, fail: true},
		{name: "external main caller assertion", external: true, vcs: revision, explicit: revision, wantSource: "caller-supplied-unverified"},
		{name: "dirty without revision", modified: true, explicit: revision, fail: true},
		{name: "conflicting evidence", vcs: revision, explicit: "0000000000000000000000000000000000000000", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &debug.BuildInfo{Main: debug.Module{Path: "github.com/zoumo/agent-sdk-go"}}
			if tc.external {
				info.Main.Path = "example.invalid/record-wrapper"
				info.Deps = []*debug.Module{{Path: "github.com/zoumo/agent-sdk-go", Version: "v0.0.0-unverified"}}
			}
			if tc.vcs != "" || tc.modified {
				info.Settings = []debug.BuildSetting{{Key: "vcs.revision", Value: tc.vcs}, {Key: "vcs.modified", Value: map[bool]string{true: "true", false: "false"}[tc.modified]}}
			}
			got, err := recordingRevision(info, tc.explicit)
			if tc.fail {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, revision, got.Revision)
			require.Equal(t, tc.wantSource, got.Source)
			if tc.wantSource == "go-build-vcs" {
				require.NotNil(t, got.Modified)
				require.False(t, *got.Modified)
			} else {
				require.Nil(t, got.Modified)
			}
		})
	}
}
