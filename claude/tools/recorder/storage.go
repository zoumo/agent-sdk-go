// SPDX-License-Identifier: Apache-2.0
package recorder

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var ErrStorageQuarantine = errors.New("recorder: unreviewed saved-storage structure quarantined in private workspace")

// StorageLimits bounds the entire selected root/child saved-content snapshot.
// All values are explicit policy ceilings; none grants recording authority.
type StorageLimits struct {
	Bytes                 int64
	Files, Entries, Depth int
}

// StorageFile preserves one separate native file, including unreviewed fields.
// Path is relative to the selected project store; it does not assert parentage.
type StorageFile struct {
	Path, SHA256 string
	Raw          []byte
}

type StorageCapture struct {
	Files               []StorageFile
	Bytes               int64
	Atomic, Publishable bool // Always false; private saved content is not a wire log.
}

var storageSessionID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var storageProjectKey = regexp.MustCompile(`[^a-zA-Z0-9]`)

// CaptureStorage reads only the selected project in this newly owned ConfigDir.
// It never uses HOME, discovers other projects, opens a Session or grants live
// membership/answer authority. Missing/unknown structures and errors return no
// partial snapshot. Raw bytes remain private and require independent versioned
// publication review; even known file names do not imply reviewed JSON fields.
// The caller must stop owned writers first. Files are checked while read, but
// there is no atomic multi-file snapshot or guarantee that service work stopped.
func (w *Workspace) CaptureStorage(ctx context.Context, sessionID string, limits StorageLimits) (capture StorageCapture, err error) {
	defer func() {
		if err != nil {
			capture = StorageCapture{}
		}
	}()
	if ctx == nil || !storageSessionID.MatchString(sessionID) || limits.Bytes < 1 || limits.Bytes > 64<<20 || limits.Files < 1 || limits.Files > 10000 || limits.Entries < 1 || limits.Entries > 10000 || limits.Depth < 0 || limits.Depth > 64 {
		return capture, fmt.Errorf("explicit bounded storage policy, context and parent UUID required")
	}
	if err = ctx.Err(); err != nil {
		return capture, err
	}
	if err = w.validate(); err != nil {
		return capture, err
	}
	config, err := os.OpenRoot(w.Config)
	if err != nil {
		return capture, err
	}
	defer config.Close()
	opened, err := config.Lstat(".")
	if err != nil || w.configIdentity == nil || !os.SameFile(w.configIdentity, opened) {
		return capture, fmt.Errorf("new owned configuration identity required")
	}
	key := storageProjectKey.ReplaceAllString(w.Project, "-")
	if len(key) > 200 {
		return capture, fmt.Errorf("unsupported project key")
	}
	projects, err := storageDirectory(config, "projects")
	if err != nil {
		return capture, err
	}
	defer projects.Close()
	project, err := storageDirectory(projects, key)
	if err != nil {
		return capture, err
	}
	defer project.Close()
	read := func(dir *os.Root, name, path string) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if len(capture.Files) >= limits.Files {
			return fmt.Errorf("storage file limit")
		}
		info, e := dir.Lstat(name)
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			return ErrStorageQuarantine
		}
		if info.Size() > limits.Bytes-capture.Bytes {
			return fmt.Errorf("storage byte limit")
		}
		file, e := openStorageFile(dir, name)
		if e != nil {
			return e
		}
		defer file.Close()
		before, e := file.Stat()
		if e != nil || !before.Mode().IsRegular() || !os.SameFile(info, before) {
			return fmt.Errorf("storage changed during open")
		}
		// SectionReader never follows appends or blocks reading a replaced FIFO.
		raw, e := io.ReadAll(io.NewSectionReader(file, 0, info.Size()))
		if e != nil {
			return e
		}
		after, e := file.Stat()
		current, currentErr := dir.Lstat(name)
		if e != nil || currentErr != nil || int64(len(raw)) != info.Size() || !os.SameFile(info, current) || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
			return fmt.Errorf("storage changed during read")
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		capture.Files = append(capture.Files, StorageFile{Path: path, SHA256: fmt.Sprintf("%x", sha256.Sum256(raw)), Raw: raw})
		capture.Bytes += int64(len(raw))
		return nil
	}
	if err = read(project, sessionID+".jsonl", sessionID+".jsonl"); err != nil {
		return capture, err
	}
	container, err := storageDirectory(project, sessionID)
	if os.IsNotExist(err) {
		return capture, nil
	}
	if err != nil {
		return capture, err
	}
	defer container.Close()
	children, err := storageDirectory(container, "subagents")
	if os.IsNotExist(err) {
		return capture, nil
	}
	if err != nil {
		return capture, err
	}
	defer children.Close()
	entries := 0
	var walk func(*os.Root, string, int) error
	walk = func(dir *os.Root, prefix string, depth int) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if depth > limits.Depth {
			return fmt.Errorf("storage depth limit")
		}
		file, e := dir.Open(".")
		if e != nil {
			return e
		}
		defer file.Close()
		for {
			batch, readErr := file.ReadDir(1)
			if readErr == io.EOF {
				return nil
			}
			if readErr != nil {
				return readErr
			}
			entries++
			if entries > limits.Entries {
				return fmt.Errorf("storage entry limit")
			}
			name := batch[0].Name()
			info, e := dir.Lstat(name)
			if e != nil {
				return e
			}
			path := filepath.Join(prefix, name)
			if info.IsDir() {
				next, e := storageDirectory(dir, name)
				if e != nil {
					return e
				}
				e = walk(next, path, depth+1)
				next.Close()
				if e != nil {
					return e
				}
			} else {
				if !strings.HasPrefix(name, "agent-") || !(strings.HasSuffix(name, ".jsonl") || strings.HasSuffix(name, ".meta.json")) {
					return ErrStorageQuarantine
				}
				if e = read(dir, name, path); e != nil {
					return e
				}
			}
		}
	}
	err = walk(children, filepath.Join(sessionID, "subagents"), 0)
	return capture, err
}

func storageDirectory(parent *os.Root, name string) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, ErrStorageQuarantine
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := child.Lstat(".")
	if err != nil || !os.SameFile(info, opened) {
		child.Close()
		return nil, fmt.Errorf("storage directory changed during open")
	}
	return child, nil
}
