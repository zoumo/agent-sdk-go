package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

// objectFields rejects duplicate envelope members, which otherwise permit an
// ambiguous correlation ID to be interpreted differently by different parsers.
func objectFields(raw []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("%w: invalid UTF-8", ErrProtocol)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("%w: expected object", ErrProtocol)
	}
	out := make(map[string]json.RawMessage)
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: malformed object", ErrProtocol)
		}
		key, ok := token.(string)
		if !ok {
			return nil, ErrProtocol
		}
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("%w: duplicate envelope member", ErrProtocol)
		}
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return nil, fmt.Errorf("%w: malformed member", ErrProtocol)
		}
		out[key] = value
	}
	if _, err = d.Token(); err != nil {
		return nil, fmt.Errorf("%w: incomplete object", ErrProtocol)
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing data", ErrProtocol)
	}
	return out, nil
}
func (c *Client) readLoop() {
	size := 64 << 10
	if c.limits.MaxFrameBytes < size {
		size = c.limits.MaxFrameBytes + 1
	}
	r := bufio.NewReaderSize(c.stdout, size)
	var frame []byte
	for {
		part, err := r.ReadSlice('\n')
		total := len(frame) + len(part)
		terminated := len(part) > 0 && part[len(part)-1] == '\n'
		payload := total
		if terminated {
			payload--
		}
		if payload > c.limits.MaxFrameBytes {
			c.fail(fmt.Errorf("%w: %w", ErrProtocol, ErrFrameTooLarge))
			return
		}
		frame = append(frame, part...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if len(frame) > 0 {
				c.fail(fmt.Errorf("%w: unterminated frame: %w", ErrProtocol, err))
			} else {
				c.fail(err)
			}
			return
		}
		raw := bytes.TrimSuffix(frame, []byte{'\n'})
		raw = bytes.TrimSuffix(raw, []byte{'\r'})
		if err := c.routeFrame(raw); err != nil {
			c.fail(err)
			return
		}
		frame = frame[:0]
	}
}
func (c *Client) routeFrame(raw []byte) error {
	f, err := objectFields(raw)
	if err != nil {
		return err
	}
	idRaw, hasID := f["id"]
	methodRaw, hasMethod := f["method"]
	result, hasResult := f["result"]
	errorRaw, hasError := f["error"]
	if hasMethod {
		var method string
		if err = json.Unmarshal(methodRaw, &method); err != nil || method == "" {
			return fmt.Errorf("%w: invalid method", ErrProtocol)
		}
		if hasResult || hasError {
			return fmt.Errorf("%w: mixed request and response", ErrProtocol)
		}
		if hasID {
			if _, err = ParseID(idRaw); err != nil {
				return err
			}
			return fmt.Errorf("%w: %s", ErrUnsupportedRequest, method)
		}
		c.mu.Lock()
		c.diag.Notifications = saturatingAdd(c.diag.Notifications, 1)
		c.mu.Unlock()
		return nil
	}
	if !hasID || hasResult == hasError {
		return fmt.Errorf("%w: expected one result or error and a request ID", ErrProtocol)
	}
	if _, exists := f["params"]; exists {
		return fmt.Errorf("%w: response has params", ErrProtocol)
	}
	id, err := ParseID(idRaw)
	if err != nil {
		return err
	}
	var rpcErr *RPCError
	if hasError {
		e, err := objectFields(errorRaw)
		if err != nil {
			return err
		}
		code, ok := e["code"]
		if !ok {
			return fmt.Errorf("%w: missing error code", ErrProtocol)
		}
		message, ok := e["message"]
		if !ok || len(message) == 0 || message[0] != '"' {
			return fmt.Errorf("%w: invalid error message", ErrProtocol)
		}
		rpcErr = &RPCError{Data: append(json.RawMessage(nil), e["data"]...), Raw: append(json.RawMessage(nil), errorRaw...)}
		if rpcErr.Code, err = strconv.ParseInt(string(code), 10, 64); err != nil {
			return fmt.Errorf("%w: invalid error code", ErrProtocol)
		}
		if err = json.Unmarshal(message, &rpcErr.Message); err != nil {
			return fmt.Errorf("%w: invalid error message", ErrProtocol)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.pending[id]
	if !ok {
		c.diag.UnmatchedResponses = saturatingAdd(c.diag.UnmatchedResponses, 1)
		return nil
	}
	// Responses to queued-but-unwritten IDs cannot settle a future request.
	if !p.started {
		return fmt.Errorf("%w: response arrived before request dispatch", ErrProtocol)
	}
	if rpcErr != nil {
		c.finishLocked(p, nil, &CallError{Method: p.method, ID: p.id, Dispatch: Responded, Cause: rpcErr})
	} else {
		c.finishLocked(p, append(json.RawMessage(nil), result...), nil)
	}
	return nil
}
