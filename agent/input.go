package agent

import "time"

// Input is an ordered sequence of adapter-neutral content blocks.
//
//nolint:recvcheck // JSON decoding mutates input; reads and encoding accept values.
type Input struct {
	Content []ContentBlock
}

// ContentBlock is one of the five supported input content kinds. Exactly one
// branch must be non-nil.
//
//nolint:recvcheck // JSON decoding mutates blocks; reads and encoding accept values.
type ContentBlock struct {
	Text             *ContentBlockText             `json:"-"`
	Image            *ContentBlockImage            `json:"-"`
	Audio            *ContentBlockAudio            `json:"-"`
	EmbeddedResource *ContentBlockEmbeddedResource `json:"-"`
	ResourceLink     *ContentBlockResourceLink     `json:"-"`
}

type ContentBlockText struct {
	Text        string
	Annotations *Annotations
}

type ContentBlockImage struct {
	Data        string
	MIMEType    string
	URI         *string
	Annotations *Annotations
}

type ContentBlockAudio struct {
	Data        string
	MIMEType    string
	Annotations *Annotations
}

type EmbeddedResource struct {
	URI      string
	MIMEType *string
	Text     *string
	Blob     *string
}

// ContentBlockEmbeddedResource carries the resource body in the block.
type ContentBlockEmbeddedResource struct {
	Resource    EmbeddedResource
	Annotations *Annotations
}

type ContentBlockResourceLink struct {
	URI         string
	Name        string
	MIMEType    *string
	Title       *string
	Description *string
	Size        *int64
	Icons       []ResourceIcon
	Annotations *Annotations
}

// ResourceIcon describes an optional display icon for a resource link.
type ResourceIcon struct {
	Src      string   `json:"src"`
	MIMEType *string  `json:"mimeType,omitempty"`
	Sizes    []string `json:"sizes,omitempty"`
	Theme    *string  `json:"theme,omitempty"`
}

type Annotations struct {
	Audience     []string
	Priority     *float64
	LastModified *time.Time
}
