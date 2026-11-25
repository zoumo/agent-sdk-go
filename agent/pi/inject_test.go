package agentpi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentpi "github.com/zoumo/agent-sdk-go/agent/pi"
)

func TestPiInjectorReceipts(t *testing.T) {
	executable, err := os.Executable()
	require.NoError(t, err)
	input := agent.Input{Content: []agent.ContentBlock{
		{Text: &agent.ContentBlockText{Text: " first\n/command   next "}},
		{Image: &agent.ContentBlockImage{Data: "eA==", MIMEType: "image/png"}},
	}}
	for _, tc := range []struct {
		name     string
		code     agent.ErrorCode
		accepted *bool
	}{
		{name: "accepted", accepted: new(true)},
		{name: "native-rejected", code: agent.ErrorNativeRejected, accepted: new(false)},
		{name: "unknown-disposition", code: agent.ErrorOutcomeUnknown},
		{name: "extension-error", accepted: new(true)},
		{name: "close-pending", code: agent.ErrorOutcomeUnknown},
		{name: "missing-inject-command", code: agent.ErrorUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			core := agentpi.New(agentpi.Config{
				CLIPath: executable, Args: []string{"-test.run=^TestPiInjectPeer$", "--"},
				Env: []string{"SDK_PI_INJECT_PEER=" + tc.name}, EnableInject: true,
			})
			_, err := core.Start(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { _ = core.Stop(context.Background()) })
			session, err := core.Open(ctx, agent.OpenOptions{Cwd: t.TempDir()})
			if tc.name == "missing-inject-command" {
				require.Equal(t, tc.code, agent.CodeOf(err))
				require.Nil(t, session)
				return
			}
			require.NoError(t, err)
			t.Cleanup(func() { _ = session.Close(context.Background()) })
			injector, ok := session.(agent.Injector)
			require.True(t, ok)
			stream, err := session.Events()
			require.NoError(t, err)

			audio := agent.Input{Content: []agent.ContentBlock{{
				Audio: &agent.ContentBlockAudio{Data: "eA==", MIMEType: "audio/wav"},
			}}}
			receipt, err := injector.Inject(ctx, audio)
			require.Equal(t, agent.ErrorUnsupported, agent.CodeOf(err))
			assert.Equal(t, new(false), receipt.Acceptance.Sent)
			assert.Nil(t, receipt.Acceptance.NativeAccepted)

			canceled, stop := context.WithCancel(ctx)
			stop()
			receipt, err = injector.Inject(canceled, input)
			require.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, new(false), receipt.Acceptance.Sent)

			if tc.name == "close-pending" {
				type result struct {
					receipt agent.InjectReceipt
					err     error
				}
				results := make(chan result, 1)
				go func() {
					receipt, err := injector.Inject(ctx, input)
					results <- result{receipt: receipt, err: err}
				}()
				event, err := stream.Next(ctx)
				require.NoError(t, err)
				raw, ok := event.Payload.(agent.UnsupportedNativeEventPayload)
				require.True(t, ok)
				require.JSONEq(t, `{"type":"fixture_received"}`, string(raw.Raw))
				busy, err := injector.Inject(ctx, input)
				require.Equal(t, agent.ErrorFailedPrecondition, agent.CodeOf(err))
				assert.Equal(t, new(false), busy.Acceptance.Sent)
				require.NoError(t, session.Close(ctx))
				got := <-results
				require.Equal(t, tc.code, agent.CodeOf(got.err))
				assert.Equal(t, new(true), got.receipt.Acceptance.Sent)
				assert.Nil(t, got.receipt.Acceptance.NativeAccepted)
			} else {
				got, err := injector.Inject(ctx, input)
				require.Equal(t, tc.code, agent.CodeOf(err))
				assert.Equal(t, new(true), got.Acceptance.Sent)
				assert.Equal(t, tc.accepted, got.Acceptance.NativeAccepted)
				if tc.name == "extension-error" {
					event, err := stream.Next(ctx)
					require.NoError(t, err)
					failure, ok := event.Payload.(agent.SessionErrorPayload)
					require.True(t, ok)
					assert.Equal(t, "pi_extension_send_message_failed", failure.Reason)
					event, err = stream.Next(ctx)
					require.NoError(t, err)
					native, ok := event.Payload.(agent.UnsupportedNativeEventPayload)
					require.True(t, ok)
					assert.Contains(t, string(native.Raw), "test append failure")
				}
				require.NoError(t, session.Close(ctx))
			}
			closed, err := injector.Inject(ctx, input)
			require.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
			assert.Equal(t, new(false), closed.Acceptance.Sent)
		})
	}
}

func TestPiInjectPeer(t *testing.T) {
	outcome := os.Getenv("SDK_PI_INJECT_PEER")
	if outcome == "" {
		return
	}
	if err := servePiInjectPeer(outcome); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func servePiInjectPeer(outcome string) error {
	index := slices.Index(os.Args, "-e")
	if index < 0 || index+1 >= len(os.Args) {
		return errors.New("missing Pi injection extension")
	}
	extPath := os.Args[index+1]
	scanner := bufio.NewScanner(os.Stdin)
	writer := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request struct {
			ID       string  `json:"id"`
			Type     string  `json:"type"`
			Message  string  `json:"message"`
			Behavior *string `json:"streamingBehavior"`
			Images   *[]any  `json:"images"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return err
		}
		reply := map[string]any{"type": "response", "id": request.ID, "command": request.Type, "success": true}
		switch request.Type {
		case "get_state":
			reply["data"] = map[string]string{"sessionId": "injection-test"}
		case "get_commands":
			names := []string{"agent-sdk-session-ready"}
			if outcome != "missing-inject-command" {
				names = append(names, "agent-sdk-inject")
			}
			commands := make([]map[string]any, 0, len(names))
			for _, name := range names {
				commands = append(commands, map[string]any{
					"name": name, "source": "extension", "sourceInfo": map[string]string{"path": extPath},
				})
			}
			reply["data"] = map[string]any{"commands": commands}
		case "prompt":
			payload, ok := strings.CutPrefix(request.Message, "/agent-sdk-inject ")
			if !ok || request.Behavior != nil || request.Images != nil {
				return errors.New("injection was not a context-only extension command")
			}
			var blocks []map[string]string
			if err := json.Unmarshal([]byte(payload), &blocks); err != nil {
				return err
			}
			if len(blocks) != 2 || blocks[0]["type"] != "text" || blocks[0]["text"] != " first\n/command   next " ||
				blocks[1]["type"] != "image" || blocks[1]["mimeType"] != "image/png" || blocks[1]["data"] != "eA==" {
				return errors.New("native injection did not preserve structured content")
			}
			reply["data"] = map[string]string{"disposition": "handled"}
			switch outcome {
			case "native-rejected":
				delete(reply, "data")
				reply["success"], reply["error"] = false, "test native refusal"
			case "unknown-disposition":
				reply["data"] = map[string]string{"disposition": "started"}
			case "extension-error":
				if err := writer.Encode(map[string]any{
					"type": "extension_error", "event": "send_message", "extensionPath": "<runtime>", "error": "test append failure",
				}); err != nil {
					return err
				}
			case "close-pending":
				if err := writer.Encode(map[string]string{"type": "fixture_received"}); err != nil {
					return err
				}
				continue
			}
		default:
			return fmt.Errorf("unexpected command %q", request.Type)
		}
		if err := writer.Encode(reply); err != nil {
			return err
		}
	}
	return scanner.Err()
}
