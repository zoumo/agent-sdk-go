package recording

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// ErrDisconnected identifies an explicitly scripted transport fault.
var ErrDisconnected = errors.New("scripted peer disconnect")

// RunPeer runs the same strict matching and request-ID correlation engine as
// Peer, without its private replay/assert handshake. Completion requires client
// EOF; any trailing request fails. Streams are owned and closed on return and on
// cancellation. Their Close methods must unblock Read/Write (pipes, files and
// network connections do); arbitrary non-interruptible streams are unsupported.
// Frames and total wire traffic are limited to 10000 and 8 MiB respectively.
func RunPeer(ctx context.Context, in io.ReadCloser, out io.WriteCloser, frames []Frame) error {
	if ctx == nil || in == nil || out == nil {
		return errors.New("peer requires context and streams")
	}
	defer in.Close()
	defer out.Close()
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			_ = in.Close()
			_ = out.Close()
		case <-done:
		}
	}()
	err := runPeer(ctx, in, out, frames, false)
	close(done)
	<-stopped
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// boundedReader rejects excess traffic, including traffic after the scenario.
type boundedReader struct {
	in        io.Reader
	remaining int
}

func (r *boundedReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		var probe [1]byte
		n, err := r.in.Read(probe[:])
		if n != 0 {
			return 0, errors.New("peer input byte limit")
		}
		return 0, err
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.in.Read(p)
	r.remaining -= n
	return n, err
}

func validID(id json.RawMessage) bool {
	if len(id) == 0 {
		return false
	}
	switch canonical(id).(type) {
	case string, json.Number:
		return true
	default:
		return false
	}
}

func runPeer(ctx context.Context, in io.Reader, out io.Writer, frames []Frame, assertion bool) error {
	if len(frames) == 0 || len(frames) > 10000 {
		return errors.New("peer frame count limit")
	}
	const trafficLimit = 8 << 20
	total := 0
	for i, f := range frames {
		total += len(f.Wire) + 1
		if total > trafficLimit || len(f.Wire) > ProtocolLimit {
			return errors.New("peer scenario byte limit")
		}
		if f.DelayMS < 0 || f.DelayMS > 300000 {
			return errors.New("peer delay out of range")
		}
		if f.Disconnect {
			if i != len(frames)-1 || (len(f.Wire) != 0 && string(f.Wire) != "null") || f.Direction != "s2c" {
				return errors.New("disconnect must be final server action without wire")
			}
		} else if f.Direction != "c2s" && f.Direction != "s2c" {
			return errors.New("invalid peer direction")
		}
	}
	s := bufio.NewScanner(&boundedReader{in: in, remaining: trafficLimit})
	s.Buffer(make([]byte, 4096), ProtocolLimit+1)
	ids := map[string]json.RawMessage{}
	active := map[string]bool{}
	reverse := map[string]bool{}
	reverseSeen := map[string]bool{}
	reverseThread := map[string]json.RawMessage{}
	written := 0
	for _, f := range frames {
		if err := ctx.Err(); err != nil {
			return err
		}
		if f.DelayMS != 0 {
			timer := time.NewTimer(time.Duration(f.DelayMS) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if f.Disconnect {
			return ErrDisconnected
		}
		want, err := object(f.Wire)
		if err != nil {
			return err
		}
		if id, exists := want["id"]; exists && !validID(id) {
			return errors.New("invalid scenario ID")
		}
		if f.Direction == "c2s" {
			if !s.Scan() {
				if s.Err() != nil {
					return s.Err()
				}
				return fmt.Errorf("early client EOF at ordinal %d", f.Ordinal)
			}
			got, err := object(s.Bytes())
			if err != nil {
				return err
			}
			if id, exists := got["id"]; exists && !validID(id) {
				return errors.New("invalid client ID")
			}
			if _, rpc := want["method"]; rpc && want["id"] != nil {
				key := string(mustJSON(canonical(want["id"])))
				if active[key] || ids[key] != nil {
					return errors.New("duplicate recorded request ID")
				}
				if got["id"] == nil {
					return errors.New("missing client ID")
				}
				for _, id := range ids {
					if equal(canonical(id), canonical(got["id"])) {
						return errors.New("duplicate current request ID")
					}
				}
				ids[key] = append(json.RawMessage(nil), got["id"]...)
				active[key] = true
				got["id"] = want["id"]
			} else if want["id"] != nil {
				key := string(mustJSON(canonical(want["id"])))
				if !reverse[key] {
					return errors.New("unknown or duplicate reverse answer")
				}
				delete(reverse, key)
			}
			if !equal(canonical(mustJSON(got)), canonical(f.Wire)) {
				return fmt.Errorf("client frame mismatch at ordinal %d", f.Ordinal)
			}
		} else {
			if id := want["id"]; id != nil {
				key := string(mustJSON(canonical(id)))
				if want["method"] != nil {
					if reverseSeen[key] {
						return errors.New("duplicate reverse ID")
					}
					reverse[key] = true
					reverseSeen[key] = true
					var params map[string]json.RawMessage
					_ = json.Unmarshal(want["params"], &params)
					var thread string
					if raw := params["threadId"]; len(raw) != 0 && raw[0] == '"' && json.Unmarshal(raw, &thread) == nil {
						reverseThread[key] = raw
					}
				} else {
					if !active[key] || ids[key] == nil {
						return errors.New("response without request")
					}
					want["id"] = ids[key]
					delete(active, key)
				}
			}
			// Native resolution cancels an unanswered request only when both its
			// request ID and owning thread match. Unknown/malformed signals are
			// still emitted faithfully and do not change pending state.
			if string(want["method"]) == `"serverRequest/resolved"` {
				var params map[string]json.RawMessage
				if json.Unmarshal(want["params"], &params) == nil && validID(params["requestId"]) {
					key := string(mustJSON(canonical(params["requestId"])))
					if reverse[key] && reverseThread[key] != nil && equal(canonical(reverseThread[key]), canonical(params["threadId"])) {
						delete(reverse, key)
					}
				}
			}
			wire := mustJSON(want)
			if len(wire) > ProtocolLimit {
				return errors.New("peer output frame byte limit")
			}
			written += len(wire) + 1
			if written > trafficLimit {
				return errors.New("peer output byte limit")
			}
			if err = writePeerLine(out, wire); err != nil {
				return err
			}
		}
	}
	if len(active) != 0 || len(reverse) != 0 {
		return errors.New("unfinished protocol requests")
	}
	if assertion {
		if !s.Scan() {
			return errors.New("missing completion assertion")
		}
		m, err := object(s.Bytes())
		if err != nil {
			return err
		}
		if !bytes.Equal(m["method"], []byte(`"replay/assert"`)) || !validID(m["id"]) || m["params"] != nil {
			return errors.New("extra client request")
		}
		if err = writePeerLine(out, []byte(fmt.Sprintf("{\"id\":%s,\"result\":{}}", m["id"]))); err != nil {
			return err
		}
	}
	if s.Scan() {
		return errors.New("extra client frame after scenario")
	}
	return s.Err()
}

func writePeerLine(out io.Writer, wire []byte) error {
	n, err := fmt.Fprintln(out, string(wire))
	if err == nil && n != len(wire)+1 {
		return io.ErrShortWrite
	}
	return err
}
