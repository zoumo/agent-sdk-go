package claude

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type childDirectory struct {
	root    *os.Root
	path    string
	info    os.FileInfo
	entries map[string]os.FileInfo
}
type childCandidate struct {
	agent  string
	parent *childDirectory
	name   string
	info   os.FileInfo
}

// childFileIdentity mirrors os.SameFile equality while normalizing the
// platform-specific Dev and Ino integer types.
type childFileIdentity struct {
	device int64
	inode  int64
}
type childQuery struct {
	ctx     context.Context
	scope   SessionQueryScope
	session string
	c       *SubagentHistoryCoverage
	root    *os.Root
	dirs    []*childDirectory
	files   []*querySource
	absent  []struct {
		parent *childDirectory
		name   string
	}
}

func (q *childQuery) close() {
	for _, f := range q.files {
		_ = f.close()
	}
	for _, v := range slices.Backward(q.dirs) {
		_ = v.root.Close()
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
		if e := q.ctx.Err(); e != nil {
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
		entries, err := q.scan(pd)
		if err != nil {
			return nil, err
		}
		names := childSortedNames(entries)
		for _, name := range names {
			d, err := q.directory(pd.root, name, filepath.Join("projects", name))
			if err != nil {
				return nil, err
			}
			_, err = d.root.Lstat(q.session + ".jsonl")
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, queryAccess(d.path, err)
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
	agentIDs := map[string]struct{}{}
	fileIDs := map[childFileIdentity]struct{}{}
	var walk func(*childDirectory, int) error
	walk = func(d *childDirectory, depth int) error {
		if depth > q.c.Limits.MaxDepth {
			return queryError("resource-limit", d.path, "directory depth", nil)
		}
		entries, err := q.scan(d)
		if err != nil {
			return err
		}
		for _, name := range childSortedNames(entries) {
			info := entries[name]
			switch {
			case info.IsDir():
				if depth >= q.c.Limits.MaxDepth {
					return queryError("resource-limit", d.path, "directory depth", nil)
				}
				next, err := q.directory(d.root, name, filepath.Join(d.path, name))
				if err != nil {
					return err
				}
				if err := walk(next, depth+1); err != nil {
					return err
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
				path := filepath.Join(d.path, name)
				if _, ok := agentIDs[id]; ok {
					return queryError("ambiguous", path, "duplicate child identity or file alias", nil)
				}
				identity, ok := childFileIdentityOf(info)
				if !ok {
					return queryError("unsupported", path, "child file identity unavailable", ErrUnsupported)
				}
				if _, ok := fileIDs[identity]; ok {
					return queryError("ambiguous", path, "duplicate child identity or file alias", nil)
				}
				candidates = append(candidates, childCandidate{id, d, name, info})
				agentIDs[id] = struct{}{}
				fileIDs[identity] = struct{}{}
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
	if e := walk(sub, 0); e != nil {
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
	limit := q.c.Limits.MaxBytes - q.c.Bytes
	if sidecar {
		limit = min(limit, q.c.Limits.MaxSidecarBytes, int64(q.c.Limits.MaxLineBytes))
	}
	source, e := openQuerySource(q.ctx, q.root, parent.root, relative, parent.entries[name])
	if e != nil {
		return nil, e
	}
	data, e := source.readAll(limit)
	if e != nil {
		source.close()
		return nil, e
	}
	q.c.Bytes += int64(len(data))
	q.c.Files++
	q.files = append(q.files, source)
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
	if e := q.parseSidecar(data, path, metaPath, candidate.agent, &info); e != nil {
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
				now, err := d.root.Lstat(entry.Name())
				if err != nil || !os.SameFile(old, now) || old.Mode() != now.Mode() {
					f.Close()
					return queryError("changed", d.path, "entry replaced", err)
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
		if e := f.verify(); e != nil {
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
