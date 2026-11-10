package agentcodex_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/agent"
	agentcodex "github.com/zoumo/agent-sdk-go/agent/codex"
	"github.com/zoumo/agent-sdk-go/codex"
)

func TestSetConfigConcurrentCallsSettleTheirOwnChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	session, requests := openCodexConfigConcurrencyPeer(t, ctx, false)
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
				assert.Equal(t, agent.Acceptance{Sent: boolPointer(true), NativeAccepted: boolPointer(false)}, result.receipt.Acceptance)
			} else {
				require.NoError(t, result.err)
				assertAccepted(t, result.receipt.Acceptance)
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

func TestSetConfigCloseRaceSettlesWithConsistentAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	session, requests := openCodexConfigConcurrencyPeer(t, ctx, true)
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
			assert.Equal(t, agent.Acceptance{Sent: boolPointer(true)}, result.receipt.Acceptance)
		case agent.ErrorClosed:
			assert.Equal(t, agent.Acceptance{Sent: boolPointer(false)}, result.receipt.Acceptance)
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
	assert.Equal(t, agent.Acceptance{Sent: boolPointer(false)}, receipt.Acceptance)
	assert.Empty(t, requests)
}

func boolPointer(value bool) *bool { return &value }

// This synthetic peer accepts arbitrary request order. In the concurrent case
// it holds both model calls, then replies in reverse arrival order; in the close
// case it withholds the settings acknowledgement while still serving cleanup.
func openCodexConfigConcurrencyPeer(t *testing.T, ctx context.Context, hold bool) (agent.Session, <-chan agent.ConfigChange) {
	t.Helper()
	reader, output := io.Pipe()
	input, writer := io.Pipe()
	requests := make(chan agent.ConfigChange, 8)
	peerDone := make(chan error, 1)
	go func() {
		defer output.Close()
		var models []struct {
			ID     json.RawMessage
			Change agent.ConfigChange
		}
		send := func(id json.RawMessage, result string, rejected bool) error {
			if rejected {
				_, err := fmt.Fprintf(output, "{\"id\":%s,\"error\":{\"code\":-32602,\"message\":\"model rejected\"}}\n", id)
				return err
			}
			return json.NewEncoder(output).Encode(struct {
				ID     json.RawMessage `json:"id"`
				Result json.RawMessage `json:"result"`
			}{id, json.RawMessage(result)})
		}
		scanner := bufio.NewScanner(input)
		for scanner.Scan() {
			var frame struct {
				ID     json.RawMessage
				Method string
				Params struct{ Model, Effort string }
			}
			if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
				peerDone <- err
				return
			}
			var err error
			switch frame.Method {
			case "initialize":
				err = send(frame.ID, `{"userAgent":"synthetic","codexHome":"/synthetic","platformFamily":"unix","platformOs":"linux"}`, false)
			case "initialized":
				continue
			case "thread/start":
				err = send(frame.ID, codexThreadStartResult("config-concurrency"), false)
			case "thread/settings/update":
				change := agent.ConfigChange{ID: agent.ConfigModel, Value: frame.Params.Model}
				if frame.Params.Effort != "" {
					change = agent.ConfigChange{ID: agent.ConfigEffort, Value: frame.Params.Effort}
				}
				requests <- change
				if hold {
					continue
				}
				if change.ID == agent.ConfigModel {
					models = append(models, struct {
						ID     json.RawMessage
						Change agent.ConfigChange
					}{frame.ID, change})
					if len(models) < 2 {
						continue
					}
					for index := len(models) - 1; index >= 0; index-- {
						if err = send(models[index].ID, `{}`, models[index].Change.Value == "rejected/model"); err != nil {
							break
						}
					}
				} else {
					err = send(frame.ID, `{}`, false)
				}
			case "thread/unsubscribe":
				err = send(frame.ID, `{"status":"unsubscribed"}`, false)
			default:
				err = fmt.Errorf("unexpected native method %q", frame.Method)
			}
			if err != nil {
				peerDone <- err
				return
			}
		}
		peerDone <- scanner.Err()
	}()
	provider := agentcodex.NewWithStream(agentcodex.Config{}, codex.StreamTransport{Reader: reader, Writer: writer}, nil)
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
	session, err := provider.Open(ctx, agent.OpenOptions{Cwd: "/synthetic"})
	require.NoError(t, err)
	closeSessionOnCleanup(t, session)
	return session, requests
}
