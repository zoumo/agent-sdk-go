// SPDX-License-Identifier: Apache-2.0

package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// AskUserQuestionInput is tool input, not a stream envelope or answer authority.
// Unknown top-level, question and option fields survive typed edits.
type AskUserQuestionInput struct {
	Questions []Question                `json:"questions"`
	Answers   map[string]QuestionAnswer `json:"answers,omitempty"`
	Response  *string                   `json:"response,omitempty"`
	Raw       json.RawMessage           `json:"-"`
	Fields    map[string]FieldState     `json:"-"`
}
type Question struct {
	Question    string                `json:"question"`
	Header      string                `json:"header,omitempty"`
	Options     []QuestionOption      `json:"options"`
	MultiSelect bool                  `json:"multiSelect,omitempty"`
	Raw         json.RawMessage       `json:"-"`
	Fields      map[string]FieldState `json:"-"`
}
type QuestionOption struct {
	Label       string                `json:"label"`
	Description string                `json:"description,omitempty"`
	Preview     *string               `json:"preview,omitempty"`
	Raw         json.RawMessage       `json:"-"`
	Fields      map[string]FieldState `json:"-"`
}

// QuestionAnswer is either a label/free-text string or an array of labels for
// a multi-select question. Exactly one alternative must be set. Empty strings
// and explicit empty arrays are allowed; null and zero values are invalid.
type QuestionAnswer struct {
	Text   *string
	Labels []string
}

func (v *QuestionAnswer) UnmarshalJSON(data []byte) error {
	*v = QuestionAnswer{}
	if !utf8.Valid(data) {
		return fmt.Errorf("invalid UTF-8")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return fmt.Errorf("empty answer")
	}
	var next QuestionAnswer
	switch data[0] {
	case '"':
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		next.Text = &text
	case '[':
		// Decode elements individually so JSON null cannot silently become "".
		var labels []json.RawMessage
		if err := json.Unmarshal(data, &labels); err != nil {
			return err
		}
		next.Labels = make([]string, len(labels))
		for i, b := range labels {
			if len(b) == 0 || b[0] != '"' {
				return fmt.Errorf("answer labels must be strings")
			}
			if err := json.Unmarshal(b, &next.Labels[i]); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("answer must be string or string array")
	}
	*v = next
	return nil
}
func (v QuestionAnswer) MarshalJSON() ([]byte, error) {
	if v.Text != nil && v.Labels == nil {
		if !utf8.ValidString(*v.Text) {
			return nil, fmt.Errorf("invalid UTF-8 answer")
		}
		return json.Marshal(*v.Text)
	}
	if v.Text == nil && v.Labels != nil {
		for _, label := range v.Labels {
			if !utf8.ValidString(label) {
				return nil, fmt.Errorf("invalid UTF-8 label")
			}
		}
		return json.Marshal(v.Labels)
	}
	return nil, fmt.Errorf("answer requires exactly one string or string array")
}
func validateQuestion(m map[string]json.RawMessage) error {
	if _, err := requiredString(m, "question"); err != nil {
		return err
	}
	b, ok := m["options"]
	if !ok || len(bytes.TrimSpace(b)) == 0 || bytes.TrimSpace(b)[0] != '[' {
		return fmt.Errorf("question requires options array")
	}
	var options []QuestionOption
	return json.Unmarshal(b, &options)
}
func validateOption(m map[string]json.RawMessage) error {
	_, err := requiredString(m, "label")
	return err
}
func validateQuestions(m map[string]json.RawMessage) error {
	b := bytes.TrimSpace(m["questions"])
	if len(b) == 0 || b[0] != '[' {
		return fmt.Errorf("questions must be an array")
	}
	var questions []Question
	if err := json.Unmarshal(b, &questions); err != nil {
		return err
	}
	known := map[string]bool{}
	for _, q := range questions {
		if _, exists := known[q.Question]; exists {
			return fmt.Errorf("duplicate question text %q", q.Question)
		}
		known[q.Question] = q.MultiSelect
	}
	if b, ok := m["answers"]; ok {
		if err := objectOnly(b); err != nil {
			return err
		}
		var answers map[string]QuestionAnswer
		if err := json.Unmarshal(b, &answers); err != nil {
			return err
		}
		for key, a := range answers {
			multi, exists := known[key]
			if !exists {
				return fmt.Errorf("answer key must be full question text: %q", key)
			}
			if a.Labels != nil && !multi {
				return fmt.Errorf("array answer requires multiSelect: %q", key)
			}
		}
	}
	return nil
}

// WithAnswers replaces answers while preserving original questions and all
// unknown fields. Keys must match full question text; partial maps are allowed.
// It returns detached JSON for PermissionAnswer.UpdatedInput and sends nothing.
func (v AskUserQuestionInput) WithAnswers(answers map[string]QuestionAnswer) (json.RawMessage, error) {
	if answers == nil {
		return nil, fmt.Errorf("answers must be an object")
	}
	v.Answers = answers
	v.Fields = cloneStates(v.Fields)
	v.Fields["answers"] = Present
	return json.Marshal(v)
}
func (v *AskUserQuestionInput) UnmarshalJSON(data []byte) error {
	*v = AskUserQuestionInput{}
	if err := validObject(data, validateQuestions); err != nil {
		return err
	}
	type wire AskUserQuestionInput
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = AskUserQuestionInput(next)
	return nil
}
func (v AskUserQuestionInput) MarshalJSON() ([]byte, error) {
	type wire AskUserQuestionInput
	b, err := objectJSON(wire(v), v.Raw, v.Fields)
	if err != nil {
		return nil, err
	}
	return b, validObject(b, validateQuestions)
}
func (v *Question) UnmarshalJSON(data []byte) error {
	*v = Question{}
	if err := validObject(data, validateQuestion); err != nil {
		return err
	}
	type wire Question
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = Question(next)
	return nil
}
func (v Question) MarshalJSON() ([]byte, error) {
	type wire Question
	b, err := objectJSON(wire(v), v.Raw, v.Fields)
	if err != nil {
		return nil, err
	}
	return b, validObject(b, validateQuestion)
}
func (v *QuestionOption) UnmarshalJSON(data []byte) error {
	*v = QuestionOption{}
	if err := validObject(data, validateOption); err != nil {
		return err
	}
	type wire QuestionOption
	var next wire
	if err := json.Unmarshal(data, &next); err != nil {
		return err
	}
	next.Raw = bytes.Clone(data)
	*v = QuestionOption(next)
	return nil
}
func (v QuestionOption) MarshalJSON() ([]byte, error) {
	type wire QuestionOption
	b, err := objectJSON(wire(v), v.Raw, v.Fields)
	if err != nil {
		return nil, err
	}
	return b, validObject(b, validateOption)
}
