// SPDX-License-Identifier: Apache-2.0

package claude

import (
	"encoding/json"
	"fmt"
	"reflect"
	"unicode/utf8"
)

// Check caller-owned native values before encoding/json can replace invalid
// UTF-8. The input structs contain only native values, Raw and presence maps.
func validateInputEncoding(value any) error {
	return validateNativeEncoding(reflect.ValueOf(value))
}

func validateNativeEncoding(v reflect.Value) error {
	if !v.IsValid() {
		return nil
	}
	if v.Type() == reflect.TypeFor[json.RawMessage]() {
		if v.IsNil() || v.Len() == 0 {
			return nil
		}
		data := v.Bytes()
		if !utf8.Valid(data) || !json.Valid(data) {
			return fmt.Errorf("%w: invalid Raw JSON or UTF-8", ErrInvalidInput)
		}
		return nil
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			return validateNativeEncoding(v.Elem())
		}
	case reflect.String:
		if !utf8.ValidString(v.String()) {
			return fmt.Errorf("%w: invalid UTF-8 string", ErrInvalidInput)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if err := validateNativeEncoding(v.Field(i)); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if err := validateNativeEncoding(v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		it := v.MapRange()
		for it.Next() {
			if err := validateNativeEncoding(it.Key()); err != nil {
				return err
			}
			if err := validateNativeEncoding(it.Value()); err != nil {
				return err
			}
		}
	}
	return nil
}
