package mocktest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	sessionSchemaVersion = 3
	lifecycleTimeout     = 30 * time.Second
)

type command struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

type sourceDescriptor struct {
	Kind          string `json:"kind"`
	ID            string `json:"id,omitempty"`
	Path          string `json:"path"`
	SHA256        string `json:"sha256"`
	Provenance    string `json:"provenance"`
	DriverProfile string `json:"driverProfile,omitempty"`
}

type connectionDescriptor struct {
	ID       string   `json:"id"`
	Command  string   `json:"command"`
	Args     []string `json:"args,omitempty"`
	Launcher string   `json:"launcher,omitempty"`
}

type routeDescriptor struct {
	Enabled     bool                   `json:"enabled"`
	Executor    string                 `json:"executor"`
	Target      string                 `json:"target"`
	Provenance  string                 `json:"provenance"`
	Workspace   string                 `json:"workspace"`
	ProtocolCwd string                 `json:"protocolCwd"`
	Env         map[string]string      `json:"env"`
	Connections []connectionDescriptor `json:"connections"`
}

type sessionDescriptor struct {
	SchemaVersion int                        `json:"schemaVersion"`
	SessionID     string                     `json:"sessionId"`
	Tool          command                    `json:"tool"`
	ToolVersion   string                     `json:"toolVersion"`
	ToolSHA256    string                     `json:"toolSHA256"`
	AgentType     string                     `json:"agentType"`
	Source        sourceDescriptor           `json:"source"`
	ResourceRoot  string                     `json:"resourceRoot"`
	Workspace     string                     `json:"workspace"`
	Home          string                     `json:"home"`
	Config        string                     `json:"config"`
	Tmp           string                     `json:"tmp"`
	ProtocolCwd   string                     `json:"protocolCwd"`
	Env           map[string]string          `json:"env"`
	Routes        map[string]routeDescriptor `json:"routes"`
}

// SessionReport is read from the tool-owned execution index before the caller
// removes the session directory.
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

// Session is one prepared external-tool lifecycle. Callers apply Workspace and
// Env to the SDK host and Close it after every SDK client and peer has exited.
type Session struct {
	Dir         string
	Workspace   string
	ProtocolCwd string
	Env         []string
	SessionID   string

	executable string
	descriptor sessionDescriptor
	mockRoute  routeDescriptor
	options    Options

	mu     sync.Mutex
	closed bool
}

// Prepare imports authored synthetic material through the public recording
// lifecycle, validates the copied descriptor and material, and returns one
// prepared session. A successful prepare must be followed by Close.
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
	executable, err := Executable()
	if err != nil {
		return nil, err
	}
	captureRoot, err := Capture(dir, provider, scenario, options)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(captureRoot)
	source, err := inspectSyntheticSource(captureRoot, provider)
	if err != nil {
		return nil, err
	}
	sessionDir, err := os.MkdirTemp(dir, ".sdk-go-session-")
	if err != nil {
		return nil, err
	}
	if err := os.Remove(sessionDir); err != nil {
		return nil, err
	}
	defer func() {
		if err == nil {
			return
		}
		err = errors.Join(err, removeSessionDir(sessionDir))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), lifecycleTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable,
		"prepare",
		"--agent", provider,
		"--recording", captureRoot,
		"--session-dir", sessionDir,
	)
	cmd.Env = prepareEnvironment(dir)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("prepare synthetic session: %w: %s%s", err, stderr.String(), stdout.String())
	}
	sessionDir, err = filepath.EvalSymlinks(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("prepared session path: %w", err)
	}
	descriptor, err := readSessionDescriptor(sessionDir)
	if err != nil {
		return nil, err
	}
	if err := validateDescriptor(descriptor, executable, provider, sessionDir); err != nil {
		return nil, err
	}
	if err := verifyPreparedMaterial(descriptor.Source.Path, source); err != nil {
		return nil, err
	}
	return &Session{
		Dir:         sessionDir,
		Workspace:   descriptor.Routes["mock"].Workspace,
		ProtocolCwd: descriptor.Routes["mock"].ProtocolCwd,
		Env:         sortedEnvironment(descriptor.Routes["mock"].Env),
		SessionID:   descriptor.SessionID,
		executable:  executable,
		descriptor:  descriptor,
		mockRoute:   descriptor.Routes["mock"],
		options:     options,
	}, nil
}

func removeSessionDir(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove prepared session: %w", err)
	}
	return nil
}

func readSessionDescriptor(sessionDir string) (sessionDescriptor, error) {
	var descriptor sessionDescriptor
	data, err := os.ReadFile(filepath.Join(sessionDir, "session.json"))
	if err != nil {
		return descriptor, err
	}
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return descriptor, fmt.Errorf("session descriptor: %w", err)
	}
	return descriptor, nil
}

func validateDescriptor(descriptor sessionDescriptor, executable, provider, sessionDir string) error {
	if descriptor.SchemaVersion != sessionSchemaVersion {
		return fmt.Errorf("unsupported session schemaVersion %d", descriptor.SchemaVersion)
	}
	if descriptor.SessionID == "" || descriptor.AgentType != provider || descriptor.ToolSHA256 == "" {
		return errors.New("session identity is incomplete")
	}
	toolPath, err := filepath.EvalSymlinks(descriptor.Tool.Command)
	if err != nil {
		return fmt.Errorf("session tool: %w", err)
	}
	wantPath, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return fmt.Errorf("configured tool: %w", err)
	}
	if toolPath != wantPath {
		return errors.New("session tool differs from AGENT_SDK_TESTIFY_BIN")
	}
	actualHash, err := fileHash(toolPath)
	if err != nil {
		return err
	}
	if descriptor.ToolSHA256 != actualHash {
		return errors.New("session tool hash differs from the executable")
	}
	if descriptor.Source.Kind != "recording" || descriptor.Source.Provenance != "external" ||
		!filepath.IsAbs(descriptor.Source.Path) || descriptor.Source.SHA256 == "" {
		return errors.New("session source is not a validated external recording")
	}
	canonicalSource, sourceErr := filepath.EvalSymlinks(descriptor.Source.Path)
	canonicalSession, sessionErr := filepath.EvalSymlinks(sessionDir)
	if sourceErr != nil || sessionErr != nil || !pathWithin(canonicalSource, canonicalSession) {
		return errors.New("session source is outside the prepared session")
	}
	if !filepath.IsAbs(descriptor.ResourceRoot) || !filepath.IsAbs(descriptor.Workspace) ||
		!filepath.IsAbs(descriptor.Home) || !filepath.IsAbs(descriptor.Config) || !filepath.IsAbs(descriptor.Tmp) {
		return errors.New("session paths must be absolute")
	}
	if filepath.Clean(descriptor.ResourceRoot) != filepath.Join(canonicalSession, "runtime") {
		return errors.New("session runtime must be inside session/runtime")
	}
	route, ok := descriptor.Routes["mock"]
	if !ok || !route.Enabled || route.Executor != "external-sdk" || route.Target != "replay" ||
		route.Provenance != "external" {
		return errors.New("session mock route is disabled or has the wrong identity")
	}
	if !filepath.IsAbs(route.Workspace) || !filepath.IsAbs(route.ProtocolCwd) {
		return errors.New("mock route paths must be absolute")
	}
	if info, err := os.Stat(route.Workspace); err != nil || !info.IsDir() {
		return errors.New("mock route workspace is unavailable")
	}
	if len(route.Env) == 0 {
		return errors.New("mock route environment is empty")
	}
	for key, value := range route.Env {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, '\x00') {
			return errors.New("mock route environment is invalid")
		}
	}
	main, ok := findConnection(route, "main")
	if !ok || !filepath.IsAbs(main.Command) || main.Launcher != "" && !filepath.IsAbs(main.Launcher) {
		return errors.New("mock main connection is incomplete")
	}
	if mainCommandHash, err := fileHash(main.Command); err != nil || mainCommandHash != actualHash {
		return errors.New("mock main connection differs from the session tool")
	}
	if containsLegacyArgument(main.Args) {
		return errors.New("mock main connection uses removed top-level arguments")
	}
	if err := validateConnectionArgs(main.Args, canonicalSession, "main"); err != nil {
		return err
	}
	return nil
}

func validateConnectionArgs(args []string, sessionDir, connection string) error {
	if len(args) != 6 ||
		args[0] != "agent" || args[1] != "mock" ||
		args[2] != "--session-dir" || args[3] != sessionDir ||
		args[4] != "--connection" || args[5] != connection {
		return errors.New("mock connection arguments differ from the prepared session")
	}
	return nil
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func containsLegacyArgument(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "--recording", "--report", "--provider", "--list", "--describe":
			return true
		}
	}
	return false
}

func sortedEnvironment(values map[string]string) []string {
	env := make([]string, 0, len(values))
	for key, value := range values {
		env = append(env, key+"="+value)
	}
	slices.Sort(env)
	return env
}

func prepareEnvironment(dir string) []string {
	home := filepath.Join(dir, "prepare-home")
	_ = os.MkdirAll(home, 0o700)
	env := append([]string(nil), os.Environ()...)
	for key, value := range map[string]string{
		"HOME":              home,
		"CODEX_HOME":        filepath.Join(home, ".codex"),
		"CLAUDE_CONFIG_DIR": filepath.Join(home, ".claude"),
	} {
		env = replaceEnvironment(env, key, value)
	}
	return env
}

func replaceEnvironment(env []string, key, value string) []string {
	prefix := key + "="
	out := env[:0]
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return append(out, prefix+value)
}

func verifyPreparedMaterial(path string, source *syntheticSource) error {
	for _, file := range []struct {
		path   string
		digest string
	}{
		{"plan.json", source.planSHA256},
		{"manifest.json", source.manifestSHA256},
		{filepath.Join("raw", "raw.jsonl"), source.rawSHA256},
	} {
		data, err := os.ReadFile(filepath.Join(path, file.path))
		if err != nil {
			return err
		}
		if hash(data) != file.digest {
			return fmt.Errorf("prepared synthetic material changed: %s", file.path)
		}
	}
	var manifest struct {
		Provenance string `json:"provenance"`
	}
	data, err := os.ReadFile(filepath.Join(path, "manifest.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Provenance != "synthetic" {
		return errors.New("prepared synthetic material lost its provenance")
	}
	return nil
}

func findConnection(route routeDescriptor, id string) (connectionDescriptor, bool) {
	for _, connection := range route.Connections {
		if connection.ID == id {
			return connection, true
		}
	}
	return connectionDescriptor{}, false
}

// Command returns the executable and argv for a declared connection.
func (s *Session) Command(id string) (string, []string, error) {
	if s == nil {
		return "", nil, errors.New("prepared session required")
	}
	connection, ok := findConnection(s.mockRoute, id)
	if !ok {
		return "", nil, fmt.Errorf("undeclared mock connection %q", id)
	}
	return connection.Command, append([]string(nil), connection.Args...), nil
}

// Launcher returns the tool-provided native launcher for SDKs that accept only
// an executable path and append native startup arguments.
func (s *Session) Launcher(id string) (string, error) {
	if s == nil {
		return "", errors.New("prepared session required")
	}
	connection, ok := findConnection(s.mockRoute, id)
	if !ok || connection.Launcher == "" {
		return "", fmt.Errorf("mock connection %q has no launcher", id)
	}
	return connection.Launcher, nil
}

// Run starts one declared mock execution and proxies the supplied streams.
func (s *Session) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	if s == nil {
		return errors.New("prepared session required")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("prepared session is closed")
	}
	s.mu.Unlock()
	if ctx == nil {
		return errors.New("peer context required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	executable, args, err := s.Command("main")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, lifecycleTimeout)
	defer cancel()

	resultFile, err := os.CreateTemp(s.Dir, ".result-*.jsonl")
	if err != nil {
		return err
	}
	defer func() {
		_ = resultFile.Close()
		_ = os.Remove(resultFile.Name())
	}()
	args = append(args, "--result-fd", "3")
	extraFiles := []*os.File{resultFile}

	var progressR, progressW *os.File
	if s.options.Complete != nil {
		progressR, progressW, err = os.Pipe()
		if err != nil {
			return err
		}
		defer progressR.Close()
		defer progressW.Close()
		args = append(args, "--progress-fd", "4")
		extraFiles = append(extraFiles, progressW)
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 3 * time.Second
	cmd.Env = append([]string(nil), s.Env...)
	cmd.Dir = s.Workspace
	var outputR, outputW *os.File
	if file, ok := out.(*os.File); ok {
		cmd.Stdout = file
	} else {
		outputR, outputW, err = os.Pipe()
		if err != nil {
			return err
		}
		defer outputR.Close()
		defer outputW.Close()
		cmd.Stdout = outputW
	}
	var stdin io.WriteCloser
	if file, ok := in.(*os.File); ok {
		cmd.Stdin = file
	} else {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return err
		}
		defer stdin.Close()
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.ExtraFiles = extraFiles
	var inputClose, outputClose sync.Once
	closeInput := func() {
		inputClose.Do(func() {
			if closer, ok := in.(io.Closer); ok {
				_ = closer.Close()
			}
		})
	}
	closeOutput := func() {
		outputClose.Do(func() {
			if closer, ok := out.(io.Closer); ok {
				_ = closer.Close()
			}
		})
	}
	cleanupDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(cleanupDone)
		closeInput()
		closeOutput()
	})
	defer func() {
		if !stop() {
			<-cleanupDone
		}
	}()
	if err := cmd.Start(); err != nil {
		return err
	}
	peerExited := make(chan struct{})
	var outputDone chan error
	if outputR != nil {
		_ = outputW.Close()
		outputDone = make(chan error, 1)
		go func() {
			_, copyErr := io.Copy(out, outputR)
			if copyErr != nil {
				select {
				case <-peerExited:
				default:
					cancel()
				}
			}
			outputDone <- copyErr
		}()
	}
	var inputDone chan error
	if stdin != nil {
		inputDone = make(chan error, 1)
		go func() {
			_, copyErr := io.Copy(stdin, in)
			_ = stdin.Close()
			inputDone <- copyErr
		}()
	}
	var callbackErr error
	if progressW != nil {
		_ = progressW.Close()
		var marker [1]byte
		if n, _ := progressR.Read(marker[:]); n == 1 && marker[0] == '1' {
			callbackErr = s.options.Complete()
			if callbackErr != nil {
				cancel()
			}
		}
	}
	runErr := cmd.Wait()
	close(peerExited)
	if inputDone != nil {
		closeInput()
	}
	var outputErr error
	if outputDone != nil {
		outputErr = <-outputDone
	}
	var inputErr error
	if inputDone != nil {
		closeInput()
		inputErr = <-inputDone
	}
	resultStatus, resultErr := validateRunResult(resultFile.Name(), s.Dir, s.SessionID, "main")
	if callbackErr != nil {
		return errors.Join(callbackErr, resultStatusError(resultStatus, resultErr))
	}
	if ctx.Err() != nil {
		return errors.Join(ctx.Err(), outputErr, resultStatusError(resultStatus, resultErr))
	}
	if runErr != nil {
		diagnostic := stderr.String()
		if strings.Contains(diagnostic, "scripted disconnect") {
			return errors.Join(ErrDisconnected, resultStatusError(resultStatus, resultErr))
		}
		if strings.Contains(diagnostic, context.DeadlineExceeded.Error()) {
			return errors.Join(context.DeadlineExceeded, resultStatusError(resultStatus, resultErr))
		}
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			switch exit.ExitCode() {
			case 4:
				return errors.Join(ErrDisconnected, resultStatusError(resultStatus, resultErr))
			case 3:
				return errors.Join(context.DeadlineExceeded, resultStatusError(resultStatus, resultErr))
			}
		}
		return errors.Join(fmt.Errorf("external mock: %w: %s", runErr, diagnostic), resultStatusError(resultStatus, resultErr))
	}
	if outputErr != nil {
		return errors.Join(fmt.Errorf("external mock output: %w", outputErr), resultStatusError(resultStatus, resultErr))
	}
	if inputErr != nil {
		return errors.Join(fmt.Errorf("external mock input: %w", inputErr), resultStatusError(resultStatus, resultErr))
	}
	if resultErr != nil {
		return resultErr
	}
	if resultStatus != "passed" {
		return fmt.Errorf("external mock result status %s", resultStatus)
	}
	return nil
}

func resultStatusError(status string, err error) error {
	if err != nil {
		return err
	}
	if status != "failed" {
		return fmt.Errorf("external mock result status %s; want failed", status)
	}
	return nil
}

type indexedExecution struct {
	ID               string             `json:"id"`
	Command          string             `json:"command"`
	DescriptorSHA256 string             `json:"descriptorSha256"`
	Status           string             `json:"status"`
	Connections      []ConnectionReport `json:"connections"`
}

type sessionIndex struct {
	SchemaVersion int                `json:"schemaVersion"`
	SessionID     string             `json:"sessionId"`
	Executions    []indexedExecution `json:"executions"`
}

type executionIdentity struct {
	SchemaVersion int    `json:"schemaVersion"`
	ExecutionID   string `json:"executionId"`
	SessionID     string `json:"sessionId"`
	Command       string `json:"command"`
}

type resultReceipt struct {
	SchemaVersion   int    `json:"schemaVersion"`
	SessionID       string `json:"sessionId"`
	ExecutionID     string `json:"executionId"`
	ExecutionSHA256 string `json:"executionSHA256"`
	Execution       string `json:"execution"`
	ConnectionID    string `json:"connectionId"`
	Command         string `json:"command"`
	Status          string `json:"status"`
	Result          string `json:"result"`
}

// Report reads the execution index and final receipts before callers remove a
// prepared session. It never mutates the session or invokes the tool.
func (s *Session) Report() (*SessionReport, error) {
	if s == nil {
		return nil, errors.New("prepared session required")
	}
	var index sessionIndex
	data, err := os.ReadFile(filepath.Join(s.Dir, "executions", "index.json"))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("execution index: %w", err)
	}
	if index.SchemaVersion != sessionSchemaVersion || index.SessionID != s.SessionID {
		return nil, errors.New("execution index belongs to a different session")
	}
	report := &SessionReport{
		SchemaVersion: index.SchemaVersion,
		SessionID:     index.SessionID,
		Status:        "abandoned",
		Executions:    make([]ExecutionReport, 0, len(index.Executions)),
	}
	active := false
	failed := false
	for _, execution := range index.Executions {
		switch execution.Status {
		case "running", "open":
			active = true
		case "passed":
		case "failed":
			failed = true
		default:
			return nil, fmt.Errorf("execution %s has unsupported status %q", execution.ID, execution.Status)
		}
		if err := validateExecutionReport(s.Dir, s.SessionID, execution); err != nil {
			return nil, err
		}
		report.Executions = append(report.Executions, ExecutionReport{
			ID:          execution.ID,
			Command:     execution.Command,
			Status:      execution.Status,
			Connections: append([]ConnectionReport(nil), execution.Connections...),
		})
	}
	if len(index.Executions) == 0 {
		return report, nil
	}
	switch {
	case active:
		report.Status = "open"
	case failed:
		report.Status = "failed"
	default:
		report.Status = "passed"
	}
	return report, nil
}

func validateExecutionReport(sessionDir, sessionID string, execution indexedExecution) error {
	if execution.ID == "" || execution.Command != "mock" || execution.DescriptorSHA256 == "" {
		return errors.New("execution index identity is incomplete")
	}
	root := filepath.Join(sessionDir, "executions", execution.ID)
	descriptorPath := filepath.Join(root, "execution.json")
	hash, err := fileHash(descriptorPath)
	if err != nil || hash != execution.DescriptorSHA256 {
		return errors.New("execution descriptor hash differs from the index")
	}
	var identity executionIdentity
	data, err := os.ReadFile(descriptorPath)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &identity); err != nil {
		return fmt.Errorf("execution descriptor: %w", err)
	}
	if identity.SchemaVersion != sessionSchemaVersion || identity.ExecutionID != execution.ID ||
		identity.SessionID != sessionID || identity.Command != "mock" {
		return errors.New("execution descriptor identity differs from the index")
	}
	for _, connection := range execution.Connections {
		switch connection.Status {
		case "pending":
			continue
		case "running", "passed", "failed":
		default:
			return fmt.Errorf("mock connection %s has unsupported status %q", connection.ID, connection.Status)
		}
		path := filepath.Join(root, "receipts", connection.ID+".result.json")
		var receipt resultReceipt
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("mock connection %s receipt: %w", connection.ID, err)
		}
		if err := json.Unmarshal(data, &receipt); err != nil {
			return fmt.Errorf("mock connection %s receipt: %w", connection.ID, err)
		}
		if receipt.SchemaVersion != 1 || receipt.SessionID != sessionID ||
			receipt.ExecutionID != execution.ID || receipt.ConnectionID != connection.ID ||
			receipt.Command != "mock" || receipt.Status != connection.Status ||
			receipt.Result != path {
			return fmt.Errorf("mock connection %s receipt differs from the index", connection.ID)
		}
	}
	return nil
}

func validateRunResult(path, sessionDir, sessionID, connection string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read result notifications: %w", err)
	}
	lines := bytes.Split(bytes.TrimSpace(data), []byte{'\n'})
	if len(lines) != 2 {
		return "", fmt.Errorf("result notifications: got %d records; want start and completion", len(lines))
	}
	notices := make([]resultReceipt, 0, len(lines))
	for _, line := range lines {
		var notice resultReceipt
		if err := json.Unmarshal(line, &notice); err != nil {
			return "", fmt.Errorf("result notification: %w", err)
		}
		notices = append(notices, notice)
	}
	start, done := notices[0], notices[1]
	if start.SchemaVersion != 1 || done.SchemaVersion != 1 ||
		start.SessionID != sessionID || done.SessionID != sessionID ||
		start.ConnectionID != connection || done.ConnectionID != connection ||
		start.Command != "mock" || done.Command != "mock" ||
		start.Status != "running" {
		return "", errors.New("result notification identity/status differs")
	}
	switch done.Status {
	case "passed", "failed":
	default:
		return "", fmt.Errorf("result notification completion status %q", done.Status)
	}
	if start.ExecutionID == "" || start.ExecutionID != done.ExecutionID ||
		start.Result != done.Result || start.Result == "" ||
		start.Execution != done.Execution || start.Execution == "" ||
		start.ExecutionSHA256 == "" || start.ExecutionSHA256 != done.ExecutionSHA256 {
		return "", errors.New("result notification execution identity changed")
	}
	if !pathWithin(start.Result, sessionDir) || !pathWithin(done.Result, sessionDir) {
		return "", errors.New("result notification receipt is outside the session")
	}
	if !pathWithin(start.Execution, sessionDir) {
		return "", errors.New("result notification execution is outside the session")
	}
	executionHash, err := fileHash(start.Execution)
	if err != nil || executionHash != start.ExecutionSHA256 {
		return "", errors.New("result notification execution hash differs")
	}
	var receipt resultReceipt
	data, err = os.ReadFile(done.Result)
	if err != nil {
		return "", fmt.Errorf("read final result receipt: %w", err)
	}
	if err := json.Unmarshal(data, &receipt); err != nil {
		return "", fmt.Errorf("final result receipt: %w", err)
	}
	if receipt.SchemaVersion != 1 || receipt.SessionID != sessionID ||
		receipt.ExecutionID != done.ExecutionID || receipt.ConnectionID != connection ||
		receipt.Command != "mock" || receipt.Status != done.Status {
		return "", errors.New("final result receipt differs from its notification")
	}
	return done.Status, nil
}

// Close removes the caller-owned session after all SDK clients and peers have
// exited. It is idempotent and never invokes a tool cleanup command.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return removeSessionDir(s.Dir)
}
