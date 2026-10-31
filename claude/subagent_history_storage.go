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

type childDirectory struct {
	root    *os.Root
	path    string
	info    os.FileInfo
	entries map[string]os.FileInfo
}
type childSource struct {
	parent     *childDirectory
	name, path string
	file       *os.File
	info       os.FileInfo
	hash       [32]byte
}
type childCandidate struct {
	agent  string
	parent *childDirectory
	name   string
	info   os.FileInfo
}
type childQuery struct {
	ctx     context.Context
	scope   SessionQueryScope
	session string
	c       *SubagentHistoryCoverage
	root    *os.Root
	dirs    []*childDirectory
	files   []childSource
	absent  []struct {
		parent *childDirectory
		name   string
	}
}

func (q *childQuery) close() {
	for _, f := range q.files {
		_ = f.file.Close()
	}
	for i := len(q.dirs) - 1; i >= 0; i-- {
		_ = q.dirs[i].root.Close()
	}
	if q.root != nil {
		_ = q.root.Close()
	}
}
func (q *childQuery) directory(parent *os.Root, name, path string) (*childDirectory, error) {
	if len(q.dirs) >= q.c.Limits.MaxDirectories {
		return nil, queryError("resource-limit", path, "directories", nil)
	}
	r, e := queryDirectory(parent, name)
	if e != nil {
		return nil, e
	}
	info, e := r.Stat(".")
	if e != nil {
		r.Close()
		return nil, queryAccess(path, e)
	}
	d := &childDirectory{root: r, path: path, info: info}
	q.dirs = append(q.dirs, d)
	q.c.Directories = append(q.c.Directories, filepath.Join(q.scope.ConfigDir, path))
	return d, nil
}
func (q *childQuery) scan(d *childDirectory) (map[string]os.FileInfo, error) {
	f, e := d.root.Open(".")
	if e != nil {
		return nil, queryAccess(d.path, e)
	}
	defer f.Close()
	entries := map[string]os.FileInfo{}
	for {
		if e = q.ctx.Err(); e != nil {
			return nil, e
		}
		batch, readErr := f.ReadDir(1)
		for _, entry := range batch {
			if q.c.Entries >= q.c.MaxEntries {
				return nil, queryError("resource-limit", d.path, "directory entries", nil)
			}
			q.c.Entries++
			info, e := d.root.Lstat(entry.Name())
			if e != nil {
				return nil, queryError("changed", d.path, "entry disappeared", e)
			}
			entries[entry.Name()] = info
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, queryAccess(d.path, readErr)
		}
	}
	d.entries = entries
	return entries, nil
}
func (q *childQuery) optionalDir(parent *childDirectory, name string) (*childDirectory, error) {
	_, e := parent.root.Lstat(name)
	if errors.Is(e, os.ErrNotExist) {
		q.absent = append(q.absent, struct {
			parent *childDirectory
			name   string
		}{parent, name})
		return nil, nil
	}
	if e != nil {
		return nil, queryAccess(parent.path, e)
	}
	return q.directory(parent.root, name, filepath.Join(parent.path, name))
}
func (q *childQuery) inventory() ([]childCandidate, error) {
	root, projects, key, e := openQueryScope(q.scope)
	if e != nil {
		return nil, e
	}
	q.root = root
	// Reopen through the owned root to include its directory identity in stability checks.
	projects.Close()
	pd, e := q.directory(root, "projects", "projects")
	if e != nil {
		return nil, e
	}
	var project *childDirectory
	if key != "" {
		project, e = q.directory(pd.root, key, filepath.Join("projects", key))
		if e != nil {
			return nil, e
		}
	} else {
		entries, e := q.scan(pd)
		if e != nil {
			return nil, e
		}
		names := childSortedNames(entries)
		for _, name := range names {
			d, e := q.directory(pd.root, name, filepath.Join("projects", name))
			if e != nil {
				return nil, e
			}
			_, e = d.root.Lstat(q.session + ".jsonl")
			if errors.Is(e, os.ErrNotExist) {
				continue
			}
			if e != nil {
				return nil, queryAccess(d.path, e)
			}
			if project != nil {
				return nil, queryError("ambiguous", q.session, "parent in multiple projects", nil)
			}
			project = d
		}
	}
	if project == nil {
		return nil, queryError("missing", q.session, "parent transcript not found", os.ErrNotExist)
	}
	if _, e = q.scan(project); e != nil {
		return nil, e
	}
	q.c.MainPath = filepath.Join(q.scope.ConfigDir, project.path, q.session+".jsonl")
	q.c.Status = "partial"
	data, e := q.read(project, q.session+".jsonl", false)
	if e != nil {
		return nil, e
	}
	if _, e = q.parse(data, q.c.MainPath, "", false); e != nil {
		return nil, e
	}
	container, e := q.optionalDir(project, q.session)
	if e != nil {
		return nil, e
	}
	if container == nil {
		return []childCandidate{}, nil
	}
	sub, e := q.optionalDir(container, "subagents")
	if e != nil {
		return nil, e
	}
	if sub == nil {
		return []childCandidate{}, nil
	}
	q.c.Root = filepath.Join(q.scope.ConfigDir, sub.path)
	candidates := []childCandidate{}
	var walk func(*childDirectory, int) error
	walk = func(d *childDirectory, depth int) error {
		if depth > q.c.Limits.MaxDepth {
			return queryError("resource-limit", d.path, "directory depth", nil)
		}
		entries, e := q.scan(d)
		if e != nil {
			return e
		}
		for _, name := range childSortedNames(entries) {
			info := entries[name]
			switch {
			case info.IsDir():
				if depth >= q.c.Limits.MaxDepth {
					return queryError("resource-limit", d.path, "directory depth", nil)
				}
				next, e := q.directory(d.root, name, filepath.Join(d.path, name))
				if e != nil {
					return e
				}
				if e = walk(next, depth+1); e != nil {
					return e
				}
			case !info.Mode().IsRegular():
				return queryError("unsupported", filepath.Join(d.path, name), "non-regular child entry", ErrUnsupported)
			case strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".jsonl"):
				id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")
				if !safeChildID(id) {
					return queryError("corrupt", filepath.Join(d.path, name), "invalid filename agentID", nil)
				}
				bound := q.c.MaxEntries
				if q.c.AgentID == "" {
					bound = q.c.MaxResults
				}
				if len(candidates) >= bound {
					return queryError("resource-limit", d.path, "child inventory", nil)
				}
				for _, old := range candidates {
					if id == old.agent || os.SameFile(info, old.info) {
						return queryError("ambiguous", filepath.Join(d.path, name), "duplicate child identity or file alias", nil)
					}
				}
				candidates = append(candidates, childCandidate{id, d, name, info})
			case strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".meta.json"):
				id := strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".meta.json")
				if !safeChildID(id) {
					return queryError("corrupt", filepath.Join(d.path, name), "invalid sidecar agentID", nil)
				}
				if _, ok := entries["agent-"+id+".jsonl"]; !ok {
					return queryError("corrupt", filepath.Join(d.path, name), "orphan sidecar", nil)
				}
			default:
				return queryError("unsupported", filepath.Join(d.path, name), "unknown child layout", ErrUnsupported)
			}
		}
		return nil
	}
	if e = walk(sub, 0); e != nil {
		return nil, e
	}
	sort.Slice(candidates, func(i, j int) bool {
		return filepath.Join(candidates[i].parent.path, candidates[i].name) < filepath.Join(candidates[j].parent.path, candidates[j].name)
	})
	return candidates, nil
}
func childSortedNames(entries map[string]os.FileInfo) []string {
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
func (q *childQuery) read(parent *childDirectory, name string, sidecar bool) ([]byte, error) {
	relative := filepath.Join(parent.path, name)
	path := filepath.Join(q.scope.ConfigDir, relative)
	if q.c.Files >= q.c.Limits.MaxFiles {
		return nil, queryError("resource-limit", path, "files read", nil)
	}
	st, e := parent.root.Lstat(name)
	if e != nil {
		return nil, queryAccess(path, e)
	}
	if !st.Mode().IsRegular() {
		return nil, queryError("unsupported", path, "non-regular source", ErrUnsupported)
	}
	if original, ok := parent.entries[name]; ok && (!os.SameFile(original, st) || original.Size() != st.Size() || !original.ModTime().Equal(st.ModTime())) {
		return nil, queryError("changed", path, "source changed after inventory", nil)
	}
	limit := q.c.Limits.MaxBytes - q.c.Bytes
	if sidecar {
		limit = min(limit, q.c.Limits.MaxSidecarBytes, int64(q.c.Limits.MaxLineBytes))
	}
	if st.Size() > limit {
		return nil, queryError("resource-limit", path, "source bytes", nil)
	}
	f, e := openDiscoveryFile(parent.root, name)
	if e != nil {
		if errors.Is(e, ErrUnsupported) {
			return nil, queryError("unsupported", path, "local query platform", e)
		}
		return nil, queryAccess(path, e)
	}
	info, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, queryAccess(path, e)
	}
	if !os.SameFile(st, info) || !info.Mode().IsRegular() || info.Size() != st.Size() {
		f.Close()
		return nil, queryError("changed", path, "source replaced during open", nil)
	}
	data, e := readQueryRange(q.ctx, f, 0, info.Size())
	if e != nil {
		f.Close()
		return nil, e
	}
	q.c.Bytes += int64(len(data))
	q.c.Files++
	q.files = append(q.files, childSource{parent, name, relative, f, info, sha256.Sum256(data)})
	return data, nil
}
func (q *childQuery) load(candidate childCandidate, chain bool) (SubagentInfo, []SessionMessage, error) {
	path := filepath.Join(q.scope.ConfigDir, candidate.parent.path, candidate.name)
	q.c.Path = path
	info := SubagentInfo{SessionID: q.session, AgentID: candidate.agent, Path: path, Running: "unknown"}
	data, e := q.read(candidate.parent, candidate.name, false)
	if e != nil {
		return info, nil, e
	}
	records, e := q.parse(data, path, candidate.agent, chain)
	if e != nil {
		return info, nil, e
	}
	name := "agent-" + candidate.agent + ".meta.json"
	metaPath := filepath.Join(q.scope.ConfigDir, candidate.parent.path, name)
	_, e = candidate.parent.root.Lstat(name)
	if errors.Is(e, os.ErrNotExist) {
		q.absent = append(q.absent, struct {
			parent *childDirectory
			name   string
		}{candidate.parent, name})
		if q.c.AgentID != "" {
			q.c.SidecarStatus = "absent"
		}
		return info, records, nil
	}
	if e != nil {
		return info, nil, queryAccess(path, e)
	}
	data, e = q.read(candidate.parent, name, true)
	if e != nil {
		return info, nil, e
	}
	if q.c.AgentID != "" {
		q.c.SidecarStatus = "present"
	}
	if e = q.parseSidecar(data, path, metaPath, candidate.agent, &info); e != nil {
		return info, nil, e
	}
	return info, records, nil
}
func (q *childQuery) verify() error {
	for _, d := range q.dirs {
		if e := q.ctx.Err(); e != nil {
			return e
		}
		now, e := q.root.Lstat(d.path)
		if e != nil || !now.IsDir() || !os.SameFile(d.info, now) {
			return queryError("changed", d.path, "directory replaced", e)
		}
		if d.entries == nil {
			continue
		}
		f, e := d.root.Open(".")
		if e != nil {
			return queryError("changed", d.path, "directory inaccessible", e)
		}
		count := 0
		for {
			batch, re := f.ReadDir(1)
			for _, entry := range batch {
				old, ok := d.entries[entry.Name()]
				if !ok {
					f.Close()
					return queryError("changed", d.path, "entry added", nil)
				}
				now, e := d.root.Lstat(entry.Name())
				if e != nil || !os.SameFile(old, now) || old.Mode() != now.Mode() {
					f.Close()
					return queryError("changed", d.path, "entry replaced", e)
				}
				count++
			}
			if re == io.EOF {
				break
			}
			if re != nil {
				f.Close()
				return queryError("changed", d.path, "directory changed", re)
			}
			if e = q.ctx.Err(); e != nil {
				f.Close()
				return e
			}
		}
		f.Close()
		if count != len(d.entries) {
			return queryError("changed", d.path, "entry removed", nil)
		}
	}
	for _, f := range q.files {
		if e := verifyQuerySource(q.ctx, f.file, q.root, f.path, f.info, f.hash); e != nil {
			return e
		}
	}
	for _, missing := range q.absent {
		if _, e := missing.parent.root.Lstat(missing.name); !errors.Is(e, os.ErrNotExist) {
			return queryError("changed", missing.parent.path, "optional source appeared/changed", e)
		}
	}
	return q.ctx.Err()
}
