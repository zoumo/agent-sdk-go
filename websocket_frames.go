package codex

import (
	"encoding/binary"
	"io"
	"net/http"
)

// The WebSocket dependency handles fragmentation, controls and message bounds.
// Its client reader tolerates server masks and nonminimal length encodings. The
// pinned server emits canonical unmasked frames; reject these RFC 6455 framing
// violations before JSON routing, even when their payload is otherwise valid.
type remoteRoundTripper struct{ transport *http.Transport }

func (t remoteRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.transport.RoundTrip(request)
	if err == nil && response.StatusCode == http.StatusSwitchingProtocols {
		if connection, ok := response.Body.(io.ReadWriteCloser); ok {
			response.Body = &serverFrameGuard{ReadWriteCloser: connection}
		}
	}
	return response, err
}

type serverFrameGuard struct {
	io.ReadWriteCloser
	header    [10]byte
	used      int
	need      int
	remaining uint64
	failed    bool
}

func (r *serverFrameGuard) Read(p []byte) (int, error) {
	if r.failed {
		return 0, ErrProtocol
	}
	n, err := r.ReadWriteCloser.Read(p)
	// Deliver preceding valid frames even when TCP coalesces a bad header
	// behind them. Once detected, the framing failure is permanent.
	safePrefix := 0
	reject := func() (int, error) {
		r.failed = true
		return safePrefix, ErrProtocol
	}
	for offset := 0; offset < n; {
		if r.remaining > 0 {
			take := uint64(n - offset)
			if take > r.remaining {
				take = r.remaining
			}
			offset += int(take)
			r.remaining -= take
			continue
		}
		if r.need == 0 {
			safePrefix = offset
			r.need = 2
		}
		take := r.need - r.used
		if take > n-offset {
			take = n - offset
		}
		copy(r.header[r.used:], p[offset:offset+take])
		r.used += take
		offset += take
		if r.used < r.need {
			continue
		}
		code := r.header[1] & 0x7f
		if r.need == 2 {
			if r.header[1]&0x80 != 0 {
				return reject()
			}
			switch code {
			case 126:
				r.need = 4
				continue
			case 127:
				r.need = 10
				continue
			}
		}
		size := uint64(code)
		if r.need == 4 {
			size = uint64(binary.BigEndian.Uint16(r.header[2:4]))
			if size < 126 {
				return reject()
			}
		} else if r.need == 10 {
			size = binary.BigEndian.Uint64(r.header[2:10])
			if size < 65536 || size>>63 != 0 {
				return reject()
			}
		}
		r.remaining = size
		r.used = 0
		r.need = 0
	}
	return n, err
}
