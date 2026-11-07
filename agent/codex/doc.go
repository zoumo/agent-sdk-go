// Package agentcodex adapts the native Codex app-server SDK to the optional
// provider-neutral agent runtime contract.
//
// A Provider owns one shared Codex connection. Sessions attach to native
// threads on that connection and expose one live unified event stream. Native
// history queries remain available through the provider-specific NativeClient
// escape hatch; this package defines no unified history or replay API.
package agentcodex
