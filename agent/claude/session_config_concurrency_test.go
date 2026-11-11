package agentclaude_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentclaude "github.com/zoumo/agent-sdk-go/agent/claude"
	"github.com/zoumo/agent-sdk-go/claude"
)

func TestClaudeSetConfigConcurrentCallsSettleTheirOwnChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	session, requests := openClaudeConfigConcurrencyPeer(t, ctx, false)
	changes := []agent.ConfigChange{
		{ID: agent.ConfigModel, Value: "accepted/model"},
		{ID: agent.ConfigModel, Value: "rejected/model"},
		{ID: agent.ConfigEffort, Value: "literal/effort"},
	}
	type result struct {
		change  agent.ConfigChange
		receipt agent.ConfigReceipt
		err     error
	}
	results := make(chan result, len(changes))
	start := make(chan struct{})
	for _, change := range changes {
		go func() {
			<-start
			receipt, err := session.(agent.ConfigController).SetConfig(ctx, change)
			results <- result{change, receipt, err}
		}()
	}
	close(start)
	for range changes {
		select {
		case result := <-results:
			assert.Equal(t, result.change, result.receipt.Change)
			if result.change.Value == "rejected/model" {
				require.Error(t, result.err)
				assert.Equal(t, agent.ErrorNativeRejected, agent.CodeOf(result.err))
				assert.Equal(t, agent.Acceptance{Sent: new(true), NativeAccepted: new(false)}, result.receipt.Acceptance)
			} else {
				require.NoError(t, result.err)
				requireAccepted(t, result.receipt.Acceptance)
			}
		case <-ctx.Done():
			t.Fatal("concurrent SetConfig calls did not settle")
		}
	}
	var observed []agent.ConfigChange
	for range changes {
		select {
		case change := <-requests:
			observed = append(observed, change)
		case <-ctx.Done():
			t.Fatal("native configuration request was lost")
		}
	}
	assert.ElementsMatch(t, changes, observed)
	assert.Empty(t, requests, "a configuration request was duplicated")
}

func TestClaudeSetConfigCloseRaceSettlesWithConsistentAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	session, requests := openClaudeConfigConcurrencyPeer(t, ctx, true)
	change := agent.ConfigChange{ID: agent.ConfigModel, Value: "pending/model"}
	type result struct {
		receipt agent.ConfigReceipt
		err     error
	}
	updated := make(chan result, 1)
	go func() {
		receipt, err := session.(agent.ConfigController).SetConfig(ctx, change)
		updated <- result{receipt, err}
	}()
	select {
	case got := <-requests:
		require.Equal(t, change, got)
	case <-ctx.Done():
		t.Fatal("SetConfig did not reach the native peer")
	}
	closed := make(chan error, 1)
	go func() { closed <- session.Close(ctx) }()
	select {
	case result := <-updated:
		require.Error(t, result.err)
		assert.Equal(t, change, result.receipt.Change)
		switch agent.CodeOf(result.err) {
		case agent.ErrorOutcomeUnknown:
			assert.Equal(t, agent.Acceptance{Sent: new(true)}, result.receipt.Acceptance)
		case agent.ErrorClosed:
			var dispatch *claude.DispatchError
			if errors.As(result.err, &dispatch) {
				assert.Equal(t, agent.Acceptance{Sent: new(dispatch.State != claude.NotSent)}, result.receipt.Acceptance)
			} else {
				assert.Equal(t, agent.Acceptance{Sent: new(false)}, result.receipt.Acceptance)
			}
		default:
			t.Errorf("unexpected close-race classification: %v", result.err)
		}
	case <-ctx.Done():
		t.Fatal("SetConfig hung during Close")
	}
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("Close hung during SetConfig")
	}
	// The other controlled phase is closure before dispatch.
	receipt, err := session.(agent.ConfigController).SetConfig(ctx, change)
	require.Equal(t, agent.ErrorClosed, agent.CodeOf(err))
	assert.Equal(t, change, receipt.Change)
	assert.Equal(t, agent.Acceptance{Sent: new(false)}, receipt.Acceptance)
	assert.Empty(t, requests)
}

// A synthetic protocol peer keeps model calls pending together and answers in
// reverse arrival order. Effort still goes through native settings preflight.
func openClaudeConfigConcurrencyPeer(t *testing.T, ctx context.Context, hold bool) (agent.Session, <-chan agent.ConfigChange) {
	t.Helper()
	cwd := t.TempDir()
	reader, output := io.Pipe()
	input, writer := io.Pipe()
	requests := make(chan agent.ConfigChange, 8)
	peerDone := make(chan error, 1)
	go func() {
		defer output.Close()
		var models []struct{ ID, Model string }
		send := func(id, result string, rejected bool) error {
			if rejected {
				_, err := fmt.Fprintf(output, "{\"type\":\"control_response\",\"response\":{\"subtype\":\"error\",\"request_id\":%q,\"error\":\"model rejected\"}}\n", id)
				return err
			}
			_, err := fmt.Fprintf(output, "{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":%q,\"response\":%s}}\n", id, result)
			return err
		}
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			var frame struct {
				ID      string `json:"request_id"`
				Request struct {
					Subtype, Model string
					Settings       struct{ EffortLevel string }
				}
			}
			if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
				peerDone <- err
				return
			}
			var err error
			switch frame.Request.Subtype {
			case "initialize":
				err = send(frame.ID, `{}`, false)
				if err == nil {
					_, err = fmt.Fprintf(output, "{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"config-concurrency\",\"cwd\":%q,\"claude_code_version\":\"2.1.258\"}\n", cwd)
				}
			case "get_settings":
				err = send(frame.ID, `{"applied":{"ultracode":false}}`, false)
			case "set_model":
				requests <- agent.ConfigChange{ID: agent.ConfigModel, Value: frame.Request.Model}
				if hold {
					continue
				}
				models = append(models, struct{ ID, Model string }{frame.ID, frame.Request.Model})
				if len(models) < 2 {
					continue
				}
				for _, model := range slices.Backward(models) {
					if err = send(model.ID, `{}`, model.Model == "rejected/model"); err != nil {
						break
					}
				}
			case "apply_flag_settings":
				requests <- agent.ConfigChange{ID: agent.ConfigEffort, Value: frame.Request.Settings.EffortLevel}
				err = send(frame.ID, `{"applied":true}`, false)
			default:
				err = fmt.Errorf("unexpected native control %q", frame.Request.Subtype)
			}
			if err != nil {
				peerDone <- err
				return
			}
		}
		peerDone <- scanner.Err()
	}()
	provider := agentclaude.NewWithStream(agentclaude.Config{}, func(context.Context, claude.SessionOptions) (agentclaude.TestStreamOpen, error) {
		return agentclaude.TestStreamOpen{Transport: claude.StreamTransport{Reader: reader, Writer: writer}}, nil
	})
	t.Cleanup(func() {
		_ = provider.Stop(context.Background())
		_ = writer.Close()
		_ = output.Close()
		select {
		case err := <-peerDone:
			assert.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("synthetic peer did not stop")
		}
		_ = input.Close()
	})
	_, err := provider.Start(ctx)
	require.NoError(t, err)
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: cwd})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	return session, requests
}
