package mcp

import (
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

const maxValueFileSize = 64 * 1024

// ResolveServers validates the declarations and returns operation-local copies
// with file references replaced by literal values. It reads each referenced
// file once per invocation. Re-run it when opening or resuming a Session to use
// current file contents.
//
// A nil filesystem reads absolute paths on the local host. Accept absolute
// paths only from callers trusted to read files as the SDK process. Supply a
// rooted filesystem such as os.Root.FS to restrict references to relative
// paths within that filesystem. Native MCP configuration carries the resolved
// values, so protect the native transport and diagnostics as needed.
func ResolveServers(servers []Server, files fs.FS) ([]Server, error) {
	if err := ValidateServers(servers); err != nil {
		return nil, err
	}
	resolved := CloneServers(servers)
	cache := make(map[string]map[string]string)
	for i, server := range resolved {
		var err error
		if server.Transport == TransportStdio {
			resolved[i].Env, err = resolveValues(i, "env", server.Env, server.EnvFrom, files, cache)
			resolved[i].EnvFrom = nil
		} else {
			resolved[i].Headers, err = resolveValues(i, "headers", server.Headers, server.HeadersFrom, files, cache)
			resolved[i].HeadersFrom = nil
		}
		if err != nil {
			return nil, err
		}
	}
	return resolved, nil
}

func resolveValues(index int, field string, values []NamedValue, imports []ImportSource, files fs.FS, cache map[string]map[string]string) ([]NamedValue, error) {
	load := func(path string) (map[string]string, error) {
		if data, ok := cache[path]; ok {
			return data, nil
		}
		source, name := files, path
		if source == nil {
			if !filepath.IsAbs(path) {
				return nil, invalidArgument(index, field, "file_system_required", "relative paths require an MCP file system")
			}
			source, name = os.DirFS(filepath.Dir(path)), filepath.Base(path)
		} else if filepath.IsAbs(path) {
			return nil, invalidArgument(index, field, "file_ref", "absolute paths require local file access")
		}
		data, err := readValues(source, name)
		if err != nil {
			return nil, invalidArgument(index, field, "file_invalid", "references an unreadable or invalid file")
		}
		cache[path] = data
		return data, nil
	}

	var result []NamedValue
	positions := make(map[string]int, len(values))
	put := func(entry NamedValue) {
		key := valueKey(field, entry.Name)
		if pos, ok := positions[key]; ok {
			result[pos] = entry
			return
		}
		positions[key] = len(result)
		result = append(result, entry)
	}
	for _, source := range imports {
		data, err := load(source.FileRef.Path)
		if err != nil {
			return nil, err
		}
		seen := make(map[string]struct{}, len(data))
		for _, name := range slices.Sorted(maps.Keys(data)) {
			key := valueKey(field, name)
			if _, ok := seen[key]; ok {
				return nil, invalidArgument(index, field+"From", "duplicate", "contains a duplicate name in one file")
			}
			seen[key] = struct{}{}
			if err := validateValueName(index, field, name); err != nil {
				return nil, err
			}
			if err := validateValue(index, field, data[name]); err != nil {
				return nil, err
			}
			put(Literal(name, data[name]))
		}
	}
	for _, entry := range values {
		if entry.Value != nil {
			put(entry)
			continue
		}
		ref := entry.ValueFrom.FileKeyRef
		data, err := load(ref.Path)
		if err != nil {
			return nil, err
		}
		value, ok := data[ref.Key]
		if !ok {
			return nil, invalidArgument(index, field, "file_key_missing", "references a missing file key")
		}
		if err := validateValue(index, field, value); err != nil {
			return nil, err
		}
		put(Literal(entry.Name, value))
	}
	return result, nil
}

var errValueFile = errors.New("invalid MCP value file")

func readValues(files fs.FS, path string) (map[string]string, error) {
	// Stat before open also avoids opening non-regular paths through filesystems
	// that implement fs.StatFS (opening a named pipe can block).
	info, err := fs.Stat(files, path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxValueFileSize {
		return nil, errValueFile
	}
	file, err := files.Open(path)
	if err != nil {
		return nil, errValueFile
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxValueFileSize {
		return nil, errValueFile
	}
	data, err := io.ReadAll(io.LimitReader(file, maxValueFileSize+1))
	if err != nil || len(data) > maxValueFileSize {
		return nil, errValueFile
	}
	return parseValueFile(string(data))
}

// Files consist of NAME='literal value' assignments, blank lines, and comment
// lines. A quoted value may span lines. Quote escaping and expansion are absent.
func parseValueFile(content string) (map[string]string, error) {
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return nil, errValueFile
	}
	entries := make(map[string]string)
	for content != "" {
		content = strings.TrimLeft(content, " \t\r\n")
		if content == "" {
			break
		}
		if content[0] == '#' {
			_, content, _ = strings.Cut(content, "\n")
			continue
		}
		key, remainder, ok := strings.Cut(content, "=")
		if !ok || !validFileKey(key) || !strings.HasPrefix(remainder, "'") {
			return nil, errValueFile
		}
		value, remainder, ok := strings.Cut(remainder[1:], "'")
		if !ok {
			return nil, errValueFile
		}
		tail, next, _ := strings.Cut(remainder, "\n")
		if strings.Trim(tail, " \t\r") != "" {
			return nil, errValueFile
		}
		if _, ok := entries[key]; ok {
			return nil, errValueFile
		}
		entries[key] = value
		content = next
	}
	return entries, nil
}
