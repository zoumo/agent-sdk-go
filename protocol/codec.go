package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

type optionalState interface {
	IsSet() bool
	IsNull() bool
}

var rawMessageType = reflect.TypeFor[json.RawMessage]()

func fieldFlags(field reflect.StructField) (required, nullable bool, constant string) {
	for _, flag := range strings.Split(field.Tag.Get("protocol"), ",") {
		switch {
		case flag == "required":
			required = true
		case flag == "nullable":
			nullable = true
		case strings.HasPrefix(flag, "const="):
			constant = strings.TrimPrefix(flag, "const=")
		}
	}
	return
}

func marshalObject(value any, alternatives ...[]string) ([]byte, error) {
	v := reflect.ValueOf(value)
	t := v.Type()
	fields := make(map[string]json.RawMessage)
	known := make(map[string]bool, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		name := field.Tag.Get("json")
		if name == "-" || name == "" {
			continue
		}
		known[name] = true
		required, nullable, constant := fieldFlags(field)
		fv := v.Field(i)
		if err := validateGoJSONValue(fv); err != nil {
			return nil, fmt.Errorf("protocol: %s.%s: %w", t.Name(), name, err)
		}
		if state, ok := fv.Interface().(optionalState); ok {
			if !state.IsSet() {
				if !required {
					continue
				}
				if nullable {
					fields[name] = json.RawMessage("null")
					continue
				}
				return nil, fmt.Errorf("protocol: %s.%s is required", t.Name(), name)
			}
			if state.IsNull() && !nullable {
				return nil, fmt.Errorf("protocol: %s.%s cannot be null", t.Name(), name)
			}
		}
		var data []byte
		var err error
		switch {
		case constant != "" && fv.Kind() == reflect.String:
			if fv.String() != "" && fv.String() != constant {
				return nil, fmt.Errorf("protocol: %s.%s must be %q", t.Name(), name, constant)
			}
			data, err = json.Marshal(constant)
		case fv.Kind() == reflect.Slice && fv.Type() != rawMessageType && fv.IsNil() && !nullable:
			data = []byte("[]")
		case fv.Kind() == reflect.Map && fv.IsNil() && !nullable:
			data = []byte("{}")
		default:
			data, err = json.Marshal(fv.Interface())
		}
		if err != nil {
			return nil, fmt.Errorf("protocol: %s.%s: %w", t.Name(), name, err)
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) && !nullable {
			return nil, fmt.Errorf("protocol: %s.%s cannot be null", t.Name(), name)
		}
		fields[name] = data
	}
	if err := validateAlternatives(fields, alternatives); err != nil {
		return nil, fmt.Errorf("protocol: %s: %w", t.Name(), err)
	}
	if unknown := v.FieldByName("UnknownFields"); unknown.IsValid() && !unknown.IsNil() {
		if err := validateGoJSONValue(unknown); err != nil {
			return nil, err
		}
		for key, data := range unknown.Interface().(map[string]json.RawMessage) {
			if known[key] {
				return nil, fmt.Errorf("protocol: %s.UnknownFields duplicates known member %q", t.Name(), key)
			}
			fields[key] = data
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	if err := validateJSONDocument(encoded); err != nil {
		return nil, err
	}
	return encoded, nil
}

func unmarshalObject(data []byte, target any, alternatives ...[]string) error {
	if err := validateJSONDocument(data); err != nil {
		return err
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return fmt.Errorf("protocol: expected JSON object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if err := validateAlternatives(fields, alternatives); err != nil {
		return err
	}
	dst := reflect.ValueOf(target).Elem()
	out := reflect.New(dst.Type()).Elem()
	t := out.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		name := field.Tag.Get("json")
		if name == "-" || name == "" {
			continue
		}
		required, nullable, constant := fieldFlags(field)
		raw, found := fields[name]
		if !found {
			if required {
				return fmt.Errorf("protocol: %s.%s is required", t.Name(), name)
			}
			continue
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && !nullable {
			return fmt.Errorf("protocol: %s.%s cannot be null", t.Name(), name)
		}
		if err := validateJSONType(raw, field.Type); err != nil {
			return fmt.Errorf("protocol: %s.%s: %w", t.Name(), name, err)
		}
		if err := json.Unmarshal(raw, out.Field(i).Addr().Interface()); err != nil {
			return fmt.Errorf("protocol: %s.%s: %w", t.Name(), name, err)
		}
		if constant != "" && out.Field(i).String() != constant {
			return fmt.Errorf("protocol: %s.%s must be %q", t.Name(), name, constant)
		}
		delete(fields, name)
	}
	if len(fields) > 0 {
		out.FieldByName("UnknownFields").Set(reflect.ValueOf(fields))
	}
	dst.Set(out)
	return nil
}

func validateAlternatives(fields map[string]json.RawMessage, alternatives [][]string) error {
	if len(alternatives) == 0 {
		return nil
	}
	for _, group := range alternatives {
		matches := true
		for _, key := range group {
			if _, ok := fields[key]; !ok {
				matches = false
			}
		}
		if matches {
			return nil
		}
	}
	return fmt.Errorf("protocol: object does not match any required member alternative")
}

// validateJSONType closes encoding/json's null-to-zero loophole for scalar
// elements nested inside arrays and maps. Generated object/union codecs perform
// their own member validation; arbitrary JSON remains deliberately opaque.
func validateJSONType(data []byte, typ reflect.Type) error {
	if typ == rawMessageType {
		return nil
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return fmt.Errorf("empty JSON value")
	}
	if typ.Kind() == reflect.Struct {
		return nil // Optional or generated object's custom UnmarshalJSON validates it.
	}
	if bytes.Equal(data, []byte("null")) {
		return fmt.Errorf("unexpected null for %s", typ)
	}
	switch typ.Kind() {
	case reflect.Slice, reflect.Array:
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return err
		}
		for _, item := range items {
			if err := validateJSONType(item, typ.Elem()); err != nil {
				return err
			}
		}
	case reflect.Map:
		var items map[string]json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return err
		}
		for _, item := range items {
			if err := validateJSONType(item, typ.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

func marshalUnion(unknown json.RawMessage, variants ...any) ([]byte, error) {
	var selected any
	count := 0
	for _, variant := range variants {
		if !reflect.ValueOf(variant).IsNil() {
			selected = variant
			count++
		}
	}
	if len(unknown) > 0 {
		count++
	}
	if count != 1 {
		return nil, fmt.Errorf("protocol: union requires exactly one variant, got %d", count)
	}
	if len(unknown) > 0 {
		if validateJSONDocument(unknown) != nil || bytes.Equal(bytes.TrimSpace(unknown), []byte("null")) {
			return nil, fmt.Errorf("protocol: unknown union variant must be valid non-null JSON")
		}
		return append([]byte(nil), unknown...), nil
	}
	if err := validateGoJSONValue(reflect.ValueOf(selected)); err != nil {
		return nil, err
	}
	return json.Marshal(selected)
}

func inspectUnion(data []byte, discriminator ...string) (kind, tag string, fields map[string]json.RawMessage, err error) {
	if err = validateJSONDocument(data); err != nil {
		return
	}
	data = bytes.TrimSpace(data)
	if !json.Valid(data) || bytes.Equal(data, []byte("null")) {
		return "", "", nil, fmt.Errorf("protocol: expected valid non-null union value")
	}
	switch data[0] {
	case '"':
		kind = "string"
	case '[':
		kind = "array"
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		// The generated integer branch decodes directly into int64/uint64;
		// floating point and overflowing values cannot change native IDs.
		kind = "number"
	case '{':
		kind = "object"
		if err = json.Unmarshal(data, &fields); err != nil {
			return
		}
		key := "type"
		if len(discriminator) > 0 {
			key = discriminator[0]
		}
		if raw, found := fields[key]; found {
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				err = fmt.Errorf("protocol: null union %s discriminator", key)
				return
			}
			err = json.Unmarshal(raw, &tag)
		}
	default:
		kind = "other"
	}
	return
}

// validateJSONDocument rejects JSON that encoding/json would silently normalize
// or collapse, including duplicate members and malformed Unicode identities.
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

// Check strings before encoding/json can replace malformed UTF-8 with U+FFFD.
// Generated structs and Optional validate their own contents when marshaled.
func validateGoJSONValue(value reflect.Value) error {
	if !value.IsValid() {
		return nil
	}
	if value.Type() == rawMessageType {
		if value.IsNil() {
			return nil
		}
		return validateJSONDocument(value.Bytes())
	}
	switch value.Kind() {
	case reflect.String:
		if !utf8.ValidString(value.String()) {
			return fmt.Errorf("invalid UTF-8 string")
		}
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			return validateGoJSONValue(value.Elem())
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if err := validateGoJSONValue(value.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			if err := validateGoJSONValue(iter.Key()); err != nil {
				return err
			}
			if err := validateGoJSONValue(iter.Value()); err != nil {
				return err
			}
		}
	}
	return nil
}
