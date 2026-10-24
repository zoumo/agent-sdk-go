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
		wantSource          string
		fail                bool
	}{
		{name: "no evidence", fail: true},
		{name: "invalid explicit", explicit: "HEAD", fail: true},
		{name: "caller assertion", explicit: revision, wantSource: "caller-supplied-unverified"},
		{name: "clean build", vcs: revision, wantSource: "go-build-vcs"},
		{name: "dirty build", vcs: revision, modified: true, fail: true},
		{name: "dirty caller cannot certify", vcs: revision, explicit: revision, modified: true, fail: true},
		{name: "conflicting evidence", vcs: revision, explicit: "0000000000000000000000000000000000000000", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &debug.BuildInfo{}
			if tc.vcs != "" {
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
