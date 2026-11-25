package codexsim

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/zoumo/agent-sdk-go/codex"
	recording "github.com/zoumo/agent-sdk-go/codex/tests/replay"
	"github.com/zoumo/agent-sdk-go/codex/types"
	"github.com/zoumo/agent-sdk-go/internal/mocktest"
)

// AcceptFlow executes recorded native operations through public Session and Client
// APIs. The recording supplies inputs and expected facts, never outbound frames.
func AcceptFlow(ctx context.Context, mock *mocktest.Session, path string, policy codex.ChildDetailPolicy, deliveries io.Writer) (err error) {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	frames, err := recording.Decode(f)
	_ = f.Close()
	if err != nil {
		return err
	}
	type message struct {
		ID             json.RawMessage
		Method         string
		Params, Result json.RawMessage
		Error          *codex.RPCError
	}
	messages := make([]message, len(frames))
	responses := map[string]message{}
	for i, frame := range frames {
		if err := json.Unmarshal(frame.Wire, &messages[i]); err != nil {
			return err
		}
		m := messages[i]
		if frame.Direction == "s2c" && len(m.ID) > 0 && m.Method == "" {
			responses[string(m.ID)] = m
		}
	}
	if len(frames) < 4 || messages[0].Method != "initialize" {
		return errors.New("recording has no initialization prefix")
	}
	var init types.InitializeParams
	if err := json.Unmarshal(messages[0].Params, &init); err != nil {
		return err
	}
	c, run, inputClosed, err := openStreamSession(ctx, mock, init)
	if err != nil {
		return err
	}
	var s *codex.Session
	var receiveDone <-chan struct{}
	defer func() {
		err = errors.Join(err, finishStream(run, c, s, inputClosed))
		if receiveDone != nil {
			<-receiveDone
		}
	}()
	startAt := -1
	for i, f := range frames {
		if f.Direction == "c2s" && messages[i].Method == "thread/start" {
			startAt = i
			break
		}
	}
	if startAt < 0 {
		return errors.New("recording has no thread/start")
	}
	var start types.ThreadStartParams
	if err := json.Unmarshal(messages[startAt].Params, &start); err != nil {
		return err
	}
	s, err = codex.Open(ctx, codex.SessionOptions{Client: c, Thread: start, ChildDetailPolicy: policy})
	if err != nil {
		return err
	}

	var mu sync.Mutex
	seen := map[string]int{}
	consumed := map[string]int{}
	type receivedRequest struct {
		request *codex.IncomingRequest
		source  types.EventIdentity
	}
	requests := map[codex.ID]receivedRequest{}
	findRequest := func(id codex.ID) receivedRequest {
		mu.Lock()
		defer mu.Unlock()
		return requests[id]
	}
	changed := make(chan struct{}, 1)
	done := make(chan struct{})
	var receiveErr error
	encoder := json.NewEncoder(deliveries)
	key := func(raw json.RawMessage) (string, error) {
		b, err := json.Marshal(canonical(raw))
		return string(b), err
	}
	go func() {
		defer close(done)
		for {
			got, e := s.Receive(ctx)
			if e != nil {
				mu.Lock()
				receiveErr = e
				mu.Unlock()
				return
			}
			e = encoder.Encode(struct {
				Kind        codex.ReceivedKind  `json:"kind"`
				Raw         string              `json:"raw,omitempty"`
				Source      types.EventIdentity `json:"source"`
				Origin      string              `json:"origin"`
				Observation *codex.Observation  `json:"observation,omitempty"`
			}{got.Kind, string(got.Raw), got.Source, got.Origin, got.Observation})
			mu.Lock()
			if e != nil {
				receiveErr = e
				mu.Unlock()
				return
			}
			if len(got.Raw) > 0 {
				k, err := key(got.Raw)
				if err != nil {
					receiveErr = err
					mu.Unlock()
					return
				}
				seen[k]++
			}
			if got.Request != nil {
				requests[got.Request.ID()] = receivedRequest{request: got.Request, source: got.Source}
			}
			mu.Unlock()
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}()
	receiveDone = done
	wait := func(raw json.RawMessage) error {
		for {
			mu.Lock()
			k, e := key(raw)
			if e != nil {
				mu.Unlock()
				return e
			}
			ok := seen[k] > consumed[k]
			if ok {
				consumed[k]++
			}
			e = receiveErr
			mu.Unlock()
			if ok {
				return nil
			}
			if e != nil {
				return e
			}
			select {
			case <-changed:
			case <-done:
				return errors.New("session ended before expected native fact")
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	terminals := 0
	for i := startAt + 1; i < len(frames); i++ {
		frame, m := frames[i], messages[i]
		if frame.Direction == "s2c" {
			var p struct {
				ThreadID  string          `json:"threadId"`
				TurnID    string          `json:"turnId"`
				RequestID json.RawMessage `json:"requestId"`
			}
			_ = json.Unmarshal(m.Params, &p)
			// Completed approval-fixture tool calls remain visible in both
			// child detail policies, including calls owned by a spawned Thread.
			var result struct {
				Item struct {
					Type   string `json:"type"`
					Server string `json:"server"`
				} `json:"item"`
			}
			if m.Method == "item/completed" {
				_ = json.Unmarshal(m.Params, &result)
			}
			mcpResult := result.Item.Type == "mcpToolCall" && result.Item.Server == "approval_fixture"
			resolvedRequest := false
			if m.Method == "serverRequest/resolved" && p.ThreadID != s.ThreadID() {
				if id, e := codex.ParseID(p.RequestID); e == nil {
					r := findRequest(id)
					resolvedRequest = r.request != nil && r.request.Method() == "mcpServer/elicitation/request" &&
						r.source.ThreadID == p.ThreadID
				}
			}
			if m.Method != "" && (p.ThreadID == s.ThreadID() || len(m.ID) > 0 || mcpResult || resolvedRequest) {
				if e := wait(frame.Wire); e != nil {
					return fmt.Errorf("ordinal %d %s: %w", frame.Ordinal, m.Method, e)
				}
				if m.Method == "mcpServer/elicitation/request" {
					id, e := codex.ParseID(m.ID)
					if e != nil {
						return e
					}
					r := findRequest(id)
					if r.request == nil || r.source.ThreadID != p.ThreadID || r.source.TurnID != p.TurnID {
						return fmt.Errorf("ordinal %d: MCP request source differs from recording", frame.Ordinal)
					}
				}
				if m.Method == "turn/completed" {
					terminals++
				}
			}
			continue
		}
		if m.Method == "" {
			id, e := codex.ParseID(m.ID)
			if e != nil {
				return e
			}
			r := findRequest(id).request
			if r == nil {
				return fmt.Errorf("ordinal %d: original answer handle missing", frame.Ordinal)
			}
			if m.Error != nil {
				e = r.Handle().Reject(ctx, m.Error)
			} else {
				switch r.Method() {
				case "item/commandExecution/requestApproval":
					var response types.CommandExecutionRequestApprovalResponse
					if e = json.Unmarshal(m.Result, &response); e == nil {
						var decision struct{ Decision string }
						if e = json.Unmarshal(m.Result, &decision); e == nil {
							options := codex.ApprovalOptions{CommandExecution: &response}
							if decision.Decision == "accept" || decision.Decision == "acceptForSession" {
								e = r.Approve(ctx, options)
							} else {
								e = r.Deny(ctx, options)
							}
						}
					}
				case "item/fileChange/requestApproval":
					var response types.FileChangeRequestApprovalResponse
					if e = json.Unmarshal(m.Result, &response); e == nil {
						options := codex.ApprovalOptions{FileChange: &response}
						if response.Decision == "accept" || response.Decision == "acceptForSession" {
							e = r.Approve(ctx, options)
						} else {
							e = r.Deny(ctx, options)
						}
					}
				case "item/permissions/requestApproval":
					e = answerSandboxPermissions(ctx, r, m.Result)
				case "mcpServer/elicitation/request":
					e = answerMCPElicitation(ctx, r, m.Result)
				case "item/tool/requestUserInput":
					var answer types.ToolRequestUserInputResponse
					if e = json.Unmarshal(m.Result, &answer); e == nil {
						e = r.Answer(ctx, answer)
					}
				default:
					e = r.Handle().Answer(ctx, m.Result)
				}
			}
			if answerErr := recordedAnswerError(e); answerErr != nil {
				return fmt.Errorf("ordinal %d %s answer: %w", frame.Ordinal, r.Method(), answerErr)
			}
			continue
		}
		var result any
		var callErr error
		switch m.Method {
		case "thread/inject_items":
			var params types.ThreadInjectItemsParams
			if err := json.Unmarshal(m.Params, &params); err != nil {
				return err
			}
			if params.ThreadID != s.ThreadID() {
				return errors.New("foreign inject thread")
			}
			callErr = s.Inject(ctx, params.Items)
			result = types.ThreadInjectItemsResponse{}
		case "thread/loaded/list":
			var response json.RawMessage
			callErr = c.Call(ctx, m.Method, m.Params, &response)
			result = response
		case "thread/name/set":
			var params struct {
				ThreadID string `json:"threadId"`
				Name     string `json:"name"`
			}
			if e := json.Unmarshal(m.Params, &params); e != nil {
				return e
			}
			if params.ThreadID != s.ThreadID() || params.Name == "" {
				return errors.New("recorded rename must name the session thread")
			}
			var response json.RawMessage
			callErr = c.Call(ctx, m.Method, params, &response)
			result = response
		case "turn/start":
			var p types.TurnStartParams
			if e := json.Unmarshal(m.Params, &p); e != nil {
				return e
			}
			var input codex.Input
			if e := json.Unmarshal(m.Params, &input); e != nil {
				return e
			}
			if p.ThreadID != s.ThreadID() {
				return errors.New("foreign input thread")
			}
			sub, e := s.Submit(ctx, input)
			result, callErr = sub.Response, e
		case "thread/settings/update":
			var p types.ThreadSettingsUpdateParams
			if e := json.Unmarshal(m.Params, &p); e != nil {
				return e
			}
			var r codex.ConfigurationReceipt
			if model, ok := p.Model.Get(); ok {
				r, callErr = s.SetModel(ctx, model)
			} else {
				r, callErr = s.SetThinking(ctx, codex.ThinkingChange{Effort: p.Effort, Summary: p.Summary})
			}
			result = r.Response
		case "turn/interrupt":
			var p types.TurnInterruptParams
			if e := json.Unmarshal(m.Params, &p); e != nil {
				return e
			}
			callErr = s.Interrupt(ctx, codex.InterruptOptions{TurnID: p.TurnID})
			result = types.TurnInterruptResponse{}
		case "thread/unsubscribe":
			var p types.ThreadUnsubscribeParams
			if err := json.Unmarshal(m.Params, &p); err != nil {
				return err
			}
			if p.ThreadID != s.ThreadID() {
				return errors.New("foreign detach thread")
			}
			expected := responses[string(m.ID)]
			var response types.ThreadUnsubscribeResponse
			if expected.Error != nil || json.Unmarshal(expected.Result, &response) != nil ||
				(response.Status != types.ThreadUnsubscribeStatusUnsubscribed &&
					response.Status != types.ThreadUnsubscribeStatusNotLoaded &&
					response.Status != types.ThreadUnsubscribeStatusNotSubscribed) {
				return errors.New("recorded detach response differs")
			}
			if callErr = s.Close(); callErr != nil {
				return fmt.Errorf("%s: %w", m.Method, callErr)
			}
			continue
		case "thread/read":
			var p types.ThreadReadParams
			if e := json.Unmarshal(m.Params, &p); e != nil {
				return e
			}
			result, callErr = c.ReadThread(ctx, p)
		default:
			return fmt.Errorf("unsupported recorded operation %s", m.Method)
		}
		expected := responses[string(m.ID)]
		if expected.Error != nil {
			var native *codex.RPCError
			if callErr == nil || !errors.As(callErr, &native) || native.Code != expected.Error.Code {
				return fmt.Errorf("%s did not reproduce native rejection: %w", m.Method, callErr)
			}
			continue
		}
		if callErr != nil {
			return fmt.Errorf("%s: %w", m.Method, callErr)
		}
		raw, e := json.Marshal(result)
		if e != nil {
			return e
		}
		if !equal(canonical(raw), canonical(expected.Result)) {
			return fmt.Errorf("go %s response differs from recording", m.Method)
		}
	}
	if terminals == 0 {
		return errors.New("recording has no observed root terminal")
	}
	if err := c.Close(); err != nil {
		return err
	}
	if err := s.Close(); err != nil {
		return err
	}
	<-done
	if receiveErr != nil && !errors.Is(receiveErr, io.EOF) {
		return receiveErr
	}
	return nil
}

func recordedAnswerError(err error) error {
	var answer *codex.AnswerError
	if errors.As(err, &answer) && answer.Dispatch == codex.OutcomeUnknown && errors.Is(answer, codex.ErrRequestResolved) {
		// The strict mock matches the answer before emitting the recorded resolution.
		// The resolution can reach the SDK reader before its writer reports completion.
		// Keep checking the recorded facts and require the mock to consume the full trace.
		return nil
	}
	return err
}

func answerSandboxPermissions(ctx context.Context, request *codex.IncomingRequest, recorded json.RawMessage) error {
	var response types.PermissionsRequestApprovalResponse
	if err := json.Unmarshal(recorded, &response); err != nil {
		return err
	}
	grant := response.Permissions
	if !grant.FileSystem.IsSet() && !grant.Network.IsSet() && len(grant.UnknownFields) == 0 {
		return request.Deny(ctx, codex.ApprovalOptions{Permissions: &response})
	}
	var params types.PermissionsRequestApprovalParams
	if err := json.Unmarshal(request.Params(), &params); err != nil {
		return err
	}
	// Match granted members to the live request and retain the recorded field
	// presence, including the legacy grant's explicit network null.
	if (grant.FileSystem.IsSet() && !equal(grant.FileSystem, params.Permissions.FileSystem)) ||
		(grant.Network.IsSet() && !equal(grant.Network, params.Permissions.Network)) ||
		(len(grant.UnknownFields) > 0 && !equal(grant.UnknownFields, params.Permissions.UnknownFields)) {
		return errors.New("recorded permission grant differs from the live request")
	}
	return request.Approve(ctx, codex.ApprovalOptions{Permissions: &response})
}

func answerMCPElicitation(ctx context.Context, request *codex.IncomingRequest, recorded json.RawMessage) error {
	var params struct {
		Mode string `json:"mode"`
		Meta struct {
			ApprovalKind string `json:"codex_approval_kind"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(request.Params(), &params); err != nil {
		return err
	}
	var answer struct {
		Action  string          `json:"action"`
		Content json.RawMessage `json:"content,omitempty"`
		Meta    json.RawMessage `json:"_meta,omitempty"`
	}
	if err := json.Unmarshal(recorded, &answer); err != nil {
		return err
	}
	if params.Meta.ApprovalKind == "mcp_tool_call" {
		if answer.Action != "accept" || canonical(answer.Content) != nil {
			return errors.New("MCP tool approval fixture requires accept without form content")
		}
	} else {
		switch answer.Action {
		case "accept", "decline", "cancel":
		default:
			return fmt.Errorf("unsupported MCP elicitation action %q", answer.Action)
		}
		if params.Mode != "form" && params.Mode != "url" {
			return fmt.Errorf("unsupported MCP elicitation mode %q", params.Mode)
		}
	}
	return request.Handle().Answer(ctx, answer)
}

func openStreamSession(ctx context.Context, mock *mocktest.Session, initialize types.InitializeParams) (*codex.Client, *mocktest.Run, <-chan error, error) {
	return openStreamSessionOnConnection(ctx, mock, "main", "", initialize)
}

func openStreamSessionOnConnection(
	ctx context.Context,
	mock *mocktest.Session,
	connection, executionID string,
	initialize types.InitializeParams,
) (*codex.Client, *mocktest.Run, <-chan error, error) {
	if mock == nil {
		return nil, nil, nil, errors.New("prepared mock session is required")
	}
	nativeArgs := []string{"app-server", "--listen", "stdio://"}
	run, err := mock.NewRun(ctx, mocktest.RunOptions{
		Connection:  connection,
		ExecutionID: executionID,
		NativeArgs:  nativeArgs,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	client, err := codex.NewStreams(ctx, codex.StreamOptions{
		Args:       append([]string(nil), run.Start.NativeArgs...),
		Env:        environment(run.Start.Env),
		Dir:        run.Start.ProtocolCwd,
		Initialize: initialize,
	}, codex.StreamTransport{
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
	inputClosed := run.CloseInputWhenComplete(client.CloseInput)
	return client, run, inputClosed, nil
}

func finishStream(run *mocktest.Run, client *codex.Client, session *codex.Session, inputClosed <-chan error) error {
	var result error
	if session != nil {
		result = errors.Join(result, session.Close())
	}
	result = joinInputCloseError(result, client.CloseInput())
	result = errors.Join(result, client.Close())
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
	if errors.Is(inputErr, codex.ErrInputClosed) {
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
