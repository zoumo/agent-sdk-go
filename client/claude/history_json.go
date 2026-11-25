package claude

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Duplicate keys must not erase native identity/parent/content evidence. Unknown
// keys and content variants remain allowed and are returned in original Raw.
func validateTranscriptJSON(raw []byte) error {
	if !json.Valid(raw) {
		return errors.New("invalid JSON")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 1000 {
			return errors.New("JSON nesting limit")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				s, ok := key.(string)
				if !ok {
					return errors.New("invalid object key")
				}
				if seen[s] {
					return errors.New("duplicate JSON member")
				}
				seen[s] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case json.Delim('['):
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	return value(0)
}
