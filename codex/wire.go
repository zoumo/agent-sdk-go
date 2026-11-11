package codex

import (
	"bytes"
	"encoding/json"
	"errors"
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
	for {
		frame, err := c.transport.ReadFrame()
		if err != nil {
			if errors.Is(err, errStreamReaderClosed) {
				return
			}
			input := c.transport.StreamInput()
			if input != nil && errors.Is(err, io.EOF) {
				c.mu.Lock()
				closing := c.closing
				c.mu.Unlock()
				if input.InputHalfClosed() || closing {
					return
				}
				c.fail(fmt.Errorf("%w: peer closed before SDK input", ErrUnexpectedEOF))
			} else {
				c.fail(err)
			}
			return
		}
		if err := c.routeFrame(frame); err != nil {
			c.fail(err)
			return
		}
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
		if err = json.Unmarshal(methodRaw, &method); err != nil || method == "" || !validStringEscapes(methodRaw) {
			return fmt.Errorf("%w: invalid method", ErrProtocol)
		}
		if hasResult || hasError {
			return fmt.Errorf("%w: mixed request and response", ErrProtocol)
		}
		if hasID {
			id, err := ParseID(idRaw)
			if err != nil {
				return err
			}
			return c.receiveRequest(id, method, f["params"], raw)
		}
		c.mu.Lock()
		c.diag.Notifications = saturatingAdd(c.diag.Notifications, 1)
		if method == "serverRequest/resolved" {
			c.resolveRequestLocked(f["params"])
		}
		c.publishLocked(method, f["params"], raw)
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
		if p.accept != nil {
			if err := p.accept(result); err != nil {
				c.finishLocked(p, nil, &CallError{Method: p.method, ID: p.id, Dispatch: Responded, Cause: err})
				return nil
			}
		}
		c.finishLocked(p, append(json.RawMessage(nil), result...), nil)
	}
	return nil
}
