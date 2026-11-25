package pi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
)

const extensionUIRequest = "extension_ui_request"

// UIRequest describes a UI frame emitted by an extension. Methods such as
// notify are one-way. Dialog methods select, confirm, input and editor expect
// a response with the original ID. Raw preserves fields outside this view.
type UIRequest struct {
	ID          string          `json:"id"`
	Method      string          `json:"method"`
	Title       string          `json:"title"`
	Message     string          `json:"message"`
	Options     []string        `json:"options"`
	Placeholder string          `json:"placeholder"`
	Prefill     string          `json:"prefill"`
	TimeoutMS   int64           `json:"timeout"`
	Raw         json.RawMessage `json:"-"`
}

// UIRequest decodes the original native extension UI event.
func (e Event) UIRequest() (UIRequest, error) {
	var request UIRequest
	if e.Type != extensionUIRequest {
		return request, errors.New("pi: expected an extension UI event")
	}
	if err := json.Unmarshal(e.Raw, &request); err != nil {
		return request, err
	}
	if request.ID == "" || request.Method == "" || request.TimeoutMS < 0 {
		return request, errors.New("pi: invalid extension UI request")
	}
	request.Raw = e.Raw
	return request, nil
}

// UIResponse answers a dialog. Set exactly one of Value, Confirmed or Canceled.
type UIResponse struct {
	ID        string  `json:"id"`
	Value     *string `json:"value,omitempty"`
	Confirmed *bool   `json:"confirmed,omitempty"`
	Canceled  bool    `json:"cancelled,omitempty"` //nolint:misspell // Pi wire field.
}

// RespondUI writes a native one-way response. Success confirms the complete
// frame was written. Pi may already have dropped the request and does not ack.
func (s *Session) RespondUI(ctx context.Context, response UIResponse) error {
	if ctx == nil {
		return errors.New("pi: nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if response.ID == "" || !response.validPayload() {
		return errors.New("pi: invalid extension UI response")
	}
	frame, err := json.Marshal(struct {
		Type string `json:"type"`
		UIResponse
	}{Type: "extension_ui_response", UIResponse: response})
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return io.ErrClosedPipe
	case <-s.writeToken:
	}
	s.mu.Lock()
	err = ctx.Err()
	if err == nil && s.closing {
		err = io.ErrClosedPipe
	}
	if err != nil {
		s.mu.Unlock()
		s.writeToken <- struct{}{}
		return err
	}
	s.writes.Add(1)
	s.mu.Unlock()
	written := s.writeFrame(append(frame, '\n'))
	select {
	case writeErr := <-written:
		if writeErr != nil {
			return &DispatchError{Err: writeErr}
		}
		return nil
	case <-ctx.Done():
		return dispatchError(written, false, ctx.Err())
	case <-s.done:
		cause := s.Err()
		if cause == nil {
			cause = io.ErrClosedPipe
		}
		return dispatchError(written, false, cause)
	}
}

func (r UIResponse) validPayload() bool {
	if r.Canceled {
		return r.Value == nil && r.Confirmed == nil
	}
	return (r.Value == nil) != (r.Confirmed == nil)
}
