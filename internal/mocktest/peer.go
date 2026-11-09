// Package mocktest bridges Go SDK fixture assertions to the in-process testify
// library. It implements no native matching or request-correlation state.
package mocktest

type Options struct {
	Handshakes       string
	MaxFrames        int
	RecordingVersion string
}
