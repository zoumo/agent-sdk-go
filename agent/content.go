package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"
	"unicode/utf8"
)

const (
	contentTypeText         = "text"
	contentTypeImage        = "image"
	contentTypeAudio        = "audio"
	contentTypeResource     = "resource"
	contentTypeResourceLink = "resource_link"
)

type annotationsJSON struct {
	Audience     []string   `json:"audience,omitempty"`
	Priority     *float64   `json:"priority,omitempty"`
	LastModified *time.Time `json:"lastModified,omitempty"`
}

type textBlockJSON struct {
	Type        string           `json:"type"`
	Text        string           `json:"text"`
	Annotations *annotationsJSON `json:"annotations,omitempty"`
}

type imageBlockJSON struct {
	Type        string           `json:"type"`
	Data        string           `json:"data"`
	MIMEType    string           `json:"mimeType"`
	URI         *string          `json:"uri,omitempty"`
	Annotations *annotationsJSON `json:"annotations,omitempty"`
}

type audioBlockJSON struct {
	Type        string           `json:"type"`
	Data        string           `json:"data"`
	MIMEType    string           `json:"mimeType"`
	Annotations *annotationsJSON `json:"annotations,omitempty"`
}

type embeddedResourceJSON struct {
	URI      string  `json:"uri"`
	MIMEType *string `json:"mimeType,omitempty"`
	Text     *string `json:"text,omitempty"`
	Blob     *string `json:"blob,omitempty"`
}

type resourceBlockJSON struct {
	Type        string               `json:"type"`
	Resource    embeddedResourceJSON `json:"resource"`
	Annotations *annotationsJSON     `json:"annotations,omitempty"`
}

type resourceLinkBlockJSON struct {
	Type        string           `json:"type"`
	URI         string           `json:"uri"`
	Name        string           `json:"name"`
	MIMEType    *string          `json:"mimeType,omitempty"`
	Title       *string          `json:"title,omitempty"`
	Description *string          `json:"description,omitempty"`
	Size        *int64           `json:"size,omitempty"`
	Annotations *annotationsJSON `json:"annotations,omitempty"`
}

// Validate checks that the input contains at least one valid content block.
func (i Input) Validate() error {
	if len(i.Content) == 0 {
		return invalidArgument("empty_input", "input must contain at least one content block")
	}
	for index, block := range i.Content {
		if err := validateContentBlock(block); err != nil {
			return invalidArgument(
				"invalid_content_block",
				"content block "+strconv.Itoa(index)+" is invalid: "+err.Error(),
			)
		}
	}
	return nil
}

// Validate checks the text block's annotations and string encoding.
func (b TextBlock) Validate() error {
	if !utf8.ValidString(b.Text) {
		return invalidArgument("invalid_text", "text is not valid UTF-8")
	}
	return validateAnnotations(b.Annotations)
}

// Validate checks required media fields, Base64 data, and annotations.
func (b ImageBlock) Validate() error {
	if b.MIMEType == "" || !utf8.ValidString(b.MIMEType) {
		return invalidArgument("missing_mime_type", "image MIME type is required")
	}
	if err := validateBase64("image data", b.Data); err != nil {
		return err
	}
	if b.URI != nil && (*b.URI == "" || !utf8.ValidString(*b.URI)) {
		return invalidArgument("invalid_uri", "image URI must be nonempty UTF-8")
	}
	return validateAnnotations(b.Annotations)
}

// Validate checks required media fields, Base64 data, and annotations.
func (b AudioBlock) Validate() error {
	if b.MIMEType == "" || !utf8.ValidString(b.MIMEType) {
		return invalidArgument("missing_mime_type", "audio MIME type is required")
	}
	if err := validateBase64("audio data", b.Data); err != nil {
		return err
	}
	return validateAnnotations(b.Annotations)
}

// Validate requires exactly one of text or blob and validates their encoding.
func (r EmbeddedResource) Validate() error {
	if r.URI == "" || !utf8.ValidString(r.URI) {
		return invalidArgument("missing_resource_uri", "resource URI is required")
	}
	if r.MIMEType != nil && (*r.MIMEType == "" || !utf8.ValidString(*r.MIMEType)) {
		return invalidArgument("invalid_mime_type", "resource MIME type must be nonempty UTF-8")
	}
	if (r.Text == nil) == (r.Blob == nil) {
		return invalidArgument(
			"invalid_resource_body",
			"resource must contain exactly one of text or blob",
		)
	}
	if r.Text != nil && !utf8.ValidString(*r.Text) {
		return invalidArgument("invalid_text", "resource text is not valid UTF-8")
	}
	if r.Blob != nil {
		return validateBase64("resource blob", *r.Blob)
	}
	return nil
}

// Validate checks the embedded resource and annotations.
func (b ResourceBlock) Validate() error {
	if err := b.Resource.Validate(); err != nil {
		return err
	}
	return validateAnnotations(b.Annotations)
}

// Validate checks required link fields, nonnegative size, and annotations.
func (b ResourceLinkBlock) Validate() error {
	if b.URI == "" || !utf8.ValidString(b.URI) {
		return invalidArgument("missing_resource_uri", "resource link URI is required")
	}
	if b.Name == "" || !utf8.ValidString(b.Name) {
		return invalidArgument("missing_resource_name", "resource link name is required")
	}
	if b.MIMEType != nil && (*b.MIMEType == "" || !utf8.ValidString(*b.MIMEType)) {
		return invalidArgument("invalid_mime_type", "resource link MIME type must be nonempty UTF-8")
	}
	if b.Title != nil && !utf8.ValidString(*b.Title) {
		return invalidArgument("invalid_title", "resource link title is not valid UTF-8")
	}
	if b.Description != nil && !utf8.ValidString(*b.Description) {
		return invalidArgument("invalid_description", "resource link description is not valid UTF-8")
	}
	if b.Size != nil && *b.Size < 0 {
		return invalidArgument("invalid_resource_size", "resource link size must not be negative")
	}
	return validateAnnotations(b.Annotations)
}

func (b TextBlock) MarshalJSON() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(textBlockJSON{
		Type:        contentTypeText,
		Text:        b.Text,
		Annotations: annotationsToJSON(b.Annotations),
	})
}

func (b ImageBlock) MarshalJSON() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(imageBlockJSON{
		Type:        contentTypeImage,
		Data:        b.Data,
		MIMEType:    b.MIMEType,
		URI:         b.URI,
		Annotations: annotationsToJSON(b.Annotations),
	})
}

func (b AudioBlock) MarshalJSON() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(audioBlockJSON{
		Type:        contentTypeAudio,
		Data:        b.Data,
		MIMEType:    b.MIMEType,
		Annotations: annotationsToJSON(b.Annotations),
	})
}

func (b ResourceBlock) MarshalJSON() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(resourceBlockJSON{
		Type: contentTypeResource,
		Resource: embeddedResourceJSON{
			URI:      b.Resource.URI,
			MIMEType: b.Resource.MIMEType,
			Text:     b.Resource.Text,
			Blob:     b.Resource.Blob,
		},
		Annotations: annotationsToJSON(b.Annotations),
	})
}

func (b ResourceLinkBlock) MarshalJSON() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(resourceLinkBlockJSON{
		Type:        contentTypeResourceLink,
		URI:         b.URI,
		Name:        b.Name,
		MIMEType:    b.MIMEType,
		Title:       b.Title,
		Description: b.Description,
		Size:        b.Size,
		Annotations: annotationsToJSON(b.Annotations),
	})
}

func (i Input) MarshalJSON() ([]byte, error) {
	if err := i.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Content []ContentBlock `json:"content"`
	}{Content: i.Content})
}

func (i *Input) UnmarshalJSON(data []byte) error {
	var wire struct {
		Content []json.RawMessage `json:"content"`
	}
	if err := decodeStrictJSON(data, &wire); err != nil {
		return invalidArgument("invalid_input_json", "decode input: "+err.Error())
	}
	content := make([]ContentBlock, len(wire.Content))
	for index, raw := range wire.Content {
		block, err := unmarshalContentBlock(raw)
		if err != nil {
			return invalidArgument(
				"invalid_content_block",
				"decode content block "+strconv.Itoa(index)+": "+err.Error(),
			)
		}
		content[index] = block
	}
	i.Content = content
	return i.Validate()
}

func validateContentBlock(block ContentBlock) error {
	switch value := block.(type) {
	case TextBlock:
		return value.Validate()
	case *TextBlock:
		if value == nil {
			return invalidArgument("nil_content_block", "content block is nil")
		}
		return value.Validate()
	case ImageBlock:
		return value.Validate()
	case *ImageBlock:
		if value == nil {
			return invalidArgument("nil_content_block", "content block is nil")
		}
		return value.Validate()
	case AudioBlock:
		return value.Validate()
	case *AudioBlock:
		if value == nil {
			return invalidArgument("nil_content_block", "content block is nil")
		}
		return value.Validate()
	case ResourceBlock:
		return value.Validate()
	case *ResourceBlock:
		if value == nil {
			return invalidArgument("nil_content_block", "content block is nil")
		}
		return value.Validate()
	case ResourceLinkBlock:
		return value.Validate()
	case *ResourceLinkBlock:
		if value == nil {
			return invalidArgument("nil_content_block", "content block is nil")
		}
		return value.Validate()
	default:
		return invalidArgument("unsupported_content_block", "content block kind is not supported")
	}
}

func unmarshalContentBlock(data []byte) (ContentBlock, error) {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}

	switch envelope.Type {
	case contentTypeText:
		var wire textBlockJSON
		if err := decodeStrictJSON(data, &wire); err != nil {
			return nil, err
		}
		if !hasJSONMember(data, "text") {
			return nil, errors.New("missing text")
		}
		block := TextBlock{Text: wire.Text, Annotations: annotationsFromJSON(wire.Annotations)}
		return block, block.Validate()
	case contentTypeImage:
		var wire imageBlockJSON
		if err := decodeStrictJSON(data, &wire); err != nil {
			return nil, err
		}
		block := ImageBlock{
			Data:        wire.Data,
			MIMEType:    wire.MIMEType,
			URI:         wire.URI,
			Annotations: annotationsFromJSON(wire.Annotations),
		}
		return block, block.Validate()
	case contentTypeAudio:
		var wire audioBlockJSON
		if err := decodeStrictJSON(data, &wire); err != nil {
			return nil, err
		}
		block := AudioBlock{
			Data:        wire.Data,
			MIMEType:    wire.MIMEType,
			Annotations: annotationsFromJSON(wire.Annotations),
		}
		return block, block.Validate()
	case contentTypeResource:
		var wire resourceBlockJSON
		if err := decodeStrictJSON(data, &wire); err != nil {
			return nil, err
		}
		block := ResourceBlock{
			Resource: EmbeddedResource{
				URI:      wire.Resource.URI,
				MIMEType: wire.Resource.MIMEType,
				Text:     wire.Resource.Text,
				Blob:     wire.Resource.Blob,
			},
			Annotations: annotationsFromJSON(wire.Annotations),
		}
		return block, block.Validate()
	case contentTypeResourceLink:
		var wire resourceLinkBlockJSON
		if err := decodeStrictJSON(data, &wire); err != nil {
			return nil, err
		}
		block := ResourceLinkBlock{
			URI:         wire.URI,
			Name:        wire.Name,
			MIMEType:    wire.MIMEType,
			Title:       wire.Title,
			Description: wire.Description,
			Size:        wire.Size,
			Annotations: annotationsFromJSON(wire.Annotations),
		}
		return block, block.Validate()
	default:
		return nil, errors.New("unsupported content type")
	}
}

func validateAnnotations(annotations *Annotations) error {
	if annotations == nil {
		return nil
	}
	for _, audience := range annotations.Audience {
		if audience != "user" && audience != "assistant" {
			return invalidArgument("invalid_audience", "annotation audience must be user or assistant")
		}
	}
	if annotations.Priority != nil &&
		(*annotations.Priority < 0 || *annotations.Priority > 1) {
		return invalidArgument("invalid_priority", "annotation priority must be between 0 and 1")
	}
	return nil
}

func validateBase64(field, value string) error {
	if _, err := base64.StdEncoding.Strict().DecodeString(value); err != nil {
		return invalidArgument("invalid_base64", field+" must be valid Base64")
	}
	return nil
}

func annotationsToJSON(annotations *Annotations) *annotationsJSON {
	if annotations == nil {
		return nil
	}
	return &annotationsJSON{
		Audience:     annotations.Audience,
		Priority:     annotations.Priority,
		LastModified: annotations.LastModified,
	}
}

func annotationsFromJSON(annotations *annotationsJSON) *Annotations {
	if annotations == nil {
		return nil
	}
	return &Annotations{
		Audience:     annotations.Audience,
		Priority:     annotations.Priority,
		LastModified: annotations.LastModified,
	}
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("JSON input must contain exactly one value")
	}
	return nil
}

func hasJSONMember(data []byte, name string) bool {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return false
	}
	_, ok := members[name]
	return ok
}
