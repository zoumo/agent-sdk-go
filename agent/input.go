package agent

import "time"

// Input is an ordered sequence of provider-neutral content blocks.
//
//nolint:recvcheck // JSON decoding mutates input; reads and encoding accept values.
type Input struct {
	Content []ContentBlock
}

// ContentBlock is one of the five supported input content kinds.
type ContentBlock interface {
	contentBlock()
}

type TextBlock struct {
	Text        string
	Annotations *Annotations
}

func (TextBlock) contentBlock() {}

type ImageBlock struct {
	Data        string
	MIMEType    string
	URI         *string
	Annotations *Annotations
}

func (ImageBlock) contentBlock() {}

type AudioBlock struct {
	Data        string
	MIMEType    string
	Annotations *Annotations
}

func (AudioBlock) contentBlock() {}

type EmbeddedResource struct {
	URI      string
	MIMEType *string
	Text     *string
	Blob     *string
}

type ResourceBlock struct {
	Resource    EmbeddedResource
	Annotations *Annotations
}

func (ResourceBlock) contentBlock() {}

type ResourceLinkBlock struct {
	URI         string
	Name        string
	MIMEType    *string
	Title       *string
	Description *string
	Size        *int64
	Annotations *Annotations
}

func (ResourceLinkBlock) contentBlock() {}

type Annotations struct {
	Audience     []string
	Priority     *float64
	LastModified *time.Time
}
