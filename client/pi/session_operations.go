package pi

import (
	"context"
	"errors"
	"fmt"
)

// ForkMessage identifies a user message Pi offers as a fork point.
type ForkMessage struct {
	EntryID string `json:"entryId"`
	Text    string `json:"text"`
}

// ForkMessages lists the current session's fork points in Pi's order.
func (s *Session) ForkMessages(ctx context.Context) ([]ForkMessage, error) {
	const cmd = "get_fork_messages"
	var data struct {
		Messages []struct {
			EntryID *string `json:"entryId"`
			Text    *string `json:"text"`
		} `json:"messages"`
	}
	_, err := s.readData(ctx, cmd, nil, &data)
	rows, err := requiredList(cmd, "messages", data.Messages, err)
	if err != nil {
		return nil, err
	}
	messages := make([]ForkMessage, len(rows))
	for i, row := range rows {
		if row.EntryID == nil || row.Text == nil {
			return nil, malformedData(cmd, fmt.Errorf("messages[%d] requires entryId and text", i))
		}
		messages[i] = ForkMessage{EntryID: *row.EntryID, Text: *row.Text}
	}
	return messages, nil
}

// LastAssistantText reads Pi's selection of the last assistant text. It returns
// nil when Pi omits the text or reports null.
func (s *Session) LastAssistantText(ctx context.Context) (*string, error) {
	const cmd = "get_last_assistant_text"
	var data *struct {
		Text *string `json:"text"`
	}
	if _, err := s.readData(ctx, cmd, nil, &data); err != nil {
		return nil, err
	}
	if data == nil {
		return nil, malformedData(cmd, errors.New("expected an object"))
	}
	return data.Text, nil
}

// SetSessionName asks Pi to rename the current session. Canceling the wait
// after dispatch leaves Pi free to apply the name.
func (s *Session) SetSessionName(ctx context.Context, name string) error {
	_, err := s.request(ctx, "set_session_name", map[string]any{"name": name})
	return err
}

// ExportHTML asks Pi to write the session as HTML and returns the path from
// Pi's filesystem. An empty outputPath lets Pi choose the destination.
// Pi may finish writing the file after the caller cancels the wait.
func (s *Session) ExportHTML(ctx context.Context, outputPath string) (string, error) {
	const cmd = "export_html"
	var fields map[string]any
	if outputPath != "" {
		fields = map[string]any{"outputPath": outputPath}
	}
	var data struct {
		Path string `json:"path"`
	}
	if _, err := s.readData(ctx, cmd, fields, &data); err != nil {
		return "", err
	}
	if data.Path == "" {
		return "", malformedData(cmd, errors.New("missing export path"))
	}
	return data.Path, nil
}
