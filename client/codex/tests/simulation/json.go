package codexsim

import (
	"bytes"
	"encoding/json"
	"reflect"
)

func equal(a, b any) bool { return reflect.DeepEqual(a, b) }
func canonical(b []byte) any {
	var v any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	_ = d.Decode(&v)
	return v
}
