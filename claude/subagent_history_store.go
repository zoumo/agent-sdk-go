package claude

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type childCandidate struct {
	agent, path string
	info        os.FileInfo
}
type childDirectory struct {
	path    string
	info    os.FileInfo
	entries map[string]os.FileInfo
}
type childFile struct {
	path   string
	info   os.FileInfo
	digest [32]byte
}
type childReader struct {
	ctx         context.Context
	root        *os.Root
	c           *SubagentHistoryCoverage
	directories []childDirectory
	files       []childFile
	absent      []string
	project     string
}

func newChildReader(ctx context.Context, scope SessionQueryScope, c *SubagentHistoryCoverage) (*childReader, error) {
	root, projects, key, e := openQueryScope(scope)
	if e != nil {
		return nil, e
	}
	projects.Close()
	r := &childReader{ctx: ctx, root: root, c: c, project: key}
	return r, nil
}
func (r *childReader) close()                      { r.root.Close() }
func (r *childReader) absolute(path string) string { return filepath.Join(r.c.ConfigDir, path) }
func (r *childReader) fail(kind, path, reason string) error {
	var cause error
	switch kind {
	case "unsupported":
		cause = ErrUnsupported
	case "missing":
		cause = os.ErrNotExist
	}
	return queryError(kind, r.absolute(path), reason, cause)
}

// Each path component is pinned and checked, in addition to os.Root's escape
// containment. A symlink is not accepted even when its destination is in-root.
func (r *childReader) directory(path string) (*os.Root, error) {
	current, e := r.root.OpenRoot(".")
	if e != nil {
		return nil, queryAccess(r.absolute(path), e)
	}
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		next, e := queryDirectory(current, part)
		current.Close()
		if e != nil {
			return nil, e
		}
		current = next
	}
	return current, nil
}
func (r *childReader) stat(path string) (os.FileInfo, error) {
	d, e := r.directory(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer d.Close()
	st, e := d.Lstat(filepath.Base(path))
	if e != nil {
		return nil, queryAccess(r.absolute(path), e)
	}
	return st, nil
}
func (r *childReader) snapshot(path string) (childDirectory, error) {
	snapshot := childDirectory{path: path, entries: map[string]os.FileInfo{}}
	if e := r.ctx.Err(); e != nil {
		return snapshot, e
	}
	if len(r.directories) >= r.c.Limits.MaxDirectories {
		return snapshot, r.fail("resource-limit", path, "directories")
	}
	d, e := r.directory(path)
	if e != nil {
		return snapshot, e
	}
	defer d.Close()
	snapshot.info, e = d.Stat(".")
	if e != nil {
		return snapshot, queryAccess(r.absolute(path), e)
	}
	f, e := d.Open(".")
	if e != nil {
		return snapshot, queryAccess(r.absolute(path), e)
	}
	defer f.Close()
	// Keep partial coverage on all exits, including a bound mid-directory.
	r.c.Directories = append(r.c.Directories, r.absolute(path))
	for {
		if e = r.ctx.Err(); e != nil {
			return snapshot, e
		}
		entries, re := f.ReadDir(1)
		if len(entries) == 0 && re == io.EOF {
			break
		}
		if re != nil && re != io.EOF {
			return snapshot, queryAccess(r.absolute(path), re)
		}
		for _, entry := range entries {
			if r.c.Entries >= r.c.MaxEntries {
				return snapshot, r.fail("resource-limit", path, "directory entries")
			}
			r.c.Entries++
			st, e := d.Lstat(entry.Name())
			if e != nil {
				return snapshot, queryAccess(r.absolute(path), e)
			}
			snapshot.entries[entry.Name()] = st
		}
	}
	r.directories = append(r.directories, snapshot)
	return snapshot, nil
}
func (r *childReader) optionalDirectory(path string) (bool, error) {
	st, e := r.stat(path)
	if errors.Is(e, os.ErrNotExist) {
		r.absent = append(r.absent, path)
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if !st.IsDir() {
		return false, r.fail("unsupported", path, "not an ordinary directory")
	}
	return true, nil
}
func (r *childReader) inventory(parent string) ([]childCandidate, error) {
	projects, e := r.snapshot("projects")
	if e != nil {
		return nil, e
	}
	keys := []string{}
	if r.project != "" {
		keys = append(keys, r.project)
	} else {
		for key, st := range projects.entries {
			if !st.IsDir() {
				return nil, r.fail("unsupported", filepath.Join("projects", key), "non-directory project")
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
	}
	selected := ""
	for _, key := range keys {
		path := filepath.Join("projects", key)
		project, e := r.snapshot(path)
		if e != nil {
			return nil, e
		}
		if _, exists := project.entries[parent+".jsonl"]; !exists {
			continue
		}
		if selected != "" {
			return nil, r.fail("ambiguous", path, "parent found in multiple projects")
		}
		selected = path
	}
	if selected == "" {
		return nil, r.fail("missing", "projects", "parent source not found")
	}
	main := filepath.Join(selected, parent+".jsonl")
	r.c.MainPath = r.absolute(main)
	r.c.Status = "partial"
	data, e := r.read(main, false)
	if e != nil {
		return nil, e
	}
	if _, e = r.parse(data, main, ""); e != nil {
		return nil, e
	}
	container := filepath.Join(selected, parent)
	root := filepath.Join(container, "subagents")
	r.c.Root = r.absolute(root)
	present, e := r.optionalDirectory(container)
	if e != nil {
		return nil, e
	}
	if !present {
		return []childCandidate{}, nil
	}
	// Snapshot the container too: addition/removal/replacement of subagents is
	// observable even when it was absent at initial inspection.
	if _, e = r.snapshot(container); e != nil {
		return nil, e
	}
	present, e = r.optionalDirectory(root)
	if e != nil {
		return nil, e
	}
	if !present {
		return []childCandidate{}, nil
	}
	candidates := []childCandidate{}
	ids := map[string]bool{}
	var walk func(string, int) error
	walk = func(path string, depth int) error {
		if depth > r.c.Limits.MaxDepth {
			return r.fail("resource-limit", path, "child directory depth")
		}
		directory, e := r.snapshot(path)
		if e != nil {
			return e
		}
		names := make([]string, 0, len(directory.entries))
		for name := range directory.entries {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if e := r.ctx.Err(); e != nil {
				return e
			}
			st := directory.entries[name]
			child := filepath.Join(path, name)
			if st.IsDir() {
				if e := walk(child, depth+1); e != nil {
					return e
				}
				continue
			}
			if !st.Mode().IsRegular() {
				return r.fail("unsupported", child, "non-regular inventory entry")
			}
			if strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".jsonl") {
				id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
				if !validChildAgent(id) {
					return r.fail("unsupported", child, "invalid filename agent component")
				}
				if ids[id] {
					return r.fail("ambiguous", child, "duplicate agent ID")
				}
				for _, old := range candidates {
					if os.SameFile(st, old.info) {
						return r.fail("ambiguous", child, "aliased child file")
					}
				}
				ids[id] = true
				// The inventory itself is bounded even when get reads only one child.
				if len(candidates) >= r.c.MaxEntries || (r.c.SidecarStatus == "per-record" && len(candidates) >= r.c.MaxResults) {
					return r.fail("resource-limit", child, "agent inventory")
				}
				candidates = append(candidates, childCandidate{agent: id, path: child, info: st})
			} else if strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".meta.json") {
				id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".meta.json")
				if !validChildAgent(id) {
					return r.fail("unsupported", child, "invalid sidecar agent component")
				}
				if _, ok := directory.entries["agent-"+id+".jsonl"]; !ok {
					return r.fail("corrupt", child, "orphan sidecar")
				}
			} else {
				return r.fail("unsupported", child, "unrecognized inventory entry")
			}
		}
		return nil
	}
	if e = walk(root, 0); e != nil {
		return nil, e
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].path < candidates[j].path })
	return candidates, nil
}
func (r *childReader) read(path string, sidecar bool) ([]byte, error) {
	if e := r.ctx.Err(); e != nil {
		return nil, e
	}
	if r.c.Files >= r.c.Limits.MaxFiles {
		return nil, r.fail("resource-limit", path, "read files")
	}
	d, e := r.directory(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer d.Close()
	initial, e := d.Lstat(filepath.Base(path))
	if e != nil {
		return nil, queryAccess(r.absolute(path), e)
	}
	if !initial.Mode().IsRegular() {
		return nil, r.fail("unsupported", path, "not a regular file")
	}
	f, e := openDiscoveryFile(d, filepath.Base(path))
	if e != nil {
		if errors.Is(e, ErrUnsupported) {
			return nil, r.fail("unsupported", path, "platform file opening")
		}
		return nil, queryAccess(r.absolute(path), e)
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil {
		return nil, queryAccess(r.absolute(path), e)
	}
	if !os.SameFile(initial, opened) || !opened.Mode().IsRegular() {
		return nil, r.fail("changed", path, "file changed during open")
	}
	r.c.Files++
	if opened.Size() > r.c.Limits.MaxBytes-r.c.Bytes || (sidecar && opened.Size() > r.c.Limits.MaxSidecarBytes) {
		return nil, r.fail("resource-limit", path, "read bytes")
	}
	data, e := readQueryRange(r.ctx, f, 0, opened.Size())
	if e != nil {
		return nil, e
	}
	r.c.Bytes += int64(len(data))
	r.files = append(r.files, childFile{path: path, info: opened, digest: sha256.Sum256(data)})
	return data, nil
}
func sameChildEntry(a, b os.FileInfo) bool { return os.SameFile(a, b) && a.Mode() == b.Mode() }
func (r *childReader) verify() error {
	for _, before := range r.directories {
		if e := r.ctx.Err(); e != nil {
			return e
		}
		d, e := r.directory(before.path)
		if e != nil {
			return queryError("changed", r.absolute(before.path), "directory removed/replaced", e)
		}
		e = r.verifyDirectory(d, before)
		d.Close()
		if e != nil {
			return e
		}
	}
	for _, path := range r.absent {
		if _, e := r.stat(path); !errors.Is(e, os.ErrNotExist) {
			return queryError("changed", r.absolute(path), "optional source appeared/changed", e)
		}
	}
	for _, before := range r.files {
		d, e := r.directory(filepath.Dir(before.path))
		if e != nil {
			return queryError("changed", r.absolute(before.path), "source ancestor changed", e)
		}
		e = r.verifyFile(d, before)
		d.Close()
		if e != nil {
			return e
		}
	}
	return r.ctx.Err()
}
func (r *childReader) verifyDirectory(d *os.Root, before childDirectory) error {
	changed := func(e error) error {
		return queryError("changed", r.absolute(before.path), "directory inventory changed", e)
	}
	st, e := d.Stat(".")
	if e != nil || !sameChildEntry(before.info, st) {
		return changed(e)
	}
	f, e := d.Open(".")
	if e != nil {
		return changed(e)
	}
	defer f.Close()
	seen := 0
	for {
		if e := r.ctx.Err(); e != nil {
			return e
		}
		entries, re := f.ReadDir(1)
		if len(entries) == 0 && re == io.EOF {
			break
		}
		if re != nil && re != io.EOF {
			return changed(re)
		}
		for _, entry := range entries {
			old, ok := before.entries[entry.Name()]
			if !ok {
				return changed(nil)
			}
			now, e := d.Lstat(entry.Name())
			if e != nil || !sameChildEntry(old, now) {
				return changed(e)
			}
			seen++
		}
	}
	if seen != len(before.entries) {
		return changed(nil)
	}
	return nil
}
func (r *childReader) verifyFile(d *os.Root, before childFile) error {
	changed := func(e error) error {
		return queryError("changed", r.absolute(before.path), "source append/rewrite/replacement observed", e)
	}
	name := filepath.Base(before.path)
	current, e := d.Lstat(name)
	if e != nil || !sameChildEntry(before.info, current) || current.Size() != before.info.Size() || !current.ModTime().Equal(before.info.ModTime()) {
		return changed(e)
	}
	f, e := openDiscoveryFile(d, name)
	if e != nil {
		return changed(e)
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !sameChildEntry(before.info, opened) {
		return changed(e)
	}
	// At most one extra read of the original byte count, even if it grew.
	data, e := readQueryRange(r.ctx, f, 0, before.info.Size())
	if e != nil {
		if r.ctx.Err() != nil {
			return r.ctx.Err()
		}
		return changed(e)
	}
	final, e := f.Stat()
	if e != nil || final.Size() != before.info.Size() || !final.ModTime().Equal(before.info.ModTime()) || sha256.Sum256(data) != before.digest {
		return changed(e)
	}
	current, e = d.Lstat(name)
	if e != nil || !sameChildEntry(before.info, current) {
		return changed(e)
	}
	return nil
}
