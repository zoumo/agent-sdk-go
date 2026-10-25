package recorder

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type ledgerHeader struct {
	Version       int       `json:"version"`
	Authorization string    `json:"authorization"`
	Revision      string    `json:"revision"`
	StartedAt     time.Time `json:"startedAt"`
	MaxRoots      int       `json:"maxRoots"`
	MaxTurns      int       `json:"maxTurns"`
}
type reservation struct {
	Scene     string `json:"scene"`
	Operation string `json:"operation"`
}

// Reserve durably charges an attempt before startup or submission. The caller
// supplies one existing aggregate ledger across output directories and tickets.
// Failed/uncertain starts are never refunded. This is accounting, not authority
// to launch, and does not measure provider requests, retries, tokens or dollars.
// The externally reviewed header starts a single 480-second activity window.
func Reserve(path, scene, operation string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("absolute clean aggregate ledger path required")
	}
	if scene != "s1" && scene != "s2" && scene != "r1" && scene != "r2" && scene != "r3" && scene != "r4" {
		return errors.New("unsupported scene; children disabled")
	}
	if operation != "root" && operation != "reader" && operation != "turn1" && operation != "turn2" {
		return errors.New("unsupported operation")
	}
	// Refuse symlink components, including parent directories.
	for p := path; p != "/"; p = filepath.Dir(p) {
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return errors.New("ledger symlink refused")
		}
	}
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 64<<10 {
		return errors.New("invalid private ledger")
	}
	b, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil {
		return err
	}
	if len(b) == 0 || b[len(b)-1] != '\n' {
		return errors.New("incomplete ledger")
	}
	scan := bufio.NewScanner(bytes.NewReader(b))
	if !scan.Scan() {
		return errors.New("missing header")
	}
	var header ledgerHeader
	if err = decodeLedger(scan.Bytes(), &header); err != nil {
		return err
	}
	age := time.Since(header.StartedAt)
	_, revisionErr := hex.DecodeString(header.Revision)
	if revisionErr != nil {
		return errors.New("invalid source revision")
	}
	if header.Version != 1 || header.Authorization == "" || len(header.Revision) != 40 || header.MaxRoots < 1 || header.MaxRoots > 3 || header.MaxTurns < 1 || header.MaxTurns > 6 || age < 0 || age >= 480*time.Second {
		return errors.New("invalid or expired aggregate budget")
	}
	seen := map[reservation]bool{}
	roots, turns, readers := 0, 0, 0
	charge := func(r reservation) error {
		if seen[r] {
			return errors.New("duplicate attempt")
		}
		if r.Scene != "s1" && r.Scene != "s2" && r.Scene != "r1" && r.Scene != "r2" && r.Scene != "r3" && r.Scene != "r4" {
			return errors.New("invalid ledger scene")
		}
		switch r.Operation {
		case "root":
			if r.Scene == "r4" {
				return errors.New("query cannot create root")
			}
			roots++
		case "reader":
			if r.Scene != "r4" {
				return errors.New("reader only r4")
			}
			readers++
		case "turn1", "turn2":
			if !seen[reservation{r.Scene, "root"}] || r.Scene == "r4" {
				return errors.New("turn without root")
			}
			if r.Operation == "turn2" && (!seen[reservation{r.Scene, "turn1"}] || r.Scene == "s1" || r.Scene == "s2") {
				return errors.New("extra or unordered turn")
			}
			turns++
		default:
			return errors.New("invalid ledger operation")
		}
		if roots > header.MaxRoots || turns > header.MaxTurns || readers > 1 {
			return errors.New("aggregate attempts exhausted")
		}
		seen[r] = true
		return nil
	}
	for scan.Scan() {
		var r reservation
		if err = decodeLedger(scan.Bytes(), &r); err != nil {
			return err
		}
		if err = charge(r); err != nil {
			return err
		}
	}
	if err = scan.Err(); err != nil {
		return err
	}
	r := reservation{scene, operation}
	if err = charge(r); err != nil {
		return err
	}
	line, _ := json.Marshal(r)
	if _, err = f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	if _, err = f.Write(append(line, '\n')); err != nil {
		return err
	}
	return f.Sync()
}
func decodeLedger(b []byte, v any) error {
	// Header/reservation fields are flat. Reject repeated keys instead of taking
	// encoding/json's last value for an invalid or ambiguous durable budget.
	keys := json.NewDecoder(bytes.NewReader(b))
	token, err := keys.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("ledger/profile must be an object")
	}
	seen := map[string]bool{}
	for keys.More() {
		token, err = keys.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return errors.New("duplicate JSON field")
		}
		seen[key] = true
		var value any
		if err = keys.Decode(&value); err != nil {
			return err
		}
	}
	if _, err = keys.Token(); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing ledger JSON")
	}
	return nil
}
