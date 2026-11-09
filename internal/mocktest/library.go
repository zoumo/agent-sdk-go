package mocktest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	agenttestify "github.com/zoumo/agent-sdk-testify"
)

const lifecycleTimeout = 30 * time.Second

// Session is one prepared in-process testify fixture. Callers start a Run for
// each declared connection, construct the SDK against run.Start, and remove the
// fixture directory after every run has reached a terminal state.
type Session struct {
	Dir         string
	Workspace   string
	ProtocolCwd string
	Env         []string
	SessionID   string

	fixture *agenttestify.Fixture
	options Options
	owned   bool

	mu         sync.Mutex
	closed     bool
	executions []string
	active     map[*Run]struct{}
}

// RunOptions selects one declared in-process mock connection.
type RunOptions struct {
	Connection  string
	ExecutionID string
	Timeout     time.Duration
	NativeArgs  []string
}

// Run exposes the SDK-side endpoints and the testify run handle for one
// connection. Reader is peer-to-SDK and Writer is SDK-to-peer.
type Run struct {
	Start  agenttestify.ExecutionStart
	Reader io.ReadCloser
	Writer io.WriteCloser
	Stderr io.ReadCloser

	session   *Session
	mock      *agenttestify.MockRun
	closeOnce sync.Once
	closeErr  error
}

type classifiedPeerReader struct {
	io.ReadCloser
}

func (r classifiedPeerReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if err != nil && strings.Contains(err.Error(), "scripted disconnect") {
		err = errors.Join(ErrDisconnected, err)
	}
	return n, err
}

type idempotentPipeFile struct {
	*os.File
	once sync.Once
	err  error
}

func (f *idempotentPipeFile) Close() error {
	f.once.Do(func() { f.err = f.File.Close() })
	return f.err
}

// SessionReport preserves the SDK bridge's aggregate report view without
// re-parsing execution files.
type SessionReport struct {
	SchemaVersion int               `json:"schemaVersion"`
	SessionID     string            `json:"sessionId"`
	Status        string            `json:"status"`
	Executions    []ExecutionReport `json:"executions"`
}

type ExecutionReport struct {
	ID          string             `json:"id"`
	Command     string             `json:"command"`
	Status      string             `json:"status"`
	Connections []ConnectionReport `json:"connections"`
}

type ConnectionReport struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Prepare imports authored synthetic material through the public in-process
// recording lifecycle. It starts no testify executable and writes no inherited
// descriptors.
func Prepare(dir, provider string, scenario []byte, options Options) (result *Session, err error) {
	if provider != "codex" && provider != "claude" {
		return nil, fmt.Errorf("unsupported mock provider %q", provider)
	}
	if dir == "" {
		dir, err = os.MkdirTemp("", "sdk-go-mocktest-")
		if err != nil {
			return nil, err
		}
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	captureRoot, err := Capture(dir, provider, scenario, options)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(captureRoot)

	fixtureDir, err := os.MkdirTemp(dir, ".sdk-go-fixture-")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(fixtureDir); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, removeFixtureDir(fixtureDir))
		}
	}()
	fixture, err := agenttestify.Prepare(agenttestify.PrepareOptions{
		FixtureDir:       fixtureDir,
		Agent:            provider,
		Recording:        captureRoot,
		EmbeddedIdentity: embeddedTestifyIdentity(),
	})
	if err != nil {
		return nil, fmt.Errorf("prepare in-process fixture: %w", err)
	}
	return sessionFromFixture(fixture, options, true)
}

// Open attaches to an existing fixture without consuming or deleting it. The
// owner may inspect typed execution evidence after this Session is closed.
func Open(dir string) (*Session, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		return nil, errors.New("open requires an absolute fixture directory")
	}
	fixture, err := agenttestify.Open(dir)
	if err != nil {
		return nil, err
	}
	return sessionFromFixture(fixture, Options{}, false)
}

// FromFixture attaches a run coordinator to an already opened fixture.
func FromFixture(fixture *agenttestify.Fixture) (*Session, error) {
	return sessionFromFixture(fixture, Options{}, false)
}

func sessionFromFixture(fixture *agenttestify.Fixture, options Options, owned bool) (*Session, error) {
	if fixture == nil {
		return nil, errors.New("prepared fixture is unavailable")
	}
	route, ok := fixture.Routes["mock"]
	if !ok || !route.Enabled {
		return nil, errors.New("prepared fixture has no enabled mock route")
	}
	return &Session{
		Dir:         fixture.FixtureDir,
		Workspace:   route.Workspace,
		ProtocolCwd: route.ProtocolCwd,
		Env:         sortedEnvironment(route.Env),
		SessionID:   fixture.FixtureID,
		fixture:     fixture,
		options:     options,
		owned:       owned,
		active:      make(map[*Run]struct{}),
	}, nil
}

// ConnectionIDs returns the sorted mock route connection inventory.
func (s *Session) ConnectionIDs() []string {
	if s == nil || s.fixture == nil {
		return nil
	}
	route, ok := s.fixture.Routes["mock"]
	if !ok {
		return nil
	}
	ids := make([]string, len(route.Connections))
	for i, connection := range route.Connections {
		ids[i] = connection.ID
	}
	slices.Sort(ids)
	return ids
}

func removeFixtureDir(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove prepared fixture: %w", err)
	}
	return nil
}

func sortedEnvironment(values map[string]string) []string {
	env := make([]string, 0, len(values))
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	slices.Sort(env)
	return env
}

// NewRun starts one in-process mock connection and returns the SDK-side
// endpoints. The fixture-side endpoints are owned by testify after Start returns.
func (s *Session) NewRun(ctx context.Context, options RunOptions) (*Run, error) {
	if s == nil {
		return nil, errors.New("prepared session required")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("prepared session is closed")
	}
	fixture := s.fixture
	s.mu.Unlock()
	if fixture == nil {
		return nil, errors.New("prepared fixture is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if options.Connection == "" {
		options.Connection = "main"
	}

	fixtureInput, sdkOutput, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create mock output pipe: %w", err)
	}
	sdkInput, fixtureOutput, err := os.Pipe()
	if err != nil {
		_ = fixtureInput.Close()
		_ = sdkOutput.Close()
		return nil, fmt.Errorf("create mock input pipe: %w", err)
	}
	fixtureInputCloser := &idempotentPipeFile{File: fixtureInput}
	sdkInputCloser := &idempotentPipeFile{File: sdkInput}
	sdkOutputCloser := &idempotentPipeFile{File: sdkOutput}
	fixtureOutputCloser := &idempotentPipeFile{File: fixtureOutput}
	stderr := newDiagnosticStream()
	mock, startErr := fixture.StartMockAgent(ctx, agenttestify.MockAgentOptions{
		Connection:  options.Connection,
		ExecutionID: options.ExecutionID,
		Timeout:     options.Timeout,
		NativeArgs:  slices.Clone(options.NativeArgs),
	}, agenttestify.StartMockAgentStreams{
		Reader: fixtureInputCloser,
		Writer: fixtureOutputCloser,
		Stderr: stderr,
	})
	if mock == nil {
		_ = fixtureInputCloser.Close()
		_ = fixtureOutputCloser.Close()
		_ = sdkInputCloser.Close()
		_ = sdkOutputCloser.Close()
		_ = stderr.Close()
		return nil, startErr
	}
	run := &Run{
		Start:   mock.Start,
		Reader:  classifiedPeerReader{ReadCloser: sdkInputCloser},
		Writer:  sdkOutputCloser,
		Stderr:  stderr,
		session: s,
		mock:    mock,
	}
	go func() {
		<-mock.Done()
		_ = stderr.Close()
	}()
	s.track(run)
	return run, startErr
}

func (s *Session) track(run *Run) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[run] = struct{}{}
	id := run.Start.ExecutionID
	if id != "" && !slices.Contains(s.executions, id) {
		s.executions = append(s.executions, id)
	}
}

func (s *Session) forget(run *Run) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.active, run)
	s.mu.Unlock()
}

func (s *Session) executionIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.executions)
}

// FullConsumption reports the one-shot safe point for SDK input half-close.
func (r *Run) FullConsumption() <-chan struct{} {
	if r == nil || r.mock == nil {
		return nil
	}
	return r.mock.FullConsumption()
}

// Done closes after this connection's replay, receipt and finalization end.
func (r *Run) Done() <-chan struct{} {
	if r == nil || r.mock == nil {
		return nil
	}
	return r.mock.Done()
}

// Environment returns the execution environment in deterministic key order.
func (r *Run) Environment() []string {
	if r == nil {
		return nil
	}
	return sortedEnvironment(r.Start.Env)
}

// CloseInputWhenComplete waits for full consumption and invokes closeInput
// without blocking the replay producer. It also exits when the run fails first.
func (r *Run) CloseInputWhenComplete(closeInput func() error) <-chan error {
	result := make(chan error, 1)
	if r == nil || closeInput == nil {
		result <- errors.New("run and closeInput are required")
		return result
	}
	go func() {
		select {
		case <-r.FullConsumption():
			result <- closeInput()
		case <-r.Done():
			select {
			case <-r.FullConsumption():
				result <- closeInput()
			default:
				result <- nil
			}
		}
	}()
	return result
}

// Wait returns the authoritative typed result. Scripted disconnect and timeout
// retain the legacy bridge errors used by the SDK replay suites.
func (r *Run) Wait(ctx context.Context) (*agenttestify.MockAgentResult, error) {
	if r == nil || r.mock == nil {
		return nil, errors.New("mock run is unavailable")
	}
	result, err := r.mock.Wait(ctx)
	r.session.forget(r)
	err = legacyFailure(err, result, r.Start.ConnectionID)
	return result, err
}

// WaitError adapts Wait to codex.StreamTransport.Wait.
func (r *Run) WaitError(ctx context.Context) error {
	_, err := r.Wait(ctx)
	return err
}

// Abort cancels the run and releases both endpoint directions.
func (r *Run) Abort() error {
	if r == nil || r.mock == nil {
		return errors.New("mock run is unavailable")
	}
	err := r.mock.Abort()
	r.closeSDK()
	r.session.forget(r)
	return err
}

// CloseSDK closes only the bridge-owned SDK endpoints. The SDK normally owns
// these closures; this method exists for setup failures before a client exists.
func (r *Run) CloseSDK() error {
	if r == nil {
		return nil
	}
	r.closeSDK()
	return r.closeErr
}

func (r *Run) closeSDK() {
	r.closeOnce.Do(func() {
		r.closeErr = errors.Join(r.Writer.Close(), r.Reader.Close(), r.Stderr.Close())
	})
}

func legacyFailure(err error, result *agenttestify.MockAgentResult, connection string) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "scripted disconnect") {
		return errors.Join(ErrDisconnected, err)
	}
	if result != nil && result.Execution != nil {
		for _, connectionReport := range result.Execution.Connections {
			if connectionReport.ID != connection {
				continue
			}
			switch connectionReport.Failure {
			case agenttestify.FailureClassTimeout:
				return errors.Join(context.DeadlineExceeded, err)
			case agenttestify.FailureClassCanceled:
				return errors.Join(context.Canceled, err)
			}
		}
	}
	return err
}

// Run is the compatibility path for byte-oriented bridge tests. New SDK-facing
// tests use NewRun with codex.NewStreams or claude.OpenStreams directly.
func (s *Session) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	if s == nil {
		return errors.New("prepared session required")
	}
	if ctx == nil {
		return errors.New("peer context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if in == nil {
		return errors.New("input reader required")
	}
	if out == nil {
		out = io.Discard
	}
	peerCtx, cancelPeer := context.WithTimeout(context.WithoutCancel(ctx), lifecycleTimeout)
	defer cancelPeer()
	run, err := s.NewRun(peerCtx, RunOptions{})
	if run == nil {
		return err
	}
	if err != nil {
		_ = run.CloseSDK()
		return err
	}
	cancellationDone := make(chan struct{})
	stopCancellation := context.AfterFunc(ctx, func() {
		defer close(cancellationDone)
		_ = run.Writer.Close()
		_ = run.Reader.Close()
		if closer, ok := in.(io.Closer); ok {
			_ = closer.Close()
		}
		if closer, ok := out.(io.Closer); ok {
			_ = closer.Close()
		}
	})
	defer func() {
		if !stopCancellation() {
			<-cancellationDone
		}
	}()

	inputDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(run.Writer, in)
		closeErr := run.Writer.Close()
		inputDone <- errors.Join(copyErr, closeErr)
	}()
	outputDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(out, run.Reader)
		if copyErr != nil {
			_ = run.Writer.Close()
		}
		outputDone <- copyErr
	}()

	var completionErr error
	completionDone := make(chan struct{})
	go func() {
		defer close(completionDone)
		select {
		case <-run.FullConsumption():
			if s.options.Complete != nil {
				completionErr = s.options.Complete()
				_ = run.Writer.Close()
			}
		case <-run.Done():
		}
	}()

	result, waitErr := run.Wait(peerCtx)
	_ = run.Writer.Close()
	_ = run.Reader.Close()
	<-completionDone
	outputErr := <-outputDone
	var inputErr error
	if waitErr == nil && outputErr == nil && ctx.Err() == nil {
		inputErr = <-inputDone
	} else {
		select {
		case inputErr = <-inputDone:
		default:
		}
	}
	_ = run.CloseSDK()
	waitErr = errors.Join(ctx.Err(), waitErr)
	if waitErr != nil {
		return errors.Join(completionErr, inputErr, outputErr, legacyFailure(waitErr, result, run.Start.ConnectionID))
	}
	return errors.Join(completionErr, inputErr, outputErr)
}

// Report returns an aggregate view from typed execution reports tracked by this
// session. It never parses execution descriptors, receipts or result notices.
func (s *Session) Report() (*SessionReport, error) {
	if s == nil || s.fixture == nil {
		return nil, errors.New("prepared session required")
	}
	report := &SessionReport{
		SchemaVersion: 5,
		SessionID:     s.SessionID,
		Status:        "abandoned",
	}
	ids := s.executionIDs()
	active := false
	failed := false
	for _, id := range ids {
		execution, err := s.fixture.ExecutionReport(id)
		if err != nil {
			return nil, fmt.Errorf("execution %s report: %w", id, err)
		}
		switch execution.Status {
		case agenttestify.ExecutionStatusOpen, agenttestify.ExecutionStatusRunning:
			active = true
		case agenttestify.ExecutionStatusFailed:
			failed = true
		case agenttestify.ExecutionStatusPassed:
		default:
			return nil, fmt.Errorf("execution %s has unsupported status %q", id, execution.Status)
		}
		connections := make([]ConnectionReport, len(execution.Connections))
		for i, connection := range execution.Connections {
			connections[i] = ConnectionReport{
				ID:     connection.ID,
				Status: string(connection.Status),
			}
		}
		report.Executions = append(report.Executions, ExecutionReport{
			ID:          execution.ExecutionID,
			Command:     "mock",
			Status:      string(execution.Status),
			Connections: connections,
		})
	}
	if len(ids) == 0 {
		report.Status = "abandoned"
	} else {
		switch {
		case active:
			report.Status = "open"
		case failed:
			report.Status = "failed"
		default:
			report.Status = "passed"
		}
	}
	return report, nil
}

// Command is intentionally unavailable: embedded connections have no
// executable identity. Tests must use NewRun and the SDK stream constructors.
func (s *Session) Command(string) (string, []string, error) {
	return "", nil, errors.New("embedded mock connections have no executable command")
}

// Launcher is intentionally unavailable: embedded connections have no launcher.
func (s *Session) Launcher(string) (string, error) {
	return "", errors.New("embedded mock connections have no launcher")
}

// Close removes a fixture created by Prepare after every run has terminated.
// Sessions from Open and FromFixture never delete the owner's fixture.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if len(s.active) != 0 {
		return errors.New("prepared session still has active runs")
	}
	s.closed = true
	if !s.owned {
		return nil
	}
	return removeFixtureDir(s.Dir)
}
