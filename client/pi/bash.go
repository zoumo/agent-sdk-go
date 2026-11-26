package pi

import (
	"context"
	"errors"
)

// BashResult is Pi's final result for a user shell command.
type BashResult struct {
	Output         string  `json:"output"`
	ExitCode       *int    `json:"exitCode,omitempty"` // Pi may omit this when the process is killed.
	Cancelled      bool    `json:"cancelled"`
	Truncated      bool    `json:"truncated"`
	FullOutputPath *string `json:"fullOutputPath,omitempty"` // Path on the Pi host when Pi saves the full output.
}

// Bash asks the Pi process to execute a user shell command in the session workspace.
// A nil excludeFromContext uses Pi's default. Pass true to keep the result out of the model context.
// Canceling ctx ends the caller's wait. A dispatched command can continue until it exits or AbortBash stops it.
func (s *Session) Bash(ctx context.Context, command string, excludeFromContext *bool) (BashResult, error) {
	fields := map[string]any{"command": command}
	if excludeFromContext != nil {
		fields["excludeFromContext"] = *excludeFromContext
	}
	var data struct {
		Output         *string `json:"output"`
		ExitCode       *int    `json:"exitCode"`
		Cancelled      *bool   `json:"cancelled"`
		Truncated      *bool   `json:"truncated"`
		FullOutputPath *string `json:"fullOutputPath"`
	}
	if _, err := s.readData(ctx, "bash", fields, &data); err != nil {
		return BashResult{}, err
	}
	if data.Output == nil || data.Cancelled == nil || data.Truncated == nil {
		return BashResult{}, malformedData("bash", errors.New("output, cancelled and truncated are required"))
	}
	return BashResult{
		Output: *data.Output, ExitCode: data.ExitCode, Cancelled: *data.Cancelled,
		Truncated: *data.Truncated, FullOutputPath: data.FullOutputPath,
	}, nil
}

// AbortBash asks Pi to stop the running user shell command and waits for its acknowledgement.
// A caller still waiting in Bash receives the command's final result separately.
func (s *Session) AbortBash(ctx context.Context) error {
	_, err := s.request(ctx, "abort_bash", nil)
	return err
}
