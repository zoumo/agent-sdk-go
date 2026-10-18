// SPDX-License-Identifier: Apache-2.0
// Package transcript contains the test/example-only native wire replay peer.
package transcript

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
)

type Frame struct {
	Ordinal   int             `json:"ordinal"`
	Direction string          `json:"direction"`
	Channel   string          `json:"channel"`
	Data      json.RawMessage `json:"data"`
}

// Replay follows capture dependencies, without replaying elapsed wall time.
// It accepts dynamic client request IDs, preserves their JSON types, and rejects
// any structural mismatch, duplicate binding, early EOF, or extra client frame.
// The caller must bound the peer lifetime (Open/Close and a test deadline).
func Replay(input io.Reader, output io.Writer, capture io.Reader, complete func() error) error {
	frames := bufio.NewScanner(capture)
	frames.Buffer(make([]byte, 4096), 2<<20)
	client := bufio.NewScanner(input)
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
		if f.Ordinal != ordinal || (f.Channel != "protocol" && f.Channel != "control") {
			return fmt.Errorf("invalid capture order/channel at %d", ordinal)
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
			binding := typ == "control_request"
			activeIDs, activeReverse := ids, reverse
			if typ == "control_response" {
				if !pendingServer[key(id)] {
					return fmt.Errorf("unknown/duplicate reverse response at %d", ordinal)
				}
				activeIDs = map[string]any{key(id): id}
				activeReverse = map[string]string{key(id): key(id)}
			}
			if decodeJSON(client.Bytes(), &actual) != nil || !match(expected, actual, "", activeIDs, activeReverse, binding) {
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
	return client.Err()
}
func key(v any) string { b, _ := json.Marshal(v); return string(b) }
func match(want, got any, field string, ids map[string]any, reverse map[string]string, bind bool) bool {
	if field == "request_id" {
		switch want.(type) {
		case string, json.Number:
		default:
			return false
		}
		if reflect.TypeOf(want) != reflect.TypeOf(got) {
			return false
		}
		k := key(want)
		if old, ok := ids[k]; ok {
			return reflect.DeepEqual(old, got)
		}
		if !bind {
			return false
		}
		g := key(got)
		if prev, ok := reverse[g]; ok && prev != k {
			return false
		}
		ids[k] = got
		reverse[g] = k
		return true
	}
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(w) != len(g) {
			return false
		}
		for k, v := range w {
			gv, ok := g[k]
			if !ok || !match(v, gv, k, ids, reverse, bind) {
				return false
			}
		}
		return true
	case []any:
		g, ok := got.([]any)
		if !ok || len(w) != len(g) {
			return false
		}
		for i, v := range w {
			if !match(v, g[i], field, ids, reverse, bind) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(want, got)
	}
}

var requestID = regexp.MustCompile(`("request_id"\s*:\s*)("(?:[^"\\]|\\.)*"|-?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)`)

func writeServer(output io.Writer, raw []byte, ids map[string]any) error {
	mapped := requestID.ReplaceAllFunc(raw, func(b []byte) []byte {
		pair := requestID.FindSubmatch(b)
		var old any
		if decodeJSON(pair[2], &old) != nil {
			return b
		}
		next, ok := ids[key(old)]
		if !ok {
			return b
		}
		value, _ := json.Marshal(next)
		return append(append([]byte(nil), pair[1]...), value...)
	})
	_, err := output.Write(append(mapped, '\n'))
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
	return d.Decode(v)
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
