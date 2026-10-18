package types

import (
	"bytes"
	"encoding/json"
	"reflect"
)

// Optional preserves the three distinct JSON field states: omitted, null, and
// present with a value. Its zero value is omitted. Use Some or Null to construct
// a present value. Generated object codecs omit unset fields without relying on
// encoding/json's treatment of zero-valued structs.
type Optional[T any] struct {
	value T
	set   bool
	null  bool
}

// Some constructs a present, non-null field, including false, zero, or "".
func Some[T any](value T) Optional[T] { return Optional[T]{value: value, set: true} }

// Null constructs a present JSON null field. Only nullable schema fields accept it.
func Null[T any]() Optional[T] { return Optional[T]{set: true, null: true} }

// Ptr returns a pointer to value, useful when selecting a typed union variant.
func Ptr[T any](value T) *T { return &value }

// IsSet reports whether the field is present, including an explicit null.
func (v Optional[T]) IsSet() bool { return v.set }

// IsNull reports whether the field is an explicit JSON null.
func (v Optional[T]) IsNull() bool { return v.set && v.null }

// Get returns the value and true only for a present, non-null field.
func (v Optional[T]) Get() (T, bool) { return v.value, v.set && !v.null }

// ValueOr returns the value or fallback if the field is omitted or null.
func (v Optional[T]) ValueOr(fallback T) T {
	if value, ok := v.Get(); ok {
		return value
	}
	return fallback
}

// MarshalJSON encodes a present value. Outside a generated object, an unset
// Optional encodes as null because JSON values have no standalone omitted state.
func (v Optional[T]) MarshalJSON() ([]byte, error) {
	if !v.set || v.null {
		return []byte("null"), nil
	}
	if err := validateGoJSONValue(reflect.ValueOf(v.value)); err != nil {
		return nil, err
	}
	return json.Marshal(v.value)
}

// UnmarshalJSON records null separately from a value. It replaces the receiver
// only after successful decoding.
func (v *Optional[T]) UnmarshalJSON(data []byte) error {
	if err := validateJSONDocument(data); err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*v = Null[T]()
		return nil
	}
	var value T
	if err := validateJSONType(data, reflect.TypeFor[T]()); err != nil {
		return err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*v = Some(value)
	return nil
}
