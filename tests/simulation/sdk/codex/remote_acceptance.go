package codexsim

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/zoumo/agent-sdk-go/codex"
	recording "github.com/zoumo/agent-sdk-go/codex/tests/replay"
	"github.com/zoumo/agent-sdk-go/codex/types"
)

type remoteRPC struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
}

func remoteRequests(path string) ([]remoteRPC, map[string]json.RawMessage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	frames, err := recording.Decode(f)
	_ = f.Close()
	if err != nil {
		return nil, nil, err
	}
	var requests []remoteRPC
	responses := map[string]json.RawMessage{}
	for _, frame := range frames {
		var rpc remoteRPC
		if err := json.Unmarshal(frame.Wire, &rpc); err != nil {
			return nil, nil, err
		}
		if frame.Direction == "c2s" {
			requests = append(requests, rpc)
		} else if len(rpc.ID) > 0 && len(rpc.Result) > 0 {
			responses[string(rpc.ID)] = rpc.Result
		}
	}
	return requests, responses, nil
}

// AcceptRemoteClose runs NewRemote against one owned offline WebSocket service.
// That service only forwards bytes to the Go protocol mock; it contains no
// protocol matcher. These are SDK simulation results, not native remote E2E.
// The caller verifies the case provenance before invoking this adapter.
func AcceptRemoteClose(ctx context.Context, root, executable, outputDir string) (err error) {
	paths := []string{filepath.Join(root, "raw/raw.jsonl"), filepath.Join(root, "remote-probe/raw/raw.jsonl")}
	receipts := []string{filepath.Join(outputDir, "main-peer.json"), filepath.Join(outputDir, "probe-peer.json")}
	mainRequests, mainResponses, err := remoteRequests(paths[0])
	if err != nil {
		return err
	}
	probeRequests, probeResponses, err := remoteRequests(paths[1])
	if err != nil {
		return err
	}
	if len(mainRequests) != 4 || mainRequests[0].Method != "initialize" || mainRequests[1].Method != "initialized" || mainRequests[2].Method != "thread/start" || mainRequests[3].Method != "turn/start" {
		return errors.New("remote close recording must have one successful setup execution")
	}
	if len(probeRequests) != 3 || probeRequests[0].Method != "initialize" || probeRequests[1].Method != "initialized" || probeRequests[2].Method != "thread/read" {
		return errors.New("remote probe must independently initialize and read")
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(secret[:])
	var connections atomic.Int32
	type phaseResult struct {
		index int
		err   error
	}
	done := make(chan phaseResult, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		index := int(connections.Add(1)) - 1
		if index >= len(paths) {
			w.WriteHeader(http.StatusGone)
			return
		}
		conn, e := websocket.Accept(w, r, nil)
		if e != nil {
			done <- phaseResult{index, e}
			return
		}
		defer conn.CloseNow()
		e = forwardRecordedSocket(ctx, conn, executable, paths[index], receipts[index], filepath.Join(outputDir, fmt.Sprintf("phase-%d-stderr.private", index)))
		done <- phaseResult{index, e}
	}))
	defer server.Close()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	open := func(rpc remoteRPC) (*codex.Client, error) {
		var init types.InitializeParams
		if err := json.Unmarshal(rpc.Params, &init); err != nil {
			return nil, err
		}
		return codex.NewRemote(ctx, codex.RemoteOptions{URL: endpoint, BearerToken: token, Initialize: init})
	}
	check := func(value any, wanted json.RawMessage) error {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if !equal(canonical(encoded), canonical(wanted)) {
			return errors.New("remote SDK result differs from the original native recording")
		}
		return nil
	}
	awaitPhase := func(index int) error {
		select {
		case result := <-done:
			if result.index != index {
				return errors.New("remote phase order differs")
			}
			if result.err != nil {
				return result.err
			}
			return checkRemoteReceipt(receipts[index], paths[index])
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	first, err := open(mainRequests[0])
	if err != nil {
		return err
	}
	defer first.Close()
	if err := check(first.ServerInfo(), mainResponses[string(mainRequests[0].ID)]); err != nil {
		return err
	}
	var start types.ThreadStartParams
	if err := json.Unmarshal(mainRequests[2].Params, &start); err != nil {
		return err
	}
	session, err := codex.Open(ctx, codex.SessionOptions{Client: first, Thread: start})
	if err != nil {
		return err
	}
	started, ok := session.StartResponse()
	if !ok {
		return errors.New("remote Session omitted its start response")
	}
	if err := check(started, mainResponses[string(mainRequests[2].ID)]); err != nil {
		return err
	}
	var turn types.TurnStartParams
	if err := json.Unmarshal(mainRequests[3].Params, &turn); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(mainRequests[3].Params, &fields); err != nil {
		return err
	}
	if len(fields) != 2 || turn.ThreadID != session.ThreadID() {
		return errors.New("unsupported remote setup input options")
	}
	submitted, err := session.Submit(ctx, codex.Input{Input: turn.Input})
	if err != nil {
		return err
	}
	if err := check(submitted.Response, mainResponses[string(mainRequests[3].ID)]); err != nil {
		return err
	}
	for {
		received, err := session.Receive(ctx)
		if err != nil {
			return err
		}
		if received.Request != nil {
			return errors.New("unexpected permission in remote close setup")
		}
		if received.Event == nil {
			continue
		}
		if received.Event.DecodeError != nil {
			return received.Event.DecodeError
		}
		if received.Event.Method != "turn/completed" {
			continue
		}
		var result types.TurnCompletedNotification
		if err := json.Unmarshal(received.Event.Params, &result); err != nil {
			return err
		}
		if result.ThreadID != session.ThreadID() || result.Turn.ID != submitted.Response.Turn.ID || result.Turn.Status != types.TurnStatusCompleted {
			return errors.New("remote setup terminal is unsuccessful or has crossed identity")
		}
		break
	}
	if err := first.Close(); err != nil {
		return err
	}
	if _, err := session.Submit(ctx, codex.Input{Input: turn.Input}); !errors.Is(err, codex.ErrClosed) {
		return errors.New("closed remote Session did not reject input")
	}
	if err := awaitPhase(0); err != nil {
		return err
	}
	second, err := open(probeRequests[0])
	if err != nil {
		return err
	}
	defer second.Close()
	if err := check(second.ServerInfo(), probeResponses[string(probeRequests[0].ID)]); err != nil {
		return err
	}
	var read types.ThreadReadParams
	if err := json.Unmarshal(probeRequests[2].Params, &read); err != nil {
		return err
	}
	if read.ThreadID != session.ThreadID() {
		return errors.New("probe is not the same remote thread")
	}
	result, err := second.ReadThread(ctx, read)
	if err != nil {
		return err
	}
	if err := check(result, probeResponses[string(probeRequests[2].ID)]); err != nil {
		return err
	}
	if err := second.Close(); err != nil {
		return err
	}
	if err := awaitPhase(1); err != nil {
		return err
	}
	if connections.Load() != 2 {
		return errors.New("remote close used an unexpected number of connections")
	}
	return nil
}

func checkRemoteReceipt(path, raw string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var receipt struct {
		FullConsumption, ClientEOF, NativeLaunched bool
		RecordingSHA256, Peer, Provider            string
	}
	if err := json.Unmarshal(b, &receipt); err != nil {
		return err
	}
	b, err = os.ReadFile(raw)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(b)
	if !receipt.FullConsumption || !receipt.ClientEOF || receipt.NativeLaunched || receipt.Peer != "Go native mock" || receipt.Provider != "codex" || receipt.RecordingSHA256 != hex.EncodeToString(digest[:]) {
		return errors.New("remote mock receipt is incomplete or mismatched")
	}
	return nil
}

func forwardRecordedSocket(ctx context.Context, conn *websocket.Conn, executable, raw, receipt, stderrPath string) error {
	cmd := exec.CommandContext(ctx, executable, "mock", "--provider", "codex", "--scenario", raw, "--receipt", receipt)
	cmd.Env = []string{}
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
	logErr := os.WriteFile(stderrPath, stderr.Bytes(), 0600)
	return errors.Join(inputErr, outputErr, processErr, logErr)
}
