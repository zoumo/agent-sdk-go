package mocktest

import (
	"io"
	"sync"
)

const maxDiagnosticBytes = 64 << 10

// diagnosticStream lets the mock write diagnostics without requiring a
// concurrent consumer. Reads still observe data incrementally and receive EOF
// when the run closes the stream.
type diagnosticStream struct {
	mu     sync.Mutex
	cond   *sync.Cond
	data   []byte
	closed bool
}

func newDiagnosticStream() *diagnosticStream {
	stream := &diagnosticStream{}
	stream.cond = sync.NewCond(&stream.mu)
	return stream
}

func (s *diagnosticStream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, io.ErrClosedPipe
	}
	s.data = append(s.data, p...)
	if excess := len(s.data) - maxDiagnosticBytes; excess > 0 {
		s.data = append([]byte(nil), s.data[excess:]...)
	}
	s.cond.Broadcast()
	return len(p), nil
}

func (s *diagnosticStream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.data) == 0 && !s.closed {
		s.cond.Wait()
	}
	if len(s.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.data)
	s.data = s.data[n:]
	return n, nil
}

func (s *diagnosticStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.cond.Broadcast()
	return nil
}
