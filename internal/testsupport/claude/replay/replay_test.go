// SPDX-License-Identifier: Apache-2.0
package transcript

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReplayAdapterCompletion(t *testing.T) {
	recording := `{"ordinal":1,"direction":"client","channel":"control","data":{"type":"control_request","request_id":"old","request":{"forwardSubagentText":true}}}
{"ordinal":2,"direction":"server","channel":"protocol","data":{"type":"notice"}}
{"ordinal":3,"direction":"server","channel":"control","data":{"type":"control_response","response":{"request_id":"old"}}}
`
	input := `{"type":"control_request","request_id":"new","request":{"forwardSubagentText":true}}` + "\n"
	var out bytes.Buffer
	done := false
	{
		err := Replay(strings.NewReader(input), &out, strings.NewReader(recording), func() error { done = true; return nil })
		require.NoError(t, err)
	}
	require.True(t, done, out.String())
	require.Contains(t, out.String(), `"request_id":"new"`, out.String())
	require.LessOrEqual(t, strings.Index(out.String(), "notice"), strings.Index(out.String(), "response"), out.String())
	for _, tc := range []struct{ name, input string }{
		{"eof", ""}, {"mismatch", strings.Replace(input, "true", "false", 1)}, {"extra", input + input},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, Replay(strings.NewReader(tc.input), &bytes.Buffer{}, strings.NewReader(recording), nil), "accepted mismatch/EOF/extra")
		})
	}
}

// These limits belong to the JSONL-to-process adapter, before the protocol
// matcher runs. Protocol ID and matching matrices live with the tool.
func TestReplayAdapterBounds(t *testing.T) {
	for _, tc := range []struct {
		name    string
		capture string
		want    string
	}{
		{"capture bytes", strings.Repeat("x", (16<<20)+1), "capture byte limit exceeded"},
		{"line bytes", strings.Repeat("x", (2<<20)+1), "token too long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			completed := false
			err := Replay(strings.NewReader(""), &output, strings.NewReader(tc.capture),
				func() error { completed = true; return nil })
			require.ErrorContains(t, err, tc.want)
			require.Empty(t, output.String())
			require.False(t, completed)
		})
	}
}

// The fixture can finish while the caller is still accepting its final output.
func TestReplayAdapterDrainsFinalFrames(t *testing.T) {
	input := `{"type":"control_request","request_id":"new","request":{"forwardSubagentText":true}}` + "\n"
	capture := `{"ordinal":1,"direction":"client","channel":"control","data":{"type":"control_request","request_id":"old","request":{"forwardSubagentText":true}}}
{"ordinal":2,"direction":"server","channel":"protocol","data":{"type":"notice","value":"final"}}
{"ordinal":3,"direction":"server","channel":"control","data":{"type":"control_response","response":{"request_id":"old"}}}
`
	started, release, consumed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	var output bytes.Buffer
	writer := &delayedReplayWriter{Writer: &output, started: started, release: release}
	done := make(chan error, 1)
	go func() {
		done <- Replay(strings.NewReader(input), writer, strings.NewReader(capture), func() error { close(consumed); return nil })
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("output did not start")
	}
	select {
	case <-consumed:
	case <-time.After(5 * time.Second):
		t.Fatal("fixture did not finish")
	}
	time.Sleep(200 * time.Millisecond)
	close(release)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("output did not drain")
	}
	require.Contains(t, output.String(), `"value":"final"`)
	require.Contains(t, output.String(), `"request_id":"new"`)
}

type delayedReplayWriter struct {
	io.Writer
	started chan struct{}
	release <-chan struct{}
}

func (w *delayedReplayWriter) Write(data []byte) (int, error) {
	select {
	case <-w.started:
	default:
		close(w.started)
	}
	<-w.release
	return w.Writer.Write(data)
}

func TestReplayAdapterUnblocksInputAfterOutputFailure(t *testing.T) {
	input, producer := io.Pipe()
	outputReader, output := io.Pipe()
	t.Cleanup(func() { _ = input.Close(); _ = producer.Close(); _ = outputReader.Close(); _ = output.Close() })
	outputErr := errors.New("synthetic output failure")
	require.NoError(t, outputReader.CloseWithError(outputErr))
	sent := make(chan error, 1)
	go func() {
		_, err := io.WriteString(producer, `{"type":"control_request","request_id":"new","request":{"forwardSubagentText":true}}`+"\n")
		sent <- err
	}()
	capture := `{"ordinal":1,"direction":"client","channel":"control","data":{"type":"control_request","request_id":"old","request":{"forwardSubagentText":true}}}
{"ordinal":2,"direction":"server","channel":"control","data":{"type":"control_response","response":{"request_id":"old"}}}
`
	done := make(chan error, 1)
	go func() { done <- Replay(input, output, strings.NewReader(capture), nil) }()
	select {
	case err := <-done:
		require.ErrorIs(t, err, outputErr)
	case <-time.After(5 * time.Second):
		t.Fatal("failed output left input copier blocked")
	}
	require.NoError(t, <-sent)
}
