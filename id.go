package codex

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"unicode/utf8"
)

// ID preserves the pinned protocol's distinct signed-integer and string IDs.
// Its zero value is invalid. Numeric IDs never pass through floating point.
type ID struct {
	kind   uint8
	number int64
	text   string
}

func IntegerID(v int64) ID                { return ID{kind: 1, number: v} }
func StringID(v string) ID                { return ID{kind: 2, text: v} }
func (id ID) Integer() (int64, bool)      { return id.number, id.kind == 1 }
func (id ID) StringValue() (string, bool) { return id.text, id.kind == 2 }
func (id ID) Valid() bool                 { return id.kind == 1 || (id.kind == 2 && utf8.ValidString(id.text)) }
func (id ID) String() string {
	b, err := id.MarshalJSON()
	if err != nil {
		return "<invalid>"
	}
	return string(b)
}
func (id ID) MarshalJSON() ([]byte, error) {
	switch id.kind {
	case 1:
		return []byte(strconv.FormatInt(id.number, 10)), nil
	case 2:
		if !utf8.ValidString(id.text) {
			return nil, fmt.Errorf("%w: invalid Unicode request ID", ErrProtocol)
		}
		return json.Marshal(id.text)
	default:
		return nil, fmt.Errorf("%w: invalid request ID", ErrProtocol)
	}
}
func (id *ID) UnmarshalJSON(raw []byte) error {
	v, err := ParseID(raw)
	if err == nil {
		*id = v
	}
	return err
}
func ParseID(raw []byte) (ID, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || !utf8.Valid(raw) || !json.Valid(raw) {
		return ID{}, fmt.Errorf("%w: invalid request ID", ErrProtocol)
	}
	if raw[0] == '"' {
		if !validStringEscapes(raw) {
			return ID{}, fmt.Errorf("%w: invalid Unicode request ID", ErrProtocol)
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return ID{}, err
		}
		return StringID(s), nil
	}
	n, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil {
		return ID{}, fmt.Errorf("%w: request ID must be a string or signed 64-bit integer", ErrProtocol)
	}
	return IntegerID(n), nil
}

// encoding/json replaces lone UTF-16 surrogates; rejecting them avoids merging
// distinct malformed peer IDs into the same replacement-character identity.
func validStringEscapes(raw []byte) bool {
	for i := 1; i < len(raw)-1; i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		code, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if code >= 0xD800 && code <= 0xDBFF {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 6
		} else if code >= 0xDC00 && code <= 0xDFFF {
			return false
		}
	}
	return true
}
