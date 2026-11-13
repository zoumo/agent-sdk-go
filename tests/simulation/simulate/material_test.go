package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	agenttestify "github.com/zoumo/agent-sdk-testify"
)

func TestPortableExportBindsManifestAndHistory(t *testing.T) {
	tool, err := discoverTool()
	require.NoError(t, err)
	for _, mutation := range []string{"none", "raw", "manifest", "extra file", "missing history", "missing descriptor", "empty descriptor"} {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			session, err := prepareSession(t.Context(), tool,
				sourceSelection{Provider: "claude", Case: "SDK-CASE-033"},
				filepath.Join(root, "session"), "preserve")
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, cleanupPreparedSession(session.Dir)) })
			exportRoot := filepath.Join(root, "export")
			require.NoError(t, agenttestify.Export(agenttestify.ExportOptions{
				FixtureDir: session.Dir, Output: exportRoot,
			}))
			pkg, err := validateExport(exportRoot, session)
			require.NoError(t, err)
			require.Equal(t, 3, pkg.Material.FormatVersion)
			require.NotEmpty(t, pkg.SavedHistory)
			require.Equal(t, "report.json", pkg.ReportSource)
			require.NotEmpty(t, pkg.Report)
			switch mutation {
			case "none":
				return
			case "raw":
				require.NoError(t, os.WriteFile(filepath.Join(exportRoot, "raw", "raw.jsonl"), []byte("{}\n"), 0o600))
			case "manifest":
				require.NoError(t, os.WriteFile(filepath.Join(exportRoot, "manifest.json"), []byte("{}\n"), 0o600))
			case "extra file":
				require.NoError(t, os.WriteFile(filepath.Join(exportRoot, "extra.json"), []byte("{}\n"), 0o600))
			case "missing history":
				for path := range pkg.SavedHistory {
					require.NoError(t, os.Remove(filepath.Join(exportRoot, path)))
					break
				}
			case "missing descriptor":
				require.NoError(t, os.Remove(filepath.Join(exportRoot, "material.json")))
			case "empty descriptor":
				require.NoError(t, os.WriteFile(filepath.Join(exportRoot, "material.json"), nil, 0o600))
			}
			_, err = validateExport(exportRoot, session)
			require.Error(t, err)
		})
	}
}

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
