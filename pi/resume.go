package pi

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Pi creates a session when asked to open an absent path. Resolve an exact
// existing header first and check get_state again before handing out the Session.
func (o Options) resumeFile() (string, error) {
	if o.SessionFile != "" {
		path := o.inCwd(o.SessionFile)
		header, err := readSessionHeader(path)
		if err != nil {
			return "", err
		}
		if header.ID != o.ResumeID {
			return "", fmt.Errorf("pi: existing session ID %q differs from %q", header.ID, o.ResumeID)
		}
		if err := o.checkResumeCwd(header.Cwd); err != nil {
			return "", err
		}
		return path, nil
	}
	dir := o.SessionDir
	if dir == "" {
		dir = o.envValue("PI_CODING_AGENT_SESSION_DIR")
	}
	if dir == "" {
		agentDir := o.envValue("PI_CODING_AGENT_DIR")
		if agentDir == "" {
			home := o.envValue("HOME")
			if home == "" {
				var err error
				home, err = os.UserHomeDir()
				if err != nil {
					return "", err
				}
			}
			agentDir = filepath.Join(home, ".pi", "agent")
		}
		key := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(strings.TrimPrefix(filepath.ToSlash(o.Cwd), "/"))
		dir = filepath.Join(agentDir, "sessions", "--"+key+"--")
	}
	dir = o.inCwd(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var found string
	for _, entry := range entries {
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		header, err := readSessionHeader(path)
		if errors.Is(err, errInvalidHeader) {
			continue
		}
		if err != nil {
			return "", err
		}
		if header.ID != o.ResumeID {
			continue
		}
		if err := o.checkResumeCwd(header.Cwd); err != nil {
			return "", err
		}
		if found != "" {
			return "", fmt.Errorf("pi: ambiguous session ID %q in %q", header.ID, dir)
		}
		found = path
	}
	if found == "" {
		return "", fmt.Errorf("pi: session %q in %q: %w", o.ResumeID, dir, os.ErrNotExist)
	}
	return found, nil
}

func (o Options) inCwd(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(o.Cwd, path)
}

func (o Options) envValue(key string) string {
	for _, entry := range slices.Backward(o.Env) {
		if value, ok := strings.CutPrefix(entry, key+"="); ok {
			return value
		}
	}
	return os.Getenv(key)
}

var errInvalidHeader = errors.New("pi: invalid native session header")

type sessionHeader struct{ Type, ID, Cwd string }

func (o Options) checkResumeCwd(cwd string) error {
	if !filepath.IsAbs(cwd) {
		return fmt.Errorf("pi: session workspace %q is not absolute", cwd)
	}
	if filepath.Clean(o.Cwd) == filepath.Clean(cwd) {
		return nil
	}
	actual, err := os.Stat(cwd)
	requested, requestErr := os.Stat(o.Cwd)
	if err == nil && requestErr == nil && os.SameFile(actual, requested) {
		return nil
	}
	return fmt.Errorf("pi: session workspace %q differs from requested cwd %q", cwd, o.Cwd)
}

func readSessionHeader(path string) (sessionHeader, error) {
	var header sessionHeader
	info, err := os.Lstat(path)
	if err != nil {
		return header, err
	}
	if !info.Mode().IsRegular() {
		return header, fmt.Errorf("%w: %q is not a regular file", errInvalidHeader, path)
	}
	file, err := openHeader(path)
	if err != nil {
		return header, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return header, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return header, errInvalidHeader
	}
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 4096), 1<<20)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return header, err
		}
		return header, errInvalidHeader
	}
	if err := json.Unmarshal(sc.Bytes(), &header); err != nil || header.Type != "session" || header.ID == "" {
		return header, errInvalidHeader
	}
	return header, nil
}
