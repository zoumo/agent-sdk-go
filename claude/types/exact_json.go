// SPDX-License-Identifier: Apache-2.0
package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

// unmarshalExact decodes only exact native member names. Unknown casing remains
// in the caller's original Raw, and cannot overwrite a known typed field.
func unmarshalExact(data []byte, dst any) error {
	if err := validateJSONDocument(data); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("expected JSON object")
	}
	value := reflect.ValueOf(dst).Elem()
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		key := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if key == "" || key == "-" {
			continue
		}
		if raw, ok := fields[key]; ok {
			if err := json.Unmarshal(raw, value.Field(i).Addr().Interface()); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateJSONDocument(data []byte) error {
	if !json.Valid(data) {
		return fmt.Errorf("protocol: invalid JSON")
	}
	if !utf8.Valid(data) {
		return fmt.Errorf("protocol: invalid UTF-8 in JSON")
	}
	for i := 0; i < len(data); i++ {
		if data[i] != '"' {
			continue
		}
		for i++; i < len(data) && data[i] != '"'; i++ {
			if data[i] != '\\' {
				continue
			}
			i++
			if data[i] != 'u' {
				continue
			}
			code := hexQuad(data[i+1 : i+5])
			i += 4
			if code >= 0xD800 && code <= 0xDBFF {
				if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
					return fmt.Errorf("protocol: unpaired Unicode surrogate in JSON")
				}
				low := hexQuad(data[i+3 : i+7])
				if low < 0xDC00 || low > 0xDFFF {
					return fmt.Errorf("protocol: unpaired Unicode surrogate in JSON")
				}
				i += 6
			} else if code >= 0xDC00 && code <= 0xDFFF {
				return fmt.Errorf("protocol: unpaired Unicode surrogate in JSON")
			}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return validateUniqueMembers(decoder)
}

func hexQuad(data []byte) uint16 {
	var value uint16
	for _, b := range data {
		value <<= 4
		switch {
		case b >= '0' && b <= '9':
			value += uint16(b - '0')
		case b >= 'a' && b <= 'f':
			value += uint16(b - 'a' + 10)
		case b >= 'A' && b <= 'F':
			value += uint16(b - 'A' + 10)
		}
	}
	return value
}

func validateUniqueMembers(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch token {
	case json.Delim('{'):
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("protocol: invalid object member")
			}
			if seen[name] {
				return fmt.Errorf("protocol: duplicate JSON object member")
			}
			seen[name] = true
			if err := validateUniqueMembers(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
	case json.Delim('['):
		for decoder.More() {
			if err := validateUniqueMembers(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
	}
	return err
}
