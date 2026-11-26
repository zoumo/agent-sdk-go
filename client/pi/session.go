package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/zoumo/agent-sdk-go/internal/queue"
)

// Session owns the two protocol streams and, for Open, the local process.
type Session struct {
	id          string
	input       io.WriteCloser
	output      io.ReadCloser
	process     *process
	events      queue.Queue[Event]
	writeToken  chan struct{}
	done        chan struct{}
	closeOnce   sync.Once
	closeInput  sync.Once
	closeOutput sync.Once
	writes      sync.WaitGroup

	mu       sync.Mutex
	closing  bool
	terminal error
	nextID   uint64
	pending  map[string]chan response
}

type response struct {
	command string
	data    json.RawMessage
	err     error
}

type record struct {
	ID      string          `json:"id"`
	Command string          `json:"command"`
	Success *bool           `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// Attach takes ownership of input and output and performs the same get_state
// handshake as Open. Closing each endpoint must unblock any operation on it.
func Attach(ctx context.Context, input io.WriteCloser, output io.ReadCloser, expectedID string) (*Session, error) {
	return attach(ctx, input, output, expectedID, nil)
}

func attach(ctx context.Context, input io.WriteCloser, output io.ReadCloser, expectedID string, proc *process) (*Session, error) {
	if input == nil || output == nil {
		if input != nil {
			_ = input.Close()
		}
		if output != nil {
			_ = output.Close()
		}
		return nil, errors.New("pi: both protocol streams are required")
	}
	s := &Session{
		input:      input,
		output:     output,
		process:    proc,
		done:       make(chan struct{}),
		writeToken: make(chan struct{}, 1),
		pending:    make(map[string]chan response),
	}
	s.writeToken <- struct{}{}
	go s.read()
	state, err := s.State(ctx)
	if err == nil && expectedID != "" && state.SessionID != expectedID {
		err = fmt.Errorf("pi: expected session %q, got %q", expectedID, state.SessionID)
	}
	if err != nil {
		_ = s.CloseAndDiscard()
		return nil, err
	}
	s.id = state.SessionID
	return s, nil
}

func (s *Session) ID() string            { return s.id }
func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminal
}

// Receive consumes the next native event. Already accepted events remain available after Close.
func (s *Session) Receive(ctx context.Context) (Event, error) { return s.events.Pop(ctx) }

// Close releases resources once. An owned process receives stdin EOF before forced termination.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closing = true
		s.mu.Unlock()
		s.closeWriter()
		if s.process == nil {
			s.closeReader()
		} else {
			select {
			case <-s.done:
			case <-time.After(shutdownGrace):
				s.process.kill()
				s.closeReader()
			}
		}
		<-s.done
	})
	return s.Err()
}

// CloseAndDiscard releases the session and discards any unread events.
func (s *Session) CloseAndDiscard() error {
	err := s.Close()
	_ = s.events.Abort(err)
	return err
}

func (s *Session) closeWriter() { s.closeInput.Do(func() { _ = s.input.Close() }) }
func (s *Session) closeReader() { s.closeOutput.Do(func() { _ = s.output.Close() }) }

func (s *Session) read() {
	var processExit chan error
	if s.process != nil {
		processExit = make(chan error, 1)
		go s.waitProcess(processExit)
	}

	scanner := bufio.NewScanner(s.output)
	scanner.Buffer(make([]byte, 4096), 64<<20)
	scanner.Split(splitRecord)
	var err error
	for scanner.Scan() {
		if err = s.deliver(scanner.Bytes()); err != nil {
			break
		}
	}
	if err == nil {
		err = scanner.Err()
	}
	s.mu.Lock()
	if (s.closing || s.process != nil) && (errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed)) {
		err = nil
	}
	if s.terminal != nil {
		err = s.terminal
	} else if err == nil && !s.closing {
		err = io.ErrUnexpectedEOF
	}
	s.closing = true
	s.mu.Unlock()

	s.closeWriter()
	s.closeReader()
	s.writes.Wait()
	if processExit != nil {
		timer := time.AfterFunc(shutdownGrace, s.process.kill)
		err = errors.Join(err, <-processExit)
		timer.Stop()
	}
	s.mu.Lock()
	s.terminal = err
	clear(s.pending)
	s.mu.Unlock()
	_ = s.events.Close(err)
	close(s.done)
}

func (s *Session) waitProcess(result chan<- error) {
	result <- s.process.wait()
	// A descendant can keep stdout open even after Pi has been reaped.
	select {
	case <-s.done:
	case <-time.After(shutdownGrace):
		s.closeReader()
	}
}

// fail prevents further writes when the transport or live identity can no longer be trusted.
func (s *Session) fail(cause error) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.closing = true
	s.terminal = cause
	s.mu.Unlock()
	s.closeWriter()
	s.closeReader()
}

// Pi uses strict LF framing. A partial last record is a transport failure.
func splitRecord(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, bytes.TrimSuffix(data[:i], []byte{'\r'}), nil
	}
	if atEOF && len(data) > 0 {
		return 0, nil, io.ErrUnexpectedEOF
	}
	return 0, nil, nil
}

func (s *Session) deliver(line []byte) error {
	var envelope struct{ Type string }
	if err := json.Unmarshal(line, &envelope); err != nil {
		return fmt.Errorf("pi: invalid RPC record: %w", err)
	}
	if envelope.Type == "" {
		return errors.New("pi: RPC record missing type")
	}
	if envelope.Type != "response" {
		return s.events.Push(Event{Type: envelope.Type, Raw: bytes.Clone(line)}, int64(len(line)))
	}
	var r record
	if err := json.Unmarshal(line, &r); err != nil {
		return fmt.Errorf("pi: invalid RPC response: %w", err)
	}
	if r.ID == "" || r.Command == "" || r.Success == nil {
		return errors.New("pi: malformed RPC response envelope")
	}
	result := response{command: r.Command, data: r.Data}
	if !*r.Success {
		result.err = &CommandError{Command: r.Command, Message: r.Error, Raw: bytes.Clone(line)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ch := s.pending[r.ID]; ch != nil {
		ch <- result
		delete(s.pending, r.ID)
	}
	return nil
}

// request serializes writes but never waits for another command's response.
// Once registered, a request may receive a response before the write goroutine returns.
func (s *Session) request(ctx context.Context, command string, fields map[string]any) (json.RawMessage, error) {
	if ctx == nil {
		return nil, errors.New("pi: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, io.ErrClosedPipe
	case <-s.writeToken:
	}
	s.mu.Lock()
	if s.closing || ctx.Err() != nil {
		s.mu.Unlock()
		s.writeToken <- struct{}{}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, io.ErrClosedPipe
	}
	s.nextID++
	id := strconv.FormatUint(s.nextID, 10)
	s.mu.Unlock()
	if fields == nil {
		fields = map[string]any{}
	}
	fields["id"], fields["type"] = id, command
	frame, err := json.Marshal(fields)
	if err != nil {
		s.writeToken <- struct{}{}
		return nil, err
	}
	frame = append(frame, '\n')
	ch := make(chan response, 1)
	s.mu.Lock()
	err = ctx.Err()
	if err == nil && s.closing {
		err = io.ErrClosedPipe
	}
	if err != nil {
		s.mu.Unlock()
		s.writeToken <- struct{}{}
		return nil, err
	}
	s.pending[id] = ch
	s.writes.Add(1)
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	written := s.writeFrame(frame)
	sent := false
	for {
		select {
		case reply := <-ch:
			return checkResponse(command, reply)
		case writeErr := <-written:
			if writeErr != nil {
				return nil, &DispatchError{Err: writeErr}
			}
			sent = true
			written = nil
		case <-ctx.Done():
			select {
			case reply := <-ch:
				return checkResponse(command, reply)
			default:
			}
			return nil, dispatchError(written, sent, ctx.Err())
		case <-s.done:
			select {
			case reply := <-ch:
				return checkResponse(command, reply)
			default:
			}
			cause := s.Err()
			if cause == nil {
				cause = io.ErrClosedPipe
			}
			return nil, dispatchError(written, sent, cause)
		}
	}
}

// writeFrame starts a write after its caller acquires writeToken and registers
// it with writes. Close can unblock the write through the owned input stream.
func (s *Session) writeFrame(frame []byte) <-chan error {
	written := make(chan error, 1)
	go func() {
		defer s.writes.Done()
		n, writeErr := s.input.Write(frame)
		if writeErr == nil && n != len(frame) {
			writeErr = io.ErrShortWrite
		}
		written <- writeErr
		if writeErr != nil {
			s.fail(writeErr)
		}
		s.writeToken <- struct{}{}
	}()
	return written
}

// A write may have finished before the caller selects cancellation or Done.
func dispatchError(written <-chan error, sent bool, cause error) *DispatchError {
	select {
	case err := <-written:
		sent = err == nil
	default:
	}
	return &DispatchError{Sent: sent, Err: cause}
}

func checkResponse(command string, r response) (json.RawMessage, error) {
	if r.command != command {
		return nil, &DispatchError{Sent: true, Err: fmt.Errorf("pi: expected %q response, received %q", command, r.command)}
	}
	return r.data, r.err
}
