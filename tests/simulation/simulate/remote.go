package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

type remotePeerEndpoint struct {
	URL         string `json:"url"`
	BearerToken string `json:"bearerToken"`
}

type remoteEndpoint struct {
	Connections map[string]remotePeerEndpoint `json:"connections"`
}

type mockRemoteService struct {
	endpoint    remoteEndpoint
	executionID string
	server      *http.Server
	cancel      context.CancelFunc
	served      chan error
	once        sync.Once
	mu          sync.Mutex
	closing     bool
	opened      map[string]bool
	completions map[string]error
	active      map[*websocket.Conn]bool
	handlers    sync.WaitGroup
	runs        map[string]*mocktest.Run
	err         error
}

// The runner owns the authenticated loopback service and both in-process
// replay connections. Drivers see only endpoints keyed by the declared
// connection ID; protocol bytes never pass through a helper process.
func startMockRemote(parent context.Context, mock *mocktest.Session, root string) (*mockRemoteService, error) {
	if mock == nil {
		return nil, errors.New("remote bridge requires a prepared fixture")
	}
	ids := mock.ConnectionIDs()
	if len(ids) != 2 || ids[0] != "main" || ids[1] != "probe" {
		return nil, errors.New("remote bridge requires declared main and probe connections")
	}
	nativeArgs := []string{"app-server", "--listen", "stdio://"}
	mainRun, err := mock.NewRun(parent, mocktest.RunOptions{Connection: "main", NativeArgs: nativeArgs})
	if err != nil {
		return nil, fmt.Errorf("start remote main: %w", err)
	}
	cleanupMain := true
	defer func() {
		if cleanupMain {
			_ = mainRun.Abort()
			_ = mainRun.CloseSDK()
		}
	}()
	probeRun, err := mock.NewRun(parent, mocktest.RunOptions{
		Connection:  "probe",
		ExecutionID: mainRun.Start.ExecutionID,
		NativeArgs:  nativeArgs,
	})
	if err != nil {
		return nil, fmt.Errorf("start remote probe: %w", err)
	}
	cleanupMain = false

	listener, err := (&net.ListenConfig{}).Listen(parent, "tcp", "127.0.0.1:0")
	if err != nil {
		_ = mainRun.Abort()
		_ = probeRun.Abort()
		_ = mainRun.CloseSDK()
		_ = probeRun.CloseSDK()
		return nil, err
	}
	endpoint := remoteEndpoint{Connections: make(map[string]remotePeerEndpoint, 2)}
	for _, id := range []string{"main", "probe"} {
		var secret [32]byte
		if _, err := rand.Read(secret[:]); err != nil {
			listener.Close()
			_ = mainRun.Abort()
			_ = probeRun.Abort()
			_ = mainRun.CloseSDK()
			_ = probeRun.CloseSDK()
			return nil, err
		}
		endpoint.Connections[id] = remotePeerEndpoint{
			URL:         "ws://" + listener.Addr().String() + "/" + id,
			BearerToken: hex.EncodeToString(secret[:]),
		}
	}
	ctx, cancel := context.WithCancel(parent)
	service := &mockRemoteService{
		endpoint:    endpoint,
		executionID: mainRun.Start.ExecutionID,
		cancel:      cancel,
		served:      make(chan error, 1),
		opened:      map[string]bool{},
		completions: map[string]error{},
		active:      map[*websocket.Conn]bool{},
		runs:        map[string]*mocktest.Run{"main": mainRun, "probe": probeRun},
	}
	service.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/")
		run, declared := service.runs[id]
		credentials, hasCredentials := service.endpoint.Connections[id]
		if !declared || !hasCredentials || r.URL.Path != "/"+id || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+credentials.BearerToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		service.mu.Lock()
		if service.closing {
			service.mu.Unlock()
			w.WriteHeader(http.StatusGone)
			return
		}
		service.mu.Unlock()
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			service.mu.Lock()
			service.err = errors.Join(service.err, fmt.Errorf("remote %s handshake: %w", id, err))
			service.mu.Unlock()
			return
		}
		defer func() { _ = conn.CloseNow() }()
		service.mu.Lock()
		if service.closing || service.opened[id] {
			service.err = errors.Join(service.err, fmt.Errorf("remote %s connection was repeated", id))
			service.mu.Unlock()
			return
		}
		service.opened[id] = true
		service.active[conn] = true
		service.handlers.Add(1)
		service.mu.Unlock()
		defer service.handlers.Done()
		defer func() {
			service.mu.Lock()
			delete(service.active, conn)
			service.mu.Unlock()
		}()
		runErr := forwardPeerSocket(ctx, conn, run, filepath.Join(root, "phase-"+id+"-stderr.private"))
		service.mu.Lock()
		service.completions[id] = runErr
		service.mu.Unlock()
	})}
	go func() { service.served <- service.server.Serve(listener) }()
	return service, nil
}

func (s *mockRemoteService) close() error {
	s.once.Do(func() {
		defer s.cancel()
		s.mu.Lock()
		s.closing = true
		runErr := s.err
		s.mu.Unlock()
		runErr = errors.Join(runErr, s.server.Close())
		if err := <-s.served; !errors.Is(err, http.ErrServerClosed) {
			runErr = errors.Join(runErr, err)
		}
		complete := make(chan struct{})
		go func() { s.handlers.Wait(); close(complete) }()
		abort := func() {
			s.cancel()
			for _, run := range s.runs {
				_ = run.Abort()
				_ = run.CloseSDK()
			}
			s.mu.Lock()
			for conn := range s.active {
				_ = conn.CloseNow()
			}
			s.mu.Unlock()
		}
		s.mu.Lock()
		count := len(s.opened)
		s.mu.Unlock()
		if count != 2 {
			runErr = errors.Join(runErr, fmt.Errorf("remote case opened %d/2 required connections", count))
			abort()
		}
		select {
		case <-complete:
		case <-time.After(3 * time.Second):
			runErr = errors.Join(runErr, errors.New("remote peer did not drain before cleanup"))
			abort()
			select {
			case <-complete:
			case <-time.After(3 * time.Second):
				runErr = errors.Join(runErr, errors.New("remote peer cleanup did not complete"))
			}
		}
		s.mu.Lock()
		for _, id := range []string{"main", "probe"} {
			if !s.opened[id] {
				runErr = errors.Join(runErr, fmt.Errorf("remote %s connection missing", id))
			} else {
				runErr = errors.Join(runErr, s.completions[id])
			}
		}
		s.mu.Unlock()
		s.err = runErr
	})
	return s.err
}

func forwardPeerSocket(ctx context.Context, conn *websocket.Conn, run *mocktest.Run, stderrPath string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var stderr bytes.Buffer
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(&stderr, run.Stderr)
	}()
	closeInput := run.CloseInputWhenComplete(run.Writer.Close)

	inputDone := make(chan error, 1)
	go func() {
		for {
			kind, frame, err := conn.Read(ctx)
			if err != nil {
				inputDone <- err
				return
			}
			if kind != websocket.MessageText || bytes.ContainsAny(frame, "\r\n") {
				inputDone <- errors.New("invalid SDK WebSocket frame")
				return
			}
			if _, err := run.Writer.Write(append(frame, '\n')); err != nil {
				if errors.Is(err, io.ErrClosedPipe) {
					inputDone <- nil
				} else {
					inputDone <- err
				}
				return
			}
		}
	}()
	outputDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(run.Reader)
		scanner.Buffer(make([]byte, 4096), 4<<20)
		for scanner.Scan() {
			if err := conn.Write(ctx, websocket.MessageText, scanner.Bytes()); err != nil {
				outputDone <- err
				return
			}
		}
		outputDone <- scanner.Err()
	}()

	var bridgeErr error
	select {
	case err := <-outputDone:
		if !expectableRemoteClose(err) {
			bridgeErr = errors.Join(bridgeErr, err)
		}
		_ = conn.Close(websocket.StatusNormalClosure, "")
	case err := <-inputDone:
		if !expectableRemoteClose(err) {
			bridgeErr = errors.Join(bridgeErr, err)
		}
	case <-ctx.Done():
		if !errors.Is(ctx.Err(), context.Canceled) {
			bridgeErr = errors.Join(bridgeErr, ctx.Err())
		}
	}
	cancel()
	_ = conn.CloseNow()

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 30*time.Second)
	waitErr := run.WaitError(waitCtx)
	waitCancel()
	select {
	case result := <-closeInput:
		bridgeErr = errors.Join(bridgeErr, result)
	default:
	}
	_ = run.CloseSDK()
	<-stderrDone
	_ = os.WriteFile(stderrPath, stderr.Bytes(), 0o600)
	return errors.Join(bridgeErr, waitErr)
}

func expectableRemoteClose(err error) bool {
	if err == nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway:
		return true
	default:
		return false
	}
}
