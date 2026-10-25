// SPDX-License-Identifier: Apache-2.0
package recorder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/zoumo/agent-sdk-go/claude"
	transcript "github.com/zoumo/agent-sdk-go/claude/tools/replay"
)

var ErrUntriggered = errors.New("reviewed execution/interaction barrier untriggered; no retry")

// Drive uses an already opened native Session and takes ownership of its cleanup.
// Reserve must succeed BEFORE Open. The live entry point additionally requires
// Preflight; offline callers use only a synthetic peer. No reconnect/resume or
// replacement turn is performed. Open must use ShutdownTimeout <= 3 seconds;
// that cleanup allowance is reserved inside the absolute scene deadline.
// Observations are private, not publishable data.
func Drive(ctx context.Context, s *claude.Session, scene string, w *Workspace, l *Ledger, observations io.Writer) (err error) {
	if s == nil || l == nil {
		return fmt.Errorf("owned Session and reserved ledger required")
	}
	defer func() {
		var cleanup error
		if err == nil {
			cleanup = s.Close()
		} else {
			cleanup = s.Abort()
		}
		err = errors.Join(err, cleanup)
		err = errors.Join(err, l.Finish(scene, err == nil))
	}()
	if ctx == nil || observations == nil {
		return fmt.Errorf("context and private observations required")
	}
	ctx, cancel := activityContext(ctx)
	defer cancel()
	plan, err := Plan(scene)
	if err != nil {
		return err
	}
	if err = w.validate(); err != nil {
		return err
	}
	encoder := json.NewEncoder(&observationWriter{destination: observations, remaining: 2 << 20})
	policy := scenePolicy{scene: scene, project: w.Project}
	submit := func(text string) error {
		if err := l.Turn(scene); err != nil {
			return err
		}
		_, err := s.Submit(ctx, claude.Input{Text: text})
		return err
	}
	if err = submit(plan.Prompts[0]); err != nil {
		return err
	}
	turn, result, modelError, interrupted := 0, false, false, false
	heldOnce := false
	var held *claude.PermissionRequest
	var heldRaw json.RawMessage
	for {
		record, e := s.Receive(ctx)
		if e != nil {
			return e
		}
		if e = encoder.Encode(record); e != nil {
			return e
		}
		if held != nil {
			if e = policy.answer(held, heldRaw); e != nil {
				return e
			}
			held = nil
		}
		if record.Request != nil {
			p := record.Request.Handle()
			if !rootPermission(record.Raw) || !rootPermission(p.Raw()) {
				return policy.answer(p, record.Raw) // Deny before any held-request control.
			}
			if scene == "r2" && !heldOnce {
				// Preserve the original handle while a non-generating control and Receive
				// progress. If no further event arrives, the scene times out/untriggers.
				held, heldOnce = p, true
				heldRaw = record.Raw
				receipt, controlErr := s.SetThinking(ctx, claude.ThinkingChange{})
				if e = recordControl(encoder, receipt, controlErr); e != nil {
					return e
				}
			} else if e = policy.answer(p, record.Raw); e != nil {
				return e
			}
			continue
		}
		native := record.Event
		if native == nil {
			continue
		}
		if native.Assistant != nil {
			for _, block := range native.Assistant.Content.Blocks {
				if block.ToolUse != nil && !allowedTool(scene, block.ToolUse.Name) {
					return fmt.Errorf("child tool violates root isolation")
				}
			}
		}
		if native.AgentID != nil && *native.AgentID != "" || native.ParentToolUseID != nil && *native.ParentToolUseID != "" {
			return ErrChildEnforcement
		}
		switch native.Subtype {
		case "task_started", "task_progress", "task_updated", "task_notification", "background_tasks_changed":
			return ErrChildEnforcement
		case "hook_started", "hook_response":
			return ErrPolicyIsolation
		}
		if native.System != nil && native.Subtype == "init" && native.System.ClaudeCodeVersion != CLIVersion {
			return fmt.Errorf("unreviewed native CLI version")
		}
		if native.Error != nil && *native.Error != "" {
			modelError = true
		}
		if native.Type == "result" {
			result = true
			if native.Result == nil || native.Result.IsError || native.Subtype != "success" || len(native.Result.Errors) > 0 || native.Result.TerminalReason == "api_error" {
				modelError = true
			}
		}
		if scene == "r3" && !interrupted && native.Type == "stream_event" && native.Stream != nil && executionProgress(native.Stream.Event) && native.ParentToolUseID == nil {
			if err = submit(plan.Prompts[1]); err != nil {
				return err
			}
			interrupted = true
			if err = s.Interrupt(ctx, claude.InterruptOptions{}); err != nil {
				return err
			}
		}
		if native.System == nil || native.Subtype != "session_state_changed" || native.System.State == nil || *native.System.State != "idle" || !result {
			continue
		}
		if modelError {
			return fmt.Errorf("native error result (success subtype is insufficient)")
		}
		if scene == "r3" && !interrupted {
			return ErrUntriggered
		}
		if scene == "r1" && turn == 0 {
			receipt, controlErr := s.SetModel(ctx, claude.ModelChange{})
			if err = recordControl(encoder, receipt, controlErr); err != nil {
				return err
			}
			receipt, controlErr = s.SetThinking(ctx, claude.ThinkingChange{})
			if err = recordControl(encoder, receipt, controlErr); err != nil {
				return err
			}
		}
		if (scene == "r1" || scene == "r2") && turn == 0 {
			if scene == "r2" && (!policy.read || !policy.write) {
				return ErrUntriggered
			}
			turn++
			policy.turn = turn
			result = false
			if err = submit(plan.Prompts[turn]); err != nil {
				return err
			}
			continue
		}
		if (scene == "s1" && (!policy.read || !policy.write)) || ((scene == "s2" || scene == "r2") && !policy.question) {
			return ErrUntriggered
		}
		if scene == "s1" {
			path := filepath.Join(w.Project, "summary.txt")
			if !transcript.SafeFile(w.Project, path, "summary.txt") {
				return fmt.Errorf("unsafe output path")
			}
			file, e := os.Open(path)
			if e != nil {
				return e
			}
			b, e := io.ReadAll(io.LimitReader(file, 32))
			closeErr := file.Close()
			if e != nil || closeErr != nil || string(b) != "total=18\n" {
				return fmt.Errorf("S1 exact output evidence incomplete")
			}
		}
		return nil
	}
}
func recordControl(e *json.Encoder, receipt claude.ConfigurationReceipt, controlErr error) error {
	if err := e.Encode(receipt); err != nil {
		return err
	}
	var rejection *claude.ControlError
	if controlErr != nil && !errors.As(controlErr, &rejection) {
		return controlErr
	}
	return nil // Preserve native rejection as evidence, never infer observation.
}

type observationWriter struct {
	destination io.Writer
	remaining   int
}

func (w *observationWriter) Write(b []byte) (int, error) {
	if len(b) > w.remaining {
		return 0, fmt.Errorf("observation byte limit")
	}
	n, err := w.destination.Write(b)
	w.remaining -= n
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return n, err
}

type scenePolicy struct {
	turn                  int
	scene, project        string
	read, write, question bool
}

func rootPermission(raw json.RawMessage) bool {
	var metadata map[string]json.RawMessage
	if json.Unmarshal(raw, &metadata) != nil || metadata == nil {
		return false
	}
	for _, key := range []string{"agent_id", "agentId", "parent_tool_use_id", "task_id", "parent_task_id"} {
		if _, present := metadata[key]; present {
			return false // Unknown attribution cannot widen the root allowlist.
		}
	}
	return true
}

func (p *scenePolicy) answer(handle *claude.PermissionRequest, envelope json.RawMessage) error {
	answer := claude.PermissionAnswer{Behavior: claude.PermissionDeny, Message: "outside exact synthetic story policy"}
	matched := false
	var input map[string]any
	rootOnly := rootPermission(envelope) && rootPermission(handle.Raw())
	if rootOnly && json.Unmarshal(handle.Input(), &input) == nil {
		path, _ := input["file_path"].(string)
		if (p.scene == "s1" || (p.scene == "r2" && p.turn == 0)) && handle.ToolName() == "Read" && !p.read && len(input) == 1 && transcript.SafeFile(p.project, path, "numbers.txt") {
			p.read, matched = true, true
			answer = claude.PermissionAnswer{Behavior: claude.PermissionAllow}
		}
		content, _ := input["content"].(string)
		if (p.scene == "s1" || (p.scene == "r2" && p.turn == 0)) && handle.ToolName() == "Write" && p.read && !p.write && len(input) == 2 && transcript.SafeFile(p.project, path, "summary.txt") && content == "total=18\n" {
			p.write, matched = true, true
			if p.scene == "s1" {
				answer = claude.PermissionAnswer{Behavior: claude.PermissionAllow}
			}
		}
		if (p.scene == "s2" || (p.scene == "r2" && p.turn == 1)) && !p.question && handle.ToolName() == "AskUserQuestion" {
			qs, ok := input["questions"].([]any)
			count := 1
			if p.scene == "r2" {
				count = 2
			}
			if ok && len(qs) == count {
				q, _ := qs[0].(map[string]any)
				valid := q["question"] == "Which synthetic color?" && exactOptions(q["options"]) && q["multiSelect"] != true
				answers := map[string]string{"Which synthetic color?": "Blue"}
				if count == 2 {
					second, _ := qs[1].(map[string]any)
					valid = valid && second["question"] == "Enter a short label." && second["multiSelect"] != true && exactLabels(second["options"], "sample", "other")
					answers["Enter a short label."] = "sample"
				}
				if valid {
					input["answers"] = answers
					updated, _ := json.Marshal(input)
					p.question, matched = true, true
					answer = claude.PermissionAnswer{Behavior: claude.PermissionAllow, UpdatedInput: updated}
				}
			}
		}
	}
	ctx, cancel := context.WithTimeout(handle.Context(), 15*time.Second)
	defer cancel()
	if err := handle.Answer(ctx, answer); err != nil {
		return err
	}
	if !matched {
		return fmt.Errorf("unknown/out-of-policy permission; scene stopped")
	}
	return nil
}

func allowedTool(scene, name string) bool {
	switch scene {
	case "s1":
		return name == "Read" || name == "Write"
	case "s2":
		return name == "AskUserQuestion"
	case "r2":
		return name == "Read" || name == "Write" || name == "AskUserQuestion"
	default:
		return false
	}
}
func executionProgress(raw json.RawMessage) bool {
	var event struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &event) != nil {
		return false
	}
	switch event.Type {
	case "message_start", "content_block_start", "content_block_delta", "message_delta":
		return true
	}
	return false
}

// Reserve owned shutdown inside the scene, including an earlier caller deadline.
func activityContext(ctx context.Context) (context.Context, context.CancelFunc) {
	end := time.Now().Add(120 * time.Second)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(end) {
		end = deadline
	}
	return context.WithDeadline(ctx, end.Add(-3*time.Second))
}
