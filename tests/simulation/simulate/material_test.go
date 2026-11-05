package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAssertionReportPrefersOfficialVerification(t *testing.T) {
	root := t.TempDir()
	fallback := []byte(`{"captureStatus":"complete"}`)
	expected := []byte(`{"receipts":[{"get_session_messages":[{"type":"user"}]}]}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "official-replay"), 0o700))
	writeJSONTest(t, filepath.Join(root, "official-replay", "verification.json"), map[string]any{
		"report": json.RawMessage(expected),
	})

	report, source, err := assertionReport(root, fallback)
	require.NoError(t, err)
	require.Equal(t, expected, report)
	require.Equal(t, "official-replay/verification.json#/report", source)
}

func TestAssertionReportFallsBackToExportReport(t *testing.T) {
	for name, verification := range map[string]any{
		"missing verification": nil,
		"null verification":    map[string]any{"report": nil},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			fallback := []byte(`{"receipts":[{"get_session_messages":[]}]}`)
			if verification != nil {
				require.NoError(t, os.MkdirAll(filepath.Join(root, "official-replay"), 0o700))
				writeJSONTest(t, filepath.Join(root, "official-replay", "verification.json"), verification)
			}
			report, source, err := assertionReport(root, fallback)
			require.NoError(t, err)
			require.Equal(t, fallback, report)
			require.Equal(t, "report.json", source)
		})
	}
}
