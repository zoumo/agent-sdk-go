package mcp

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

// NamedValue assigns an environment variable or an HTTP header. Exactly one of
// Value and ValueFrom is required. A pointer preserves an explicit empty value.
type NamedValue struct {
	Name      string       `json:"name" yaml:"name"`
	Value     *string      `json:"value,omitempty" yaml:"value,omitempty"`
	ValueFrom *ValueSource `json:"valueFrom,omitempty" yaml:"valueFrom,omitempty"`
}

// Literal creates a NamedValue with an explicit value.
func Literal(name, value string) NamedValue {
	return NamedValue{Name: name, Value: &value}
}

// ValueSource selects one value from a file.
type ValueSource struct {
	FileKeyRef *FileKeyRef `json:"fileKeyRef,omitempty" yaml:"fileKeyRef,omitempty"`
}

// FileKeyRef reads a case-sensitive key from a file.
type FileKeyRef struct {
	Path string `json:"path" yaml:"path"`
	Key  string `json:"key" yaml:"key"`
}

// ImportSource imports all entries from one file, using the file's keys as names.
type ImportSource struct {
	FileRef *FileRef `json:"fileRef,omitempty" yaml:"fileRef,omitempty"`
}

// FileRef names a local absolute path or a path relative to a supplied filesystem.
type FileRef struct {
	Path string `json:"path" yaml:"path"`
}

// LiteralValues converts resolved entries into the flat representation that
// native providers accept. It rejects unresolved entries.
func LiteralValues(values []NamedValue) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	result := make(map[string]string, len(values))
	for _, entry := range values {
		if entry.Value == nil || entry.ValueFrom != nil {
			return nil, &ValidationError{Reason: "mcp_value_unresolved", Message: "MCP value must be resolved before native conversion"}
		}
		result[entry.Name] = *entry.Value
	}
	return result, nil
}

func cloneValues(values []NamedValue) []NamedValue {
	cloned := slices.Clone(values)
	for i, entry := range values {
		if entry.Value != nil {
			value := *entry.Value
			cloned[i].Value = &value
		}
		if entry.ValueFrom != nil {
			source := *entry.ValueFrom
			if source.FileKeyRef != nil {
				ref := *source.FileKeyRef
				source.FileKeyRef = &ref
			}
			cloned[i].ValueFrom = &source
		}
	}
	return cloned
}

func cloneImports(imports []ImportSource) []ImportSource {
	cloned := slices.Clone(imports)
	for i, source := range imports {
		if source.FileRef != nil {
			ref := *source.FileRef
			cloned[i].FileRef = &ref
		}
	}
	return cloned
}

func validateValues(index int, field string, values []NamedValue, imports []ImportSource) error {
	seen := make(map[string]struct{}, len(values))
	for _, entry := range values {
		if err := validateValueName(index, field, entry.Name); err != nil {
			return err
		}
		key := valueKey(field, entry.Name)
		if _, ok := seen[key]; ok {
			return invalidArgument(index, field, "duplicate", "contains a duplicate name")
		}
		seen[key] = struct{}{}
		if (entry.Value == nil) == (entry.ValueFrom == nil) {
			return invalidArgument(index, field, "value_choice", "requires exactly one of value and valueFrom")
		}
		if entry.Value != nil {
			if err := validateValue(index, field, *entry.Value); err != nil {
				return err
			}
			continue
		}
		ref := entry.ValueFrom.FileKeyRef
		if ref == nil || !validFilePath(ref.Path) || !validFileKey(ref.Key) {
			return invalidArgument(index, field, "file_key_ref", "requires a fileKeyRef with a valid file path and key")
		}
	}
	for _, source := range imports {
		if source.FileRef == nil || !validFilePath(source.FileRef.Path) {
			return invalidArgument(index, field+"From", "file_ref", "requires a fileRef with a valid file path")
		}
	}
	return nil
}

func validFilePath(path string) bool {
	if filepath.IsAbs(path) {
		return filepath.Clean(path) == path && filepath.Dir(path) != path && !strings.ContainsRune(path, 0)
	}
	return fs.ValidPath(path) && path != "." && !strings.ContainsAny(path, "\\:\x00")
}

func validFileKey(key string) bool {
	if key == "" {
		return false
	}
	for _, c := range key {
		if !isTokenChar(c) {
			return false
		}
	}
	return true
}

func isTokenChar(c rune) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
		strings.ContainsRune("!#$%&'*+-.^_`|~", c)
}

func valueKey(field, name string) string {
	if field == "headers" {
		return strings.ToLower(name)
	}
	return name
}

func validateValueName(index int, field, name string) error {
	if err := validateString(index, field+" key", name); err != nil {
		return err
	}
	if field == "headers" && !validFileKey(name) || field == "env" && (name == "" || strings.ContainsAny(name, "=\x00")) {
		return invalidArgument(index, field+" key", "invalid", "contains an invalid name")
	}
	return nil
}

func validateValue(index int, field, value string) error {
	if err := validateString(index, field+" value", value); err != nil {
		return err
	}
	for _, c := range value {
		if c == 0 || field == "headers" && (c < ' ' && c != '\t' || c == 0x7f) {
			return invalidArgument(index, field+" value", "invalid", "contains an invalid character")
		}
	}
	return nil
}
