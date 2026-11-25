package types

import "encoding/json"

// W3cTraceContext is the optional request-envelope carrier from
// codex-rs/app-server-protocol/src/rpc.rs at revision
// 092d3acd6bec3e3a14bdc7e7a2810ab628ab759d. It is handwritten because the
// request envelope is outside the generated parameter/result schema closure.
// Encoding preserves native optional values; Session.Submit separately checks
// W3C syntax before propagating a carrier.
type W3cTraceContext struct {
	Traceparent   Optional[string]           `json:"traceparent" protocol:"nullable"`
	Tracestate    Optional[string]           `json:"tracestate" protocol:"nullable"`
	UnknownFields map[string]json.RawMessage `json:"-"`
}

func (v W3cTraceContext) MarshalJSON() ([]byte, error)     { return marshalObject(v) }
func (v *W3cTraceContext) UnmarshalJSON(data []byte) error { return unmarshalObject(data, v) }
