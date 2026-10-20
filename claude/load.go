package claude

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zoumo/agent-sdk-go/internal/history"
)

// LoadOptions combines ordinary native resume options and bounded history input.
// WorkDir and ResumeID may be omitted; if supplied they must match History.
type LoadOptions struct {
	Options Options
	History HistoryOptions
}

var ErrLoadPending = errors.New("claude: session load has not completed")

// LoadedSession owns one resumed native Client and an asynchronous history
// stream. Drain Records, check Err, then obtain Client for normal Send/Receive.
// Close cancels replay and closes the owned child, including after success.
type LoadedSession struct {
	replay    *History
	mu        sync.Mutex
	client    *Client
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

func (h *LoadedSession) Records() <-chan HistoryRecord { return h.replay.Records() }
func (h *LoadedSession) Done() <-chan struct{}         { return h.replay.Done() }
func (h *LoadedSession) Err() error                    { return h.replay.Err() }

// Client returns the normal resumed Client only after successful complete replay.
// Later native failures are reported by Client.Err/Receive, not replay Err.
func (h *LoadedSession) Client() (*Client, error) {
	select {
	case <-h.Done():
	default:
		return nil, ErrLoadPending
	}
	if err := h.Err(); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrClosed
	}
	select {
	case <-h.client.Done():
		return nil, h.client.operationError()
	default:
		return h.client, nil
	}
}
func (h *LoadedSession) closeClient() error {
	h.closeOnce.Do(func() {
		h.mu.Lock()
		c := h.client
		h.mu.Unlock()
		if c != nil {
			h.closeErr = c.Close()
		}
	})
	return h.closeErr
}
func (h *LoadedSession) Close() error {
	h.mu.Lock()
	h.closed = true
	h.mu.Unlock()
	_ = h.replay.Close()
	return h.closeClient()
}

// Load resumes the same native session and replays its complete retained main
// chain asynchronously. No prompts or historic tools are sent to the CLI.
// The context bounds startup/replay; it does not own the Client after success.
func Load(ctx context.Context, o LoadOptions) (*LoadedSession, error) {
	opts, path, err := prepareHistory(ctx, o.History)
	if err != nil {
		return nil, err
	}
	process := o.Options
	if process.ResumeID != "" && process.ResumeID != opts.SessionID {
		return nil, errors.New("claude load: conflicting resume identity")
	}
	if process.WorkDir != "" {
		cwd, e := filepath.EvalSymlinks(process.WorkDir)
		if e != nil {
			return nil, e
		}
		if cwd != opts.CWD {
			return nil, errors.New("claude load: conflicting cwd")
		}
	}
	process.ResumeID = opts.SessionID
	process.WorkDir = opts.CWD
	env := process.Env
	if env == nil {
		env = os.Environ()
	}
	process.Env = make([]string, 0, len(env)+1)
	for _, v := range env {
		if !strings.HasPrefix(v, "CLAUDE_CONFIG_DIR=") {
			process.Env = append(process.Env, v)
		}
	}
	process.Env = append(process.Env, "CLAUDE_CONFIG_DIR="+opts.ConfigDir)
	if err := process.defaults(); err != nil {
		return nil, err
	}
	replayCtx, cancel := context.WithCancel(ctx)
	h := &LoadedSession{}
	bound := make(chan struct{})
	h.replay = history.Start(replayCtx, func(ctx context.Context, send func(HistoryRecord) error) error {
		<-bound
		return replayTranscript(ctx, opts, path, send, func(ctx context.Context) error {
			c, e := open(ctx, process, opts.SessionID)
			if e != nil {
				return e
			}
			h.mu.Lock()
			h.client = c
			h.mu.Unlock()
			go func() {
				select {
				case <-c.Done():
					cancel()
				case <-h.Done():
				}
			}()
			return nil
		})
	}, func(err error) error {
		defer cancel()
		if err != nil {
			return errors.Join(err, h.closeClient())
		}
		return nil
	})
	close(bound)
	return h, nil
}
