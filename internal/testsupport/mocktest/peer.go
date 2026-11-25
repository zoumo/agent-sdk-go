// Package mocktest bridges Go SDK fixture assertions to the in-process Replay
// library. It implements no native matching or request-correlation state.
package mocktest

type Options struct {
	Handshakes       string
	MaxFrames        int
	RecordingVersion string
}
