package pi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zoumo/agent-sdk-go/client/pi"
)

func TestBash(t *testing.T) {
	for _, tc := range []struct {
		name    string
		exclude *bool
		data    string
		want    pi.BashResult
	}{
		{"silent success", nil, `{"output":"","exitCode":0,"cancelled":false,"truncated":false}`, pi.BashResult{ExitCode: new(0)}},
		{"nonzero exit", new(false), `{"output":"failed\n","exitCode":7,"cancelled":false,"truncated":false}`, pi.BashResult{Output: "failed\n", ExitCode: new(7)}},
		{"truncated", new(true), `{"output":"tail","exitCode":0,"cancelled":false,"truncated":true,"fullOutputPath":"/tmp/full.log"}`, pi.BashResult{
			Output: "tail", ExitCode: new(0), Truncated: true, FullOutputPath: new("/tmp/full.log"),
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const script = "printf 'line one\nline two'"
			frames := make(chan map[string]any, 1)
			s, ctx := runPeer(t, func(p peer) error {
				var frame map[string]any
				if err := p.input.Decode(&frame); err != nil {
					return err
				}
				id, ok := frame["id"].(string)
				if !ok || id == "" {
					return fmt.Errorf("invalid request id %v", frame["id"])
				}
				delete(frame, "id")
				frames <- frame
				return p.reply(command{ID: id, Type: "bash"}, json.RawMessage(tc.data))
			})
			got, err := s.Bash(ctx, script, tc.exclude)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			wantFrame := map[string]any{"type": "bash", "command": script}
			if tc.exclude != nil {
				wantFrame["excludeFromContext"] = *tc.exclude
			}
			assert.Equal(t, wantFrame, <-frames)
		})
	}
}

func TestBashResponseErrors(t *testing.T) {
	for _, data := range []string{
		`{"cancelled":false,"truncated":false}`,
		`{"output":"","truncated":false}`,
		`{"output":"","cancelled":false}`,
		`{"output":null,"cancelled":false,"truncated":false}`,
		`{"output":"","exitCode":"0","cancelled":false,"truncated":false}`,
	} {
		t.Run(data, func(t *testing.T) {
			s, ctx := runPeer(t, func(p peer) error {
				c, err := p.receive()
				if err != nil {
					return err
				}
				return p.reply(c, json.RawMessage(data))
			})
			_, err := s.Bash(ctx, "pwd", nil)
			var malformed *pi.DispatchError
			require.ErrorAs(t, err, &malformed)
			assert.True(t, malformed.Sent)
			assert.ErrorContains(t, err, "bash response data")
		})
	}
}

func TestBashNativeRejection(t *testing.T) {
	s, ctx := runPeer(t, func(p peer) error {
		for _, kind := range []string{"bash", "abort_bash"} {
			c, err := p.receive()
			if err != nil {
				return err
			}
			if c.Type != kind {
				return fmt.Errorf("got %q, want %q", c.Type, kind)
			}
			if err := json.NewEncoder(p.output).Encode(map[string]any{
				"type": "response", "command": kind, "id": c.ID, "success": false, "error": "native refusal",
			}); err != nil {
				return err
			}
		}
		return nil
	})
	_, err := s.Bash(ctx, "pwd", nil)
	var rejected *pi.CommandError
	require.ErrorAs(t, err, &rejected)
	assert.Equal(t, "bash", rejected.Command)
	assert.Equal(t, "native refusal", rejected.Message)
	require.ErrorAs(t, s.AbortBash(ctx), &rejected)
	assert.Equal(t, "abort_bash", rejected.Command)
}

func TestAbortBashWhileReceivingOutput(t *testing.T) {
	s, ctx := runPeer(t, func(p peer) error {
		bash, err := p.receive()
		if err != nil {
			return err
		}
		if bash.Type != "bash" {
			return fmt.Errorf("expected bash, got %q", bash.Type)
		}
		if err := json.NewEncoder(p.output).Encode(map[string]string{
			"type": "bash_execution_update", "id": bash.ID, "delta": "partial",
		}); err != nil {
			return err
		}
		abort, err := p.receive()
		if err != nil {
			return err
		}
		if abort.Type != "abort_bash" || abort.ID == bash.ID {
			return fmt.Errorf("unexpected abort request %+v for bash %q", abort, bash.ID)
		}
		if err := p.reply(abort, nil); err != nil {
			return err
		}
		return p.reply(bash, json.RawMessage(`{"output":"partial","cancelled":true,"truncated":false}`))
	})
	type outcome struct {
		result pi.BashResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := s.Bash(ctx, "sleep 10", nil)
		done <- outcome{result, err}
	}()
	for _, kind := range []string{"session_ready", "bash_execution_update"} {
		event, err := s.Receive(ctx)
		require.NoError(t, err)
		require.Equal(t, kind, event.Type)
	}
	require.NoError(t, s.AbortBash(ctx))
	got := <-done
	require.NoError(t, got.err)
	assert.Equal(t, pi.BashResult{Output: "partial", Cancelled: true}, got.result)
}

func TestCancelBashWaitAcceptsLateResponse(t *testing.T) {
	started := make(chan struct{})
	s, ctx := runPeer(t, func(p peer) error {
		bash, err := p.receive()
		close(started)
		if err != nil {
			return err
		}
		if bash.Type != "bash" {
			return fmt.Errorf("expected bash, got %q", bash.Type)
		}
		// The first state read confirms that the bash frame finished writing.
		// The second is pending when the canceled call's late response arrives.
		for i := range 2 {
			state, err := p.receive()
			if err != nil {
				return err
			}
			if state.Type != "get_state" {
				return fmt.Errorf("expected get_state, got %q", state.Type)
			}
			if i == 1 {
				if err := p.reply(bash, json.RawMessage(`{"output":"late","exitCode":0,"cancelled":false,"truncated":false}`)); err != nil {
					return err
				}
			}
			if err := p.reply(state, map[string]string{"sessionId": "native-1"}); err != nil {
				return err
			}
		}
		c, err := p.receive()
		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("expected EOF, got %+v and %v", c, err)
		}
		return nil
	})
	wait, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.Bash(wait, "pwd", nil)
		done <- err
	}()
	<-started
	_, err := s.State(ctx)
	require.NoError(t, err)
	cancel()
	var dispatched *pi.DispatchError
	require.ErrorAs(t, <-done, &dispatched)
	assert.True(t, dispatched.Sent)
	assert.ErrorIs(t, dispatched, context.Canceled)
	state, err := s.State(ctx)
	require.NoError(t, err)
	assert.Equal(t, s.ID(), state.SessionID)
	require.NoError(t, s.Close())
}
