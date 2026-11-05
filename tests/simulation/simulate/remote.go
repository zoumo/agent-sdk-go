package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
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
	err         error
}

// The runner owns the offline service and its protocol children. Drivers see
// only endpoints keyed by the declared connection ID. The URL path and bearer
// token jointly select main or probe, so arrival order never grants identity.
func startMockRemote(parent context.Context, peers map[string]command, root string) (*mockRemoteService, error) {
	if len(peers) != 2 || peers["main"].Command == "" || peers["probe"].Command == "" {
		return nil, errors.New("remote bridge requires declared main and probe peers")
	}
	listener, err := (&net.ListenConfig{}).Listen(parent, "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	endpoint := remoteEndpoint{Connections: make(map[string]remotePeerEndpoint, len(peers))}
	for _, id := range []string{"main", "probe"} {
		var secret [32]byte
		if _, err := rand.Read(secret[:]); err != nil {
			listener.Close()
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
		cancel:      cancel,
		served:      make(chan error, 1),
		opened:      map[string]bool{},
		completions: map[string]error{},
		active:      map[*websocket.Conn]bool{},
	}
	service.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/")
		peer, declared := peers[id]
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
		conn, e := websocket.Accept(w, r, nil)
		if e != nil {
			service.mu.Lock()
			service.err = errors.Join(service.err, fmt.Errorf("remote %s handshake: %w", id, e))
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
		runErr := forwardPeerSocket(ctx, conn, peer, filepath.Join(root, "phase-"+id+"-stderr.private"))
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

func forwardPeerSocket(ctx context.Context, conn *websocket.Conn, peer command, stderrPath string) error {
	cmd := exec.CommandContext(ctx, peer.Command, peer.Args...)
	cmd.Env = peer.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.Dir = peer.Dir
	cmd.WaitDelay = 3 * time.Second
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Cancellation owns the entire peer tree, including the lifetime wrapper
	// and mock process. Killing only the wrapper would orphan the mock.
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	defer output.Close()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()
	// A descendant can retain stdout after the direct peer exits. Cancellation
	// must unblock pipe readers before Wait can reap the direct peer; waiting
	// for natural EOF here can otherwise strand a zombie until cleanup times out.
	canceled := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		defer close(canceled)
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = input.Close()
		_ = output.Close()
		_ = conn.CloseNow()
	})
	defer func() {
		if !stopCancel() {
			<-canceled
		}
	}()
	readDone := make(chan error, 1)
	go func() {
		for {
			kind, frame, err := conn.Read(ctx)
			if err != nil {
				readDone <- nil
				return
			}
			if kind != websocket.MessageText || bytes.ContainsAny(frame, "\r\n") {
				readDone <- errors.New("invalid SDK WebSocket frame")
				return
			}
			if _, err := input.Write(append(frame, '\n')); err != nil {
				readDone <- err
				return
			}
		}
	}()
	writeDone := make(chan error, 1)
	go func() {
		scan := bufio.NewScanner(output)
		scan.Buffer(make([]byte, 4096), 4<<20)
		for scan.Scan() {
			if err := conn.Write(ctx, websocket.MessageText, scan.Bytes()); err != nil {
				writeDone <- err
				return
			}
		}
		writeDone <- scan.Err()
	}()
	var inputErr, outputErr error
	select {
	case inputErr = <-readDone:
		_ = input.Close()
		outputErr = <-writeDone
	case outputErr = <-writeDone:
		_ = conn.CloseNow()
		inputErr = <-readDone
		_ = input.Close()
	}
	processErr := cmd.Wait()
	logErr := os.WriteFile(stderrPath, stderr.Bytes(), 0o600)
	return errors.Join(inputErr, outputErr, processErr, logErr)
}
