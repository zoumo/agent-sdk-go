package codex

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/coder/websocket"
)

// NewRemote opens exactly one WS/WSS connection, completes initialize, and
// writes initialized before returning. Context and CallTimeout bound the whole
// construction. Cancellation after construction does not close the client.
// Reconnecting requires a new explicit NewRemote call and initialization.
func NewRemote(ctx context.Context, options RemoteOptions) (*Client, error) {
	if ctx == nil {
		return nil, errors.New("codex: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limits, err := options.Limits.normalized()
	if err != nil {
		return nil, err
	}
	if int64(limits.MaxFrameBytes) == math.MaxInt64 {
		return nil, errors.New("codex: remote frame limit is too large")
	}
	u, err := url.Parse(options.URL)
	// Never quote a URL or a parser error: either may contain credentials.
	if err != nil || u == nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Hostname() == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(options.URL, "#") {
		return nil, errors.New("codex: remote URL requires ws/wss and no user info, query, or fragment")
	}
	if !validBearer(options.BearerToken) {
		return nil, errors.New("codex: explicit valid bearer token is required")
	}
	if strings.Contains(options.URL, options.BearerToken) || strings.Contains(u.Path, options.BearerToken) {
		return nil, errors.New("codex: bearer token must not appear in remote URL")
	}
	if u.Scheme == "ws" && !options.AllowInsecureWS && !loopbackHost(u.Hostname()) {
		return nil, errors.New("codex: non-loopback ws requires AllowInsecureWS; prefer wss")
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if options.TLSConfig != nil {
		config = options.TLSConfig.Clone()
		if config.InsecureSkipVerify {
			return nil, errors.New("codex: TLS certificate verification cannot be disabled")
		}
		if config.MinVersion < tls.VersionTLS12 {
			config.MinVersion = tls.VersionTLS12
		}
		if config.RootCAs != nil {
			config.RootCAs = config.RootCAs.Clone()
		}
	}
	ctx, cancel := context.WithTimeout(ctx, limits.CallTimeout)
	defer cancel()
	// No ambient proxy or default-client mutation. Redirects are refused even
	// on the same origin, before a second Authorization header can be sent.
	dialer := &net.Dialer{Timeout: limits.CallTimeout}
	var dialMu sync.Mutex
	var stopDialWatchers []func() bool
	stopWatchingDial := func() {
		dialMu.Lock()
		defer dialMu.Unlock()
		for _, stop := range stopDialWatchers {
			stop()
		}
		stopDialWatchers = nil
	}
	defer stopWatchingDial()
	dialContext := func(dialCtx context.Context, network, address string) (net.Conn, error) {
		conn, err := dialer.DialContext(dialCtx, network, address)
		if err != nil {
			return nil, err
		}
		if u.Scheme == "ws" && !options.AllowInsecureWS {
			address, ok := conn.RemoteAddr().(*net.TCPAddr)
			if !ok || !address.IP.IsLoopback() {
				_ = conn.Close()
				return nil, ErrRemoteTransport
			}
		}
		// A malformed HTTP 101 upgrade can make a WebSocket library drain a
		// hijacked body after net/http has detached request cancellation. Keep
		// the actual socket deadline-bound until Dial finishes, then detach.
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		dialMu.Lock()
		stopDialWatchers = append(stopDialWatchers, stop)
		dialMu.Unlock()
		return conn, nil
	}
	transport := &http.Transport{DialContext: dialContext, TLSClientConfig: config, TLSHandshakeTimeout: limits.CallTimeout, ResponseHeaderTimeout: limits.CallTimeout, MaxResponseHeaderBytes: 64 << 10}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: remoteRoundTripper{transport}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	conn, response, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"Authorization": {"Bearer " + options.BearerToken}}, CompressionMode: websocket.CompressionDisabled})
	stopWatchingDial()
	if err == nil && ctx.Err() != nil {
		_ = conn.CloseNow()
		err = ctx.Err()
	}
	if err != nil {
		cause := ctx.Err()
		var networkError net.Error
		if cause == nil && errors.Is(err, context.Canceled) {
			cause = context.Canceled
		}
		if cause == nil && (errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout())) {
			cause = context.DeadlineExceeded
		}
		if cause != nil {
			return nil, fmt.Errorf("%w: dial: %w", ErrRemoteTransport, cause)
		}
		if response != nil {
			return nil, fmt.Errorf("%w: handshake HTTP status %d", ErrRemoteTransport, response.StatusCode)
		}
		return nil, fmt.Errorf("%w: dial or TLS handshake failed", ErrRemoteTransport)
	}
	conn.SetReadLimit(int64(limits.MaxFrameBytes))
	c := newClient(limits)
	c.remote = conn
	c.remoteToken = options.BearerToken
	c.workers.Add(2)
	go func() { defer c.workers.Done(); defer close(c.readDone); c.readLoop() }()
	go func() { defer c.workers.Done(); c.writeLoop() }()
	go c.cleanup()
	return c.initialize(ctx, options.Initialize)
}

func validBearer(s string) bool {
	if s == "" {
		return false
	}
	padding := false
	for _, r := range s {
		if r == '=' {
			padding = true
			continue
		}
		if padding || !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("-._~+/", r)) {
			return false
		}
	}
	return s[0] != '='
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *Client) writeFrame(ctx context.Context, frame []byte) error {
	if c.remote != nil {
		// The shared writer owns cancellation and dispatch classification. A call
		// may receive its response before Write returns; its deferred context
		// cancellation must not tear down an otherwise successful connection.
		if err := c.remote.Write(context.Background(), websocket.MessageText, frame); err != nil {
			// Close reasons and library errors may contain peer-controlled text,
			// including reflected headers. Expose only safe classification.
			if ctx.Err() != nil {
				return fmt.Errorf("%w: write: %w", ErrRemoteTransport, ctx.Err())
			}
			return fmt.Errorf("%w: write: %w", ErrRemoteTransport, io.EOF)
		}
		return nil
	}
	frame = append(frame, '\n')
	n, err := c.stdin.Write(frame)
	if err == nil && n != len(frame) {
		err = io.ErrShortWrite
	}
	return err
}

func (c *Client) readRemote() {
	for {
		kind, raw, err := c.remote.Read(context.Background())
		if err != nil {
			var networkError net.Error
			var closeError websocket.CloseError
			switch {
			case errors.As(err, &closeError) && !utf8.ValidString(closeError.Reason):
				c.fail(fmt.Errorf("%w: invalid WebSocket close reason", ErrProtocol))
			case errors.Is(err, websocket.ErrMessageTooBig):
				c.fail(fmt.Errorf("%w: %w", ErrProtocol, ErrFrameTooLarge))
			case websocket.CloseStatus(err) == websocket.StatusProtocolError || websocket.CloseStatus(err) == websocket.StatusInvalidFramePayloadData:
				c.fail(fmt.Errorf("%w: invalid WebSocket frame", ErrProtocol))
			case websocket.CloseStatus(err) >= 0, errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, net.ErrClosed), errors.As(err, &networkError):
				c.fail(fmt.Errorf("%w: read: %w", ErrRemoteTransport, io.EOF))
			default:
				c.fail(fmt.Errorf("%w: invalid WebSocket framing", ErrProtocol))
			}
			return
		}
		if len(raw) > c.limits.MaxFrameBytes {
			c.fail(fmt.Errorf("%w: %w", ErrProtocol, ErrFrameTooLarge))
			return
		}
		if kind != websocket.MessageText {
			c.fail(fmt.Errorf("%w: expected WebSocket text message", ErrProtocol))
			return
		}
		if err := c.routeFrame(raw); err != nil {
			c.fail(err)
			return
		}
	}
}
