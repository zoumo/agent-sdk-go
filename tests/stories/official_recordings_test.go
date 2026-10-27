package stories_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zoumo/agent-sdk-go/claude"
	claudeMock "github.com/zoumo/agent-sdk-go/claude/tools/mock"
	clt "github.com/zoumo/agent-sdk-go/claude/types"
	"github.com/zoumo/agent-sdk-go/codex"
	codexMock "github.com/zoumo/agent-sdk-go/codex/tools/mock"
	ct "github.com/zoumo/agent-sdk-go/codex/types"
	privateRecording "github.com/zoumo/agent-sdk-go/internal/recording"
)

// This optional check consumes private official-Python-SDK captures offline. It
// never discovers a CLI, HOME, account, or credential. Default CI has no source.
func TestOfficialPythonRecordings(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			root := os.Getenv("SDK_OFFICIAL_" + map[string]string{"codex": "CODEX", "claude": "CLAUDE"}[provider] + "_ROOT")
			if root == "" {
				t.Skip("private official SDK capture not selected; offline opt-in only")
			}
			var manifest struct {
				Files          map[string]string `json:"files"`
				CredentialScan string            `json:"credentialScan"`
			}
			original, err := os.ReadFile(filepath.Join(root, "manifest.json"))
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(original, &manifest))
			require.Equal(t, "pass", manifest.CredentialScan)
			original, err = os.ReadFile(filepath.Join(root, "raw/raw.jsonl"))
			require.NoError(t, err)
			sum := sha256.Sum256(original)
			require.Equal(t, manifest.Files["raw.jsonl"], hex.EncodeToString(sum[:]))
			frames, err := privateRecording.ReadFrames(filepath.Join(root, "raw/raw.jsonl"))
			require.NoError(t, err)
			var plan struct {
				Version     int                               `json:"version"`
				Story       string                            `json:"story"`
				OfficialSDK struct{ Package, Version string } `json:"officialSDK"`
			}
			b, err := os.ReadFile(filepath.Join(root, "plan.json"))
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(b, &plan))
			require.Equal(t, 2, plan.Version)
			require.Equal(t, "SDK-US-001", plan.Story)
			require.Equal(t, map[string]string{"codex": "openai-codex", "claude": "claude-agent-sdk"}[provider], plan.OfficialSDK.Package)
			require.Equal(t, map[string]string{"codex": "0.161.0", "claude": "0.2.174"}[provider], plan.OfficialSDK.Version)
			temp := t.TempDir()
			scenario := filepath.Join(temp, "scenario.json")
			complete := filepath.Join(temp, "complete")
			var document any
			if provider == "codex" {
				document = map[string]any{"version": 1, "provenance": "recording", "source": filepath.Join(root, "manifest.json"), "frames": frames}
			} else {
				native := make([]claudeMock.Frame, 0, len(frames))
				for _, f := range frames {
					wire := f.Wire
					if f.Direction == "c2s" {
						var value map[string]json.RawMessage
						require.NoError(t, json.Unmarshal(wire, &value))
						var typ string
						require.NoError(t, json.Unmarshal(value["type"], &typ))
						switch typ {
						case "control_request":
							// SDKs request different initialization capabilities. Require the exact
							// reviewed Python envelope before constructing the explicit Go request.
							require.JSONEq(t, `{"subtype":"initialize","hooks":null}`, string(value["request"]))
							value["request"] = json.RawMessage(`{"subtype":"initialize","hooks":null,"forwardSubagentText":true}`)
						case "user":
							// Both are native ownership markers, never routing to saved projects.
							require.JSONEq(t, `"default"`, string(value["session_id"]))
							value["session_id"] = json.RawMessage(`""`)
						default:
							t.Fatalf("unsupported official SDK client frame type %q", typ)
						}
						wire, err = json.Marshal(value)
						require.NoError(t, err)
					}
					direction := map[string]string{"c2s": "client", "s2c": "server"}[f.Direction]
					native = append(native, claudeMock.Frame{Ordinal: f.Ordinal, Direction: direction, Channel: f.Channel, Data: wire})
				}
				// Derived expectations are explicit synthetic adaptation; native server
				// bytes and original source files are unchanged and are never relabeled.
				document = map[string]any{"version": 1, "provenance": "synthetic", "source": filepath.Join(root, "manifest.json") + "; explicit Go initialize/stream-marker adaptation", "frames": native}
			}
			data, err := json.Marshal(document)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(scenario, data, 0600))
			exe, err := os.Executable()
			require.NoError(t, err)
			env := []string{"PATH=/usr/bin:/bin", "SDK_OFFICIAL_PEER=" + provider, "SDK_OFFICIAL_SCENARIO=" + scenario, "SDK_OFFICIAL_COMPLETE=" + complete}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if provider == "codex" {
				var initialize ct.InitializeParams
				var thread ct.ThreadStartParams
				for _, f := range frames {
					if f.Direction != "c2s" {
						continue
					}
					var w struct {
						Method string
						Params json.RawMessage
					}
					require.NoError(t, json.Unmarshal(f.Wire, &w))
					switch w.Method {
					case "initialize":
						require.NoError(t, json.Unmarshal(w.Params, &initialize))
					case "thread/start":
						require.NoError(t, json.Unmarshal(w.Params, &thread))
					}
				}
				session, err := codex.Open(ctx, codex.SessionOptions{Stdio: &codex.StdioOptions{Executable: exe, Env: env, Initialize: initialize}, Thread: thread})
				require.NoError(t, err)
				defer session.Close()
				turns := 0
				for _, f := range frames {
					if f.Direction != "c2s" {
						continue
					}
					var w struct {
						Method string
						Params json.RawMessage
					}
					require.NoError(t, json.Unmarshal(f.Wire, &w))
					if w.Method != "turn/start" {
						continue
					}
					var params ct.TurnStartParams
					require.NoError(t, json.Unmarshal(w.Params, &params))
					_, err := session.Submit(ctx, codex.Input{Input: params.Input})
					require.NoError(t, err)
					for {
						event, err := session.Receive(ctx)
						require.NoError(t, err)
						require.Nil(t, event.Request)
						if event.Event != nil {
							require.NoError(t, event.Event.DecodeError)
							if event.Event.Method == "turn/completed" {
								turns++
								break
							}
						}
					}
				}
				require.Equal(t, 2, turns)
				require.NoError(t, session.Close())
			} else {
				session, err := claude.Open(ctx, claude.SessionOptions{CLIPath: exe, Env: env, InitializeTimeout: 5 * time.Second})
				require.NoError(t, err)
				defer session.Close()
				turns := 0
				for _, f := range frames {
					if f.Direction != "c2s" {
						continue
					}
					var w struct {
						Type    string
						Message struct{ Content string }
					}
					require.NoError(t, json.Unmarshal(f.Wire, &w))
					if w.Type != "user" {
						continue
					}
					_, err := session.Submit(ctx, claude.Input{Text: w.Message.Content})
					require.NoError(t, err)
					for {
						event, err := session.Receive(ctx)
						require.NoError(t, err)
						require.Nil(t, event.Request)
						if event.Event != nil && event.Event.Type == "result" {
							var result clt.ResultEvent
							require.NoError(t, json.Unmarshal(event.Raw, &result))
							require.False(t, result.IsError)
							turns++
							break
						}
					}
				}
				require.Equal(t, 2, turns)
				require.NoError(t, session.Close())
			}
			require.FileExists(t, complete, "Go mock requires full protocol consumption and EOF")
		})
	}
}

func officialRecordingPeer(provider string) int {
	path := os.Getenv("SDK_OFFICIAL_SCENARIO")
	code := 1
	if provider == "codex" {
		code = codexMock.Main(context.Background(), []string{"--scenario", path}, os.Stdin, os.Stdout, os.Stderr)
	} else if provider == "claude" {
		code = claudeMock.CLI(context.Background(), os.Args[1:], path, os.Stdin, os.Stdout, os.Stderr)
	}
	if code == 0 {
		if err := os.WriteFile(os.Getenv("SDK_OFFICIAL_COMPLETE"), []byte("full-consumption; no native process\n"), 0600); err != nil {
			fmt.Fprintln(os.Stderr, "completion receipt failed")
			return 1
		}
	}
	return code
}
