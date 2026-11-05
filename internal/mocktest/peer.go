// Package mocktest bridges Go SDK fixture assertions to the external mock
// process. It implements no native matching or request-correlation state.
package mocktest

import (
	"errors"
)

var ErrDisconnected = errors.New("scripted peer disconnect")

type Options struct {
	Handshakes       string
	MaxFrames        int
	RecordingVersion string
	Complete         func() error
}
