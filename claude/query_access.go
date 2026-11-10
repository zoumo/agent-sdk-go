package claude

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var (
	errQueryOrdinaryDirectoryRequired = errors.New("ordinary directory required")
	errQueryRegularFileRequired       = errors.New("regular file required")
	errQueryInventoryIdentityChanged  = errors.New("source identity changed after inventory")
	errQueryOpenedSourceChanged       = errors.New("source changed during open")
)

// queryDirectoryChangedError records a failed identity check after opening a
// directory. Callers keep their existing public error classification.
type queryDirectoryChangedError struct {
	cause error
}

func (e *queryDirectoryChangedError) Error() string { return "directory changed during open" }
func (e *queryDirectoryChangedError) Unwrap() error { return e.cause }

// pinQueryDirectory validates and pins every ordinary directory component.
// The caller owns the returned root. check runs before each component access so
// discovery callers can retain their context and Session-close cancellation.
func pinQueryDirectory(root *os.Root, path string, check func() error) (*os.Root, error) {
	current := root
	owned := false
	for part := range strings.SplitSeq(filepath.Clean(path), string(filepath.Separator)) {
		if part == "." {
			continue
		}
		if check != nil {
			if err := check(); err != nil {
				if owned {
					_ = current.Close()
				}
				return nil, err
			}
		}
		st, err := current.Lstat(part)
		if err != nil {
			if owned {
				_ = current.Close()
			}
			return nil, err
		}
		if !st.IsDir() {
			if owned {
				_ = current.Close()
			}
			return nil, errQueryOrdinaryDirectoryRequired
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			if owned {
				_ = current.Close()
			}
			return nil, err
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(st, opened) {
			_ = next.Close()
			if owned {
				_ = current.Close()
			}
			return nil, &queryDirectoryChangedError{cause: err}
		}
		if owned {
			_ = current.Close()
		}
		current, owned = next, true
	}
	if owned {
		return current, nil
	}
	pinned, err := root.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	return pinned, nil
}

type queryRegularFile struct {
	file       *os.File
	pathInfo   os.FileInfo
	openedInfo os.FileInfo
}

// openQueryRegularFile owns the Lstat/open/Stat identity sequence and closes
// the handle on every failed result. The caller may compare pathInfo with an
// inventory entry before open, and retain pathInfo for partial metadata when a
// later access step fails.
func openQueryRegularFile(directory *os.Root, name string, expected os.FileInfo) (*queryRegularFile, error) {
	result := &queryRegularFile{}
	st, err := directory.Lstat(name)
	if err != nil {
		return result, err
	}
	result.pathInfo = st
	if !st.Mode().IsRegular() {
		return result, errQueryRegularFileRequired
	}
	if expected != nil && (!os.SameFile(expected, st) || expected.Size() != st.Size() || !expected.ModTime().Equal(st.ModTime())) {
		return result, errQueryInventoryIdentityChanged
	}
	f, err := openDiscoveryFile(directory, name)
	if err != nil {
		return result, err
	}
	opened, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return result, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(st, opened) {
		_ = f.Close()
		return result, errQueryOpenedSourceChanged
	}
	result.file, result.openedInfo = f, opened
	return result, nil
}
