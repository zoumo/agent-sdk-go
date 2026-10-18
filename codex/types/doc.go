// Package types exposes native app-server parameters, results, and recursive
// model types from the exact official Codex revision recorded in the generated
// source. Experimental fields are included; using them may require negotiating
// experimentalApi during initialization.
//
// Optional fields use Optional to distinguish omission, explicit null, and
// present zero values. Required nullable fields encode an unset Optional as null.
// Missing required members and malformed known variants fail decoding. Unknown
// object members are retained in UnknownFields, and unknown union variants in
// Unknown, so future data can be inspected and round-tripped without float64
// coercion. String enums accept future values.
//
// A union has one pointer field per native variant and an Unknown raw fallback.
// Set exactly one field, using Ptr if convenient. Object variants automatically
// emit their constant type discriminator. Required empty arrays encode as []
// even when the Go slice is nil. UnknownFields must not duplicate known fields.
//
// Domain constraints such as absolute-path validation and experimental feature
// gates remain server-enforced.
//
// These types are protocol bindings, not evidence of live-server compatibility.
package types
