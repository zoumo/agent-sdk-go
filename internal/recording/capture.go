package recording

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type Frame struct {
	Ordinal   int             `json:"ordinal"`
	Direction string          `json:"direction"`
	Channel   string          `json:"channel"`
	Epoch     int             `json:"epoch"`
	ElapsedMS int64           `json:"elapsedMs"`
	Wire      json.RawMessage `json:"wire"`
}

const ProtocolLimit = 2 << 20
const StderrLimit = 256 << 10

// Capture is used only by the explicitly selected recording proxy. Raw files
// are private temporary evidence; they are never embedded or published directly.
type Capture struct {
	mu      sync.Mutex
	out     io.Writer
	start   time.Time
	ordinal int
	bytes   int
	limit   int
}

func NewCapture(w io.Writer, limit int) *Capture {
	return &Capture{out: w, start: time.Now(), limit: limit}
}
func (c *Capture) Frame(direction string, b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(b)+1 > c.limit-c.bytes {
		return errors.New("capture protocol byte limit")
	}
	if !json.Valid(b) {
		return errors.New("non-JSON protocol frame")
	}
	c.bytes += len(b) + 1
	c.ordinal++
	channel := "protocol"
	var m map[string]json.RawMessage
	_ = json.Unmarshal(b, &m)
	if m["id"] != nil && m["method"] != nil && direction == "s2c" {
		channel = "control"
	}
	if m["id"] != nil && m["method"] == nil && direction == "c2s" {
		channel = "control"
	}
	return json.NewEncoder(c.out).Encode(Frame{Ordinal: c.ordinal, Direction: direction, Channel: channel, Epoch: 1, ElapsedMS: time.Since(c.start).Milliseconds(), Wire: append(json.RawMessage(nil), b...)})
}
func copyFrames(in io.Reader, out io.Writer, c *Capture, direction string) error {
	s := bufio.NewScanner(in)
	s.Buffer(make([]byte, 4096), ProtocolLimit)
	for s.Scan() {
		b := append([]byte(nil), s.Bytes()...)
		if err := c.Frame(direction, b); err != nil {
			return err
		}
		if _, err := out.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	return s.Err()
}

type ProxyConfig struct {
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
	Env        []string `json:"-"`
	Dir        string   `json:"dir"`
	RawDir     string   `json:"rawDir"`
	Seconds    int      `json:"seconds"`
}
type TransportResult struct {
	Reason          string `json:"reason"`
	Milliseconds    int64  `json:"milliseconds"`
	Reaped          bool   `json:"reaped"`
	CleanupComplete bool   `json:"cleanupComplete"`
	ProtocolBytes   int    `json:"protocolBytes"`
	StderrBytes     int    `json:"stderrBytes"`
}

// Proxy owns one fresh native process group. It terminates only that group and
// reaps its native child; no daemon sockets or user processes are discovered.
func Proxy(cfg ProxyConfig, in io.Reader, out io.Writer) (result TransportResult, ret error) {
	start := time.Now()
	result.Reason = "EOF"
	raw, err := os.OpenFile(cfg.RawDir+"/raw.jsonl", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return result, err
	}
	defer raw.Close()
	stderr, err := os.OpenFile(cfg.RawDir+"/stderr.raw", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return result, err
	}
	defer stderr.Close()
	capture := NewCapture(raw, ProtocolLimit)
	cmd := exec.Command(cfg.Executable, cfg.Args...)
	cmd.Env = cfg.Env
	cmd.Dir = cfg.Dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cin, err := cmd.StdinPipe()
	if err != nil {
		return result, err
	}
	cout, err := cmd.StdoutPipe()
	if err != nil {
		return result, err
	}
	cerr, err := cmd.StderrPipe()
	if err != nil {
		return result, err
	}
	if err = cmd.Start(); err != nil {
		return result, err
	}
	done := make(chan error, 3)
	go func() { e := copyFrames(in, cin, capture, "c2s"); _ = cin.Close(); done <- e }()
	go func() { done <- copyFrames(cout, out, capture, "s2c") }()
	stderrCount := make(chan int, 1)
	go func() {
		n, e := io.Copy(stderr, io.LimitReader(cerr, StderrLimit+1))
		stderrCount <- int(n)
		if n > StderrLimit {
			e = errors.New("capture stderr byte limit")
		}
		done <- e
	}()
	timer := time.NewTimer(time.Duration(cfg.Seconds) * time.Second)
	defer timer.Stop()
	completedWorkers := 0
	select {
	case err = <-done:
		completedWorkers = 1
		if err != nil {
			result.Reason = "transport-error"
			ret = errors.New("capture transport failed")
		}
	case <-timer.C:
		result.Reason = "deadline"
		ret = context.DeadlineExceeded
	}
	// EOF on SDK stdin is a request to close this owned session. Termination is
	// bounded even if a descendant retained a pipe or the provider stopped reading.
	_ = cin.Close()
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case <-waited:
		result.Reaped = true
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-waited:
			result.Reaped = true
		case <-time.After(time.Second):
			result.Reason = "cleanup-failed"
			ret = errors.New("owned process not reaped")
		}
	}
	// Clean only remaining descendants in the owned group, never the security
	// receiver or pre-existing daemon to which an owned hook might have connected.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cout.Close()
	_ = cerr.Close()
	if closer, ok := in.(io.Closer); ok {
		_ = closer.Close()
	}
	drained := 0
	cleanupTimer := time.NewTimer(time.Second)
	defer cleanupTimer.Stop()
	remainingWorkers := 3 - completedWorkers
	for drained < remainingWorkers {
		select {
		case <-done:
			drained++
		case <-cleanupTimer.C:
			result.Reason = "cleanup-failed"
			ret = errors.New("capture workers not drained")
			drained = -1
		}
		if drained < 0 {
			break
		}
	}
	result.CleanupComplete = result.Reaped && drained == remainingWorkers
	select {
	case result.StderrBytes = <-stderrCount:
	case <-time.After(time.Second):
		ret = errors.New("stderr drain failed")
	}
	capture.mu.Lock()
	result.ProtocolBytes = capture.bytes
	capture.mu.Unlock()
	result.Milliseconds = time.Since(start).Milliseconds()
	b, _ := json.MarshalIndent(result, "", "  ")
	if e := os.WriteFile(cfg.RawDir+"/transport.json", append(b, '\n'), 0600); e != nil {
		return result, e
	}
	return result, ret
}

func Guard(mode string) error {
	if mode != "record" || os.Getenv("SDK_REAL_AGENT_E2E") != "1" {
		return fmt.Errorf("live recording requires explicit record mode and SDK_REAL_AGENT_E2E=1")
	}
	if os.Getenv("CI") != "" {
		return errors.New("live recording refused in CI")
	}
	return nil
}
