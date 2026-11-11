package agent

import "time"

// Input is an ordered sequence of provider-neutral content blocks.
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
	Text         *ContentBlockText         `json:"-"`
	Image        *ContentBlockImage        `json:"-"`
	Audio        *ContentBlockAudio        `json:"-"`
	ResourceLink *ContentBlockResourceLink `json:"-"`
	Resource     *ContentBlockResource     `json:"-"`
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

type ContentBlockResource struct {
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
	Annotations *Annotations
}

type Annotations struct {
	Audience     []string
	Priority     *float64
	LastModified *time.Time
}
