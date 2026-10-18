// SPDX-License-Identifier: Apache-2.0
// Package transcript provides reusable native wire replay and correlation primitives.
package transcript

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"
)

type Frame struct {
	Ordinal     int             `json:"ordinal"`
	Direction   string          `json:"direction"`
	Channel     string          `json:"channel"`
	Data        json.RawMessage `json:"data,omitempty"`
	DelayMillis int             `json:"delay_ms,omitempty"`
}

// Replay follows capture dependencies, without replaying elapsed wall time.
// It accepts dynamic client request IDs, preserves their JSON types, and rejects
// any structural mismatch, duplicate binding, early EOF, or extra client frame.
// The caller must bound the peer lifetime (Open/Close and a test deadline).
func Replay(input io.Reader, output io.Writer, capture io.Reader, complete func() error) error {
	return ReplayWithOptions(input, output, capture, complete, ReplayOptions{})
}

// ErrDisconnected marks an intentional synthetic disconnect after prior frames.
var ErrDisconnected = errors.New("scenario disconnected")

// ReplayOptions bounds scripted scheduling. Zero values select the replay limits.
// Blocking stream operations must be bounded by the caller, for example by
// closing owned pipes on context cancellation as tools/mock.Run does.
type ReplayOptions struct {
	Context   context.Context
	MaxFrames int
}

// ReplayWithOptions uses the same matcher and ID state as recorded replay.
// Synthetic frames may delay up to 30 seconds or use direction "disconnect"
// with an empty data field to deliberately terminate pending work.
func ReplayWithOptions(input io.Reader, output io.Writer, capture io.Reader, complete func() error, options ReplayOptions) error {
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	maxFrames := options.MaxFrames
	if maxFrames == 0 {
		maxFrames = 10000
	}
	if maxFrames < 1 || maxFrames > 10000 {
		return fmt.Errorf("invalid frame limit")
	}
	captureBudget := &io.LimitedReader{R: capture, N: (16 << 20) + 1}
	frames := bufio.NewScanner(captureBudget)
	output = &budgetWriter{writer: output, remaining: 16 << 20}
	frames.Buffer(make([]byte, 4096), 2<<20)
	inputBudget := &io.LimitedReader{R: input, N: (16 << 20) + 1}
	client := bufio.NewScanner(inputBudget)
	client.Buffer(make([]byte, 4096), 2<<20)
	ids := map[string]any{}
	reverse := map[string]string{}
	seenClient := map[string]bool{}
	seenServer := map[string]bool{}
	pendingClient := map[string]bool{}
	pendingServer := map[string]bool{}
	ordinal := 0
	for frames.Scan() {
		var f Frame
		if err := json.Unmarshal(frames.Bytes(), &f); err != nil {
			return fmt.Errorf("invalid capture frame")
		}
		ordinal++
		if ordinal > maxFrames {
			return fmt.Errorf("frame limit exceeded")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if f.DelayMillis < 0 || f.DelayMillis > 30000 {
			return fmt.Errorf("invalid delay at %d", ordinal)
		}
		if f.DelayMillis > 0 {
			timer := time.NewTimer(time.Duration(f.DelayMillis) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if f.Ordinal != ordinal || (f.Channel != "protocol" && f.Channel != "control") {
			return fmt.Errorf("invalid capture order/channel at %d", ordinal)
		}
		if f.Direction == "disconnect" {
			if len(f.Data) != 0 {
				return fmt.Errorf("disconnect has data at %d", ordinal)
			}
			if frames.Scan() {
				return fmt.Errorf("frames after disconnect")
			}
			if err := frames.Err(); err != nil {
				return err
			}
			return ErrDisconnected
		}
		var expected any
		if err := decodeJSON(f.Data, &expected); err != nil {
			return fmt.Errorf("invalid native JSON at %d", ordinal)
		}
		switch f.Direction {
		case "client":
			if !client.Scan() {
				return fmt.Errorf("early client EOF at %d", ordinal)
			}
			var actual any
			typ, id := control(expected)
			if (typ == "control_request" || typ == "control_response") && !validID(id) {
				return fmt.Errorf("missing/invalid control ID at %d", ordinal)
			}
			if decodeJSON(client.Bytes(), &actual) != nil {
				return fmt.Errorf("invalid client JSON at %d", ordinal)
			}
			if typ == "control_request" {
				actualType, actualID := control(actual)
				if actualType != typ || !validID(actualID) || reflect.TypeOf(id) != reflect.TypeOf(actualID) {
					return fmt.Errorf("invalid client control ID at %d", ordinal)
				}
				oldKey, actualKey := key(id), key(actualID)
				if old, ok := ids[oldKey]; ok && !reflect.DeepEqual(old, actualID) {
					return fmt.Errorf("inconsistent client ID at %d", ordinal)
				}
				if previous, ok := reverse[actualKey]; ok && previous != oldKey {
					return fmt.Errorf("duplicate client ID binding at %d", ordinal)
				}
				ids[oldKey], reverse[actualKey] = actualID, oldKey
				// Only the native envelope ID is dynamic. Arbitrary payload fields named
				// request_id must retain literal equality like every other payload field.
				expected.(map[string]any)["request_id"] = actualID
			}
			if typ == "control_response" && !pendingServer[key(id)] {
				return fmt.Errorf("unknown/duplicate reverse response at %d", ordinal)
			}
			if !reflect.DeepEqual(expected, actual) {
				return fmt.Errorf("client mismatch at %d", ordinal)
			}
			if typ == "control_request" {
				if seenClient[key(id)] {
					return fmt.Errorf("duplicate client request at %d", ordinal)
				}
				seenClient[key(id)] = true
				pendingClient[key(id)] = true
			}
			if typ == "control_response" {
				delete(pendingServer, key(id))
			}
		case "server":
			typ, id := control(expected)
			if (typ == "control_request" || typ == "control_response" || typ == "control_cancel_request") && !validID(id) {
				return fmt.Errorf("missing/invalid server control ID at %d", ordinal)
			}
			if typ == "control_request" {
				if seenServer[key(id)] {
					return fmt.Errorf("duplicate reverse request at %d", ordinal)
				}
				seenServer[key(id)] = true
				pendingServer[key(id)] = true
			}
			if typ == "control_response" {
				if !pendingClient[key(id)] {
					return fmt.Errorf("unknown/duplicate client response at %d", ordinal)
				}
				delete(pendingClient, key(id))
			}
			if typ == "control_cancel_request" {
				if !pendingServer[key(id)] {
					return fmt.Errorf("unknown cancellation at %d", ordinal)
				}
				delete(pendingServer, key(id))
			}
			serverIDs := map[string]any{}
			if typ == "control_response" {
				serverIDs = ids
			}
			if err := writeServer(output, f.Data, serverIDs); err != nil {
				return err
			}
		default:
			return fmt.Errorf("invalid direction at %d", ordinal)
		}
	}
	if err := frames.Err(); err != nil {
		return err
	}
	if captureBudget.N == 0 {
		return fmt.Errorf("capture byte limit exceeded")
	}
	if ordinal == 0 {
		return fmt.Errorf("empty capture")
	}
	if len(pendingClient) != 0 || len(pendingServer) != 0 {
		return fmt.Errorf("capture ends with pending controls")
	}
	if complete != nil {
		if err := complete(); err != nil {
			return err
		}
	}
	if client.Scan() {
		return fmt.Errorf("extra client frame")
	}
	if inputBudget.N == 0 {
		return fmt.Errorf("client byte limit exceeded")
	}
	return client.Err()
}
func key(v any) string { b, _ := json.Marshal(v); return string(b) }

// writeServer rewrites only the native control_response.response.request_id.
// All other server frames retain exact capture bytes; payload request_id fields
// are never interpreted as native correlation identifiers.
func writeServer(output io.Writer, raw []byte, ids map[string]any) error {
	mapped := raw
	if len(ids) != 0 {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return err
		}
		var response map[string]json.RawMessage
		if err := json.Unmarshal(envelope["response"], &response); err != nil {
			return err
		}
		var old any
		if err := decodeJSON(response["request_id"], &old); err != nil {
			return err
		}
		if next, ok := ids[key(old)]; ok {
			response["request_id"], _ = json.Marshal(next)
			envelope["response"], _ = json.Marshal(response)
			var err error
			mapped, err = json.Marshal(envelope)
			if err != nil {
				return err
			}
		}
	}
	_, err := output.Write(append(append([]byte(nil), mapped...), '\n'))
	return err
}

func control(v any) (string, any) {
	m, ok := v.(map[string]any)
	if !ok {
		return "", nil
	}
	typ, _ := m["type"].(string)
	if typ == "control_response" {
		if response, ok := m["response"].(map[string]any); ok {
			return typ, response["request_id"]
		}
	}
	return typ, m["request_id"]
}

func decodeJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func validID(v any) bool {
	switch x := v.(type) {
	case string:
		return x != ""
	case json.Number:
		return true
	}
	return false
}

// Every server frame is one write. Enforce both individual and conversation
// byte budgets after dynamic ID substitution, before emitting any excess data.
type budgetWriter struct {
	writer    io.Writer
	remaining int
}

func (b *budgetWriter) Write(p []byte) (int, error) {
	if len(p) > 2<<20 || len(p) > b.remaining {
		return 0, fmt.Errorf("server byte limit exceeded")
	}
	n, err := b.writer.Write(p)
	b.remaining -= n
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}
