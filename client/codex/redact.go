package codex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"slices"
	"strings"
)

// The only retained credential is this connection's explicitly supplied bearer,
// used to prevent a peer's reflected credential from escaping in an SDK error.
// Ordinary returned protocol data is not a logging or redaction facility.
func (c *Client) redact(text string) string {
	if c.remoteToken == "" {
		return text
	}
	for _, secret := range []string{c.remoteToken, url.QueryEscape(c.remoteToken), url.PathEscape(c.remoteToken)} {
		// The marker contains no valid bearer-token character, so a second
		// pass cannot treat the replacement itself as a reflected credential.
		text = strings.ReplaceAll(text, secret, "***")
	}
	return text
}

// Scan each original lexical value rather than decoding into a map: duplicate
// nested object keys must not conceal an earlier reflected secret. Preserve
// every unaffected byte, numeric lexeme and duplicate member without float64.
func (c *Client) redactJSON(raw json.RawMessage) json.RawMessage {
	if c.remoteToken == "" || len(raw) == 0 {
		return raw
	}
	if !json.Valid(raw) {
		return nil
	}
	var out []byte
	copied := 0
	for i := 0; i < len(raw); {
		start := i
		var text string
		switch {
		case raw[i] == '"':
			i++
			for i < len(raw) {
				if raw[i] == '\\' {
					i += 2
					continue
				}
				if raw[i] == '"' {
					i++
					break
				}
				i++
			}
			if json.Unmarshal(raw[start:i], &text) != nil {
				return nil
			}
		case raw[i] == '-' || (raw[i] >= '0' && raw[i] <= '9'):
			for i < len(raw) && strings.ContainsRune("0123456789.eE+-", rune(raw[i])) {
				i++
			}
			text = string(raw[start:i])
		default:
			i++
			continue
		}
		next := c.redact(text)
		if next != text {
			// encoding/json cannot fail for a string value.
			//nolint:errchkjson
			encoded, _ := json.Marshal(next)
			out = append(out, raw[copied:start]...)
			out = append(out, encoded...)
			copied = i
		}
	}
	if out == nil {
		return raw
	}
	return append(out, raw[copied:]...)
}

// safeError preserves public SDK error types and safe sentinel classification.
// If an arbitrary caller/decoder error reflects credentials, its private unsafe
// cause is discarded instead of remaining reachable through Unwrap or formatting.
func (c *Client) safeError(err error) error {
	if err == nil || c.remoteToken == "" {
		return err
	}
	{
		var callErr *CallError
		var answerErr *AnswerError
		var decodeErr *DecodeError
		var rpcErr *RPCError
		switch {
		case errors.As(err, &callErr):
			return &CallError{Method: c.redact(callErr.Method), ID: callErr.ID, Dispatch: callErr.Dispatch, Cause: c.safeError(callErr.Cause)}
		case errors.As(err, &answerErr):
			return &AnswerError{Method: c.redact(answerErr.Method), ID: answerErr.ID, Dispatch: answerErr.Dispatch, Cause: c.safeError(answerErr.Cause)}
		case errors.As(err, &decodeErr):
			return &DecodeError{Method: c.redact(decodeErr.Method), Cause: c.safeError(decodeErr.Cause)}
		case errors.As(err, &rpcErr):
			return &RPCError{Code: rpcErr.Code, Message: c.redact(rpcErr.Message), Data: c.redactJSON(rpcErr.Data), Raw: c.redactJSON(rpcErr.Raw)}
		}
	}
	text := err.Error()
	redacted := c.redact(text)
	if redacted == text {
		return err
	}
	safe := &redactedCause{text: redacted}
	for _, marker := range []error{ErrRemoteTransport, ErrProtocol, ErrFrameTooLarge, ErrClosed, ErrLimit, ErrWriteTimeout, ErrShutdownTimeout, ErrSubscriptionOverflow, ErrInvalidAnswerHandle, ErrRequestAnswered, ErrRequestCanceled, ErrRequestExpired, ErrRequestResolved, ErrRequestOverflow, context.Canceled, context.DeadlineExceeded, io.EOF, io.ErrUnexpectedEOF} {
		if errors.Is(err, marker) {
			safe.markers = append(safe.markers, marker)
		}
	}
	return safe
}

// Keep errors.Is classification without re-exposing a scrubbed cause through
// Unwrap, or appending potentially credential-matching text to Error().
type redactedCause struct {
	text    string
	markers []error
}

func (e *redactedCause) Error() string { return e.text }
func (e *redactedCause) Is(target error) bool {
	return slices.Contains(e.markers, target)
}
