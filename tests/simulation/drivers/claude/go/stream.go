package claudesim

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/zoumo/agent-sdk-go/claude"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

func openStreamSession(ctx context.Context, mock *mocktest.Session, options claude.SessionOptions) (*claude.Session, *mocktest.Run, <-chan error, error) {
	return openStreamSessionOnConnection(ctx, mock, "main", "", options)
}

func openStreamSessionOnConnection(
	ctx context.Context,
	mock *mocktest.Session,
	connection, executionID string,
	options claude.SessionOptions,
) (*claude.Session, *mocktest.Run, <-chan error, error) {
	if mock == nil {
		return nil, nil, nil, errors.New("prepared mock session is required")
	}
	nativeArgs, err := claude.NativeArgs(options)
	if err != nil {
		return nil, nil, nil, err
	}
	return openStreamSessionWithNativeArgs(ctx, mock, connection, executionID, options, mockNativeArgs(nativeArgs))
}

func openStreamSessionWithNativeArgs(
	ctx context.Context,
	mock *mocktest.Session,
	connection, executionID string,
	options claude.SessionOptions,
	nativeArgs []string,
) (*claude.Session, *mocktest.Run, <-chan error, error) {
	if mock == nil {
		return nil, nil, nil, errors.New("prepared mock session is required")
	}
	run, err := mock.NewRun(ctx, mocktest.RunOptions{
		Connection:  connection,
		ExecutionID: executionID,
		NativeArgs:  append([]string(nil), nativeArgs...),
	})
	if err != nil {
		return nil, nil, nil, err
	}
	options.WorkDir = run.Start.Workspace
	options.Env = environment(run.Start.Env)
	session, err := claude.OpenStreams(ctx, options, claude.StreamTransport{
		Reader: run.Reader,
		Writer: run.Writer,
		Stderr: run.Stderr,
		Wait:   run.WaitError,
	})
	if err != nil {
		_ = run.CloseSDK()
		waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, _ = run.Wait(waitCtx)
		cancel()
		return nil, run, nil, err
	}
	inputClosed := run.CloseInputWhenComplete(session.CloseInput)
	return session, run, inputClosed, nil
}

func mockNativeArgs(args []string) []string {
	result := []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json", "--permission-prompt-tool", "stdio", "--include-partial-messages"}
	for _, arg := range args {
		if strings.HasPrefix(arg, "--model=") || strings.HasPrefix(arg, "--resume=") {
			result = append(result, arg)
		}
	}
	return result
}

func finishStream(session *claude.Session, run *mocktest.Run, inputClosed <-chan error) error {
	var result error
	if session != nil {
		result = joinInputCloseError(result, session.CloseInput())
		result = errors.Join(result, session.Close())
	}
	if inputClosed != nil {
		select {
		case inputErr := <-inputClosed:
			result = joinInputCloseError(result, inputErr)
		case <-time.After(5 * time.Second):
			result = errors.Join(result, errors.New("mock input close did not finish"))
		}
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return errors.Join(result, run.WaitError(waitCtx))
}

func joinInputCloseError(result, inputErr error) error {
	if errors.Is(inputErr, claude.ErrInputClosed) {
		return result
	}
	return errors.Join(result, inputErr)
}

func environment(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	slices.Sort(result)
	return result
}
