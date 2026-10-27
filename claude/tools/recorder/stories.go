package recorder

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoumo/agent-sdk-go/claude"
	ct "github.com/zoumo/agent-sdk-go/claude/types"
	recording "github.com/zoumo/agent-sdk-go/internal/recording"
)

func runStoryEntry() (bool, error) {
	return recording.StoryCommand("claude", claudeStoryPeerArgs, runClaudeStory, nil)
}
func claudeStoryPeerArgs(p recording.StoryPlan) []string {
	args := []string{"--output-format", "stream-json", "--verbose", "--input-format", "stream-json", "--permission-prompt-tool", "stdio", "--include-partial-messages"}
	if p.Model != "" {
		args = append(args, "--model="+p.Model)
	}
	return args
}

func runClaudeStory(ctx context.Context, p recording.StoryPlan, replay bool, r *recording.StoryReport) (err error) {
	if !replay {
		return errors.New("historical Go story driver is replay-only; use official Python SDK recorder")
	}
	id := strings.TrimPrefix(p.Story, "SDK-US-")
	if id == "006" || id == "007" {
		r.Status = "blocked"
		r.Gaps = []string{"recursive child attempt/depth/tool enforcement is not proven for this native version; no model launched"}
		return nil
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	mode := "--story-proxy"
	if replay {
		mode = "--story-peer"
	}
	// A fixed executable wrapper binds the plan and passes only SDK transport flags.
	wrapper := filepath.Join(p.Root, "sdk-peer")
	// Prepared roots and executables cannot contain shell quoting metacharacters.
	if strings.ContainsAny(exe+p.Root, "'\n\r") {
		return errors.New("unsafe wrapper path")
	}
	script := "#!/bin/sh\nexec '" + exe + "' '" + mode + "' '" + filepath.Join(p.Root, "plan.json") + "' \"$@\"\n"
	if e = os.WriteFile(wrapper, []byte(script), 0700); e != nil {
		return e
	}
	s, e := claude.Open(ctx, claude.SessionOptions{CLIPath: wrapper, WorkDir: p.Project, Model: p.Model, InitializeTimeout: 25 * time.Second, ShutdownTimeout: 4 * time.Second, Env: []string{"PATH=/usr/bin:/bin", "SDK_REAL_AGENT_E2E=1"}})
	if e != nil {
		return e
	}
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, s.Close())
		}
		if !replay {
			var t recording.TransportResult
			b, e := os.ReadFile(filepath.Join(p.Raw, "transport.json"))
			if e == nil {
				e = json.Unmarshal(b, &t)
			}
			if e != nil || !t.Reaped || !t.CleanupComplete {
				err = errors.Join(err, errors.New("native cleanup incomplete"))
			}
		}
	}()
	closeDrain := func() error {
		if e := s.Close(); e != nil {
			return e
		}
		closed = true
		for {
			_, e := s.Receive(ctx)
			if e != nil {
				if errors.Is(e, io.EOF) || errors.Is(e, claude.ErrClosed) {
					break
				}
				return e
			}
			r.Events++
		}
		r.Facts = append(r.Facts, "owned Close and accepted-event drain completed")
		return nil
	}
	control := func() error {
		model := claude.ModelChange{}
		if p.Model != "" {
			model.Model = &p.Model
		}
		receipt, e := s.SetModel(ctx, model)
		r.Receipts = append(r.Receipts, map[string]any{"operation": "SetModel", "receipt": receipt, "rejected": e != nil})
		if e != nil {
			r.Gaps = append(r.Gaps, "native model change rejected")
		}
		receipt, e = s.SetThinking(ctx, claude.ThinkingChange{})
		r.Receipts = append(r.Receipts, map[string]any{"operation": "SetThinking", "receipt": receipt, "rejected": e != nil})
		if e != nil {
			r.Gaps = append(r.Gaps, "native thinking change rejected")
		}
		return s.Err()
	}
	if id == "018" {
		r.Facts = append(r.Facts, "initialized a fresh native process with no submitted input")
		if e = closeDrain(); e != nil {
			return e
		}
		_, e = s.Submit(ctx, claude.Input{Text: "must not be sent"})
		if e == nil {
			return errors.New("closed session accepted input")
		}
		r.Facts = append(r.Facts, "application disconnect invalidated further submissions without reconnect")
		r.Gaps = append(r.Gaps, "unexpected connection loss/recovery remains separate fault coverage")
		return nil
	}
	if id == "015" {
		wait, stop := context.WithCancel(ctx)
		stop()
		_, e = s.Receive(wait)
		if !errors.Is(e, context.Canceled) {
			return errors.New("local Receive cancellation changed")
		}
		r.Facts = append(r.Facts, "local Receive canceled before subsequent successful Submit")
		r.Gaps = append(r.Gaps, "dispatched unknown outcome is not manufactured by a live timing race")
	}
	if id == "012" {
		if e = control(); e != nil {
			return e
		}
	}
	prompts := append([]string{}, p.Prompts...)
	if id == "008" {
		prompts = []string{"Reply with the word Ready."}
	}
	policy := scenePolicy{scene: "s1", project: p.Project}
	if id == "011" {
		policy.scene = "s2"
	}
	var rootID string
	for turn := 0; turn < len(prompts); turn++ {
		prompt := prompts[turn]
		input := claude.Input{Text: prompt}
		if id == "002" {
			var user ct.UserInput
			if e = json.Unmarshal([]byte(`{"message":{"role":"user","content":[{"type":"text","text":"Reply with the word Structured."}]}}`), &user); e != nil {
				return e
			}
			input = claude.Input{User: &user}
			r.Facts = append(r.Facts, "native structured text content block submitted without prompt flattening")
			r.Gaps = append(r.Gaps, "image/document inputs not included in this sample")
		}
		r.Inputs++
		if _, e = s.Submit(ctx, input); e != nil {
			return e
		}
		if id == "014" {
			if e = control(); e != nil {
				return e
			}
			r.Facts = append(r.Facts, "native controls returned while Session.Receive had not consumed execution events")
		}
		interleaved, interrupted := false, false
		for {
			event, e := s.Receive(ctx)
			if e != nil {
				return e
			}
			r.Events++
			if event.Request != nil {
				r.Requests++
				h := event.Request.Handle()
				if id == "004" || id == "011" {
					if e = policy.answer(h); e != nil {
						return e
					}
				} else if id == "009" || id == "010" {
					var in map[string]any
					if json.Unmarshal(h.Input(), &in) != nil {
						return errors.New("invalid native permission input")
					}
					path, _ := in["file_path"].(string)
					name := "summary.txt"
					text := "total=18\n"
					if turn == 1 {
						name = "denied.txt"
						text = "denied\n"
					}
					valid := h.ToolName() == "Write" && len(in) == 2 && in["content"] == text
					if id == "010" {
						name = "numbers.txt"
						valid = h.ToolName() == "Read" && len(in) == 1
					}
					if !valid || !transcriptSafeFile(p.Project, path, name) || (id == "009" && r.Requests != turn+1) || (id == "010" && r.Requests != 1) {
						return errors.New("permission outside exact synthetic policy")
					}
					if id == "010" {
						receipt, e := s.SetThinking(ctx, claude.ThinkingChange{})
						r.Receipts = append(r.Receipts, receipt)
						if e != nil {
							return e
						}
						r.Facts = append(r.Facts, "original permission remained unanswered during a successful native control")
					}
					answer := claude.PermissionAnswer{Behavior: claude.PermissionAllow}
					if id == "009" && turn == 1 {
						answer = claude.PermissionAnswer{Behavior: claude.PermissionDeny, Message: "synthetic denial; do not retry"}
					}
					if e = h.Answer(ctx, answer); e != nil {
						return e
					}
				} else {
					return errors.New("unexpected native permission")
				}
				continue
			}
			n := event.Event
			if n == nil {
				continue
			}
			if n.SessionID != "" {
				rootID = n.SessionID
			}
			if n.Subtype == "init" && n.System != nil && n.System.ClaudeCodeVersion != p.CLIVersion {
				return errors.New("observed native version differs from plan")
			}
			if id == "013" && n.Subtype == "init" {
				r.Facts = append(r.Facts, "native init model/configuration envelope retained separately from requested controls")
			}
			if n.Type == "stream_event" && n.Stream != nil && executionProgress(n.Stream.Event) {
				if id == "003" && turn == 0 && !interleaved {
					r.Inputs++
					_, e = s.Submit(ctx, claude.Input{Text: p.Prompts[1]})
					r.Receipts = append(r.Receipts, map[string]any{"operation": "SubmitDuringExecution", "written": e == nil})
					if e != nil {
						return e
					}
					interleaved = true
					r.Facts = append(r.Facts, "second native input written after observed execution progress")
				}
				if id == "016" && !interrupted {
					e = s.Interrupt(ctx, claude.InterruptOptions{})
					r.Receipts = append(r.Receipts, map[string]any{"operation": "Interrupt", "accepted": e == nil})
					if e != nil {
						return e
					}
					interrupted = true
					r.Facts = append(r.Facts, "Interrupt sent after native execution progress; Close remains separate")
				}
			}
			if n.Type == "result" {
				r.Results++
				if n.Result == nil || n.Result.IsError || len(n.Result.Errors) > 0 || n.Subtype != "success" {
					if id == "016" && interrupted {
						r.Facts = append(r.Facts, "native interrupt/error result retained")
						break
					}
					return errors.New("native task result failed")
				}
				if id == "003" && turn == 0 {
					if !interleaved {
						r.Gaps = append(r.Gaps, "execution ended before the supplement barrier")
					}
					if interleaved {
						prompts = prompts[:1]
						r.Gaps = append(r.Gaps, "second input has no submit acknowledgment; terminal scheduling is native")
					}
				}
				if id == "016" && !interrupted {
					r.Gaps = append(r.Gaps, "execution ended before interrupt barrier")
				}
				break
			}
		}
		if id == "013" && turn == 0 {
			if e = control(); e != nil {
				return e
			}
		}
	}
	if id == "008" {
		page, e := s.ListSubagents(ctx, claude.SubagentQuery{})
		r.Receipts = append(r.Receipts, page)
		if e != nil {
			r.Gaps = append(r.Gaps, "native root stored discovery unavailable")
		}
		r.Gaps = append(r.Gaps, "fresh synthetic root contains no previously created child records")
		return nil
	}
	if (id == "004" || id == "009" || id == "010" || id == "011") && r.Requests == 0 {
		r.Status = "untriggered"
		r.Gaps = append(r.Gaps, "required original native permission/question did not occur")
	}
	if (id == "004" || id == "009") && r.Requests > 0 {
		data, e := os.ReadFile(filepath.Join(p.Project, "summary.txt"))
		if e != nil || string(data) != "total=18\n" {
			return errors.New("native synthetic output did not match exact approved content")
		}
		if id == "009" {
			if _, e = os.Lstat(filepath.Join(p.Project, "denied.txt")); !os.IsNotExist(e) {
				return errors.New("denied write produced a file")
			}
			r.Facts = append(r.Facts, "approved synthetic file has exact bytes; denied file is absent")
		}
	}
	if id == "005" {
		r.Facts = append(r.Facts, "complete original native JSON retained with source and order")
		r.Gaps = append(r.Gaps, "unknown/duplicate injection belongs to separately labeled synthetic companions")
	}
	if id == "010" {
		r.Gaps = append(r.Gaps, "native event progress during the unanswered interval is not guaranteed by control completion")
	}
	if id == "014" {
		r.Gaps = append(r.Gaps, "bounded sample is not an unbounded slow-consumer stress proof")
	}
	if id == "017" {
		if e = closeDrain(); e != nil {
			return e
		}
		r.Gaps = append(r.Gaps, "this sample covers Close/drain; native task failure and transport faults remain separate")
	}
	if id == "019" {
		if e = s.Close(); e != nil {
			return e
		}
		closed = true
		scope := claude.SessionQueryScope{ConfigDir: p.Config, ProjectDir: p.Project, MaxEntries: 100}
		list, e := claude.ListSessions(ctx, claude.ListSessionsOptions{Scope: scope, Limit: 10, MaxSessions: 20})
		r.Receipts = append(r.Receipts, list)
		if e != nil {
			return e
		}
		info, e := claude.GetSessionInfo(ctx, rootID, claude.SessionInfoOptions{Scope: scope})
		r.Receipts = append(r.Receipts, info)
		if e != nil {
			return e
		}
		page, e := claude.GetSessionMessages(ctx, rootID, claude.SessionMessagesOptions{Scope: scope, Limit: 10, MaxMessages: 100})
		r.Receipts = append(r.Receipts, page)
		if e != nil {
			return e
		}
		r.Facts = append(r.Facts, "independent list/metadata/saved-message queries used only this fresh synthetic storage after Close")
	}
	if len(r.Gaps) == 0 && r.Status != "untriggered" {
		r.Status = "captured"
	}
	return nil
}

func transcriptSafeFile(root, path, name string) bool {
	if !filepath.IsAbs(path) {
		if path != name {
			return false
		}
		path = filepath.Join(root, path)
	}
	if path != filepath.Join(root, name) {
		return false
	}
	st, e := os.Lstat(path)
	return os.IsNotExist(e) || (e == nil && st.Mode().IsRegular() && st.Mode()&os.ModeSymlink == 0)
}
