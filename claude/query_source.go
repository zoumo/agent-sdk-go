package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// querySource owns a file and the evidence needed to verify the ranges read
// from it. The caller owns the directory roots and keeps them open until close.
type querySource struct {
	ctx    context.Context
	root   *os.Root
	path   string
	file   *os.File
	info   os.FileInfo
	ranges []querySourceRange
}

type querySourceRange struct {
	start, size int64
	hash        [32]byte
}

// A failed open may retain stat information for partial Metadata results;
// it never returns an owned file on failure. expected is an optional inventory
// identity, used by child queries before reading any content.
func openQuerySource(ctx context.Context, root, directory *os.Root, path string, expected os.FileInfo) (*querySource, error) {
	s := &querySource{ctx: ctx, root: root, path: path}
	display := filepath.Join(root.Name(), path)
	if err := ctx.Err(); err != nil {
		return s, err
	}
	name := filepath.Base(path)
	st, err := directory.Lstat(name)
	if err != nil {
		return s, queryAccess(display, err)
	}
	if !st.Mode().IsRegular() {
		return s, queryError("unsupported", display, "not a regular file", ErrUnsupported)
	}
	s.info = st
	if expected != nil && (!os.SameFile(expected, st) || expected.Size() != st.Size() || !expected.ModTime().Equal(st.ModTime())) {
		return s, queryError("changed", display, "source changed after inventory", nil)
	}
	f, err := openDiscoveryFile(directory, name)
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			return s, queryError("unsupported", display, "local query platform", err)
		}
		return s, queryAccess(display, err)
	}
	initial, err := f.Stat()
	if err != nil {
		f.Close()
		return s, queryAccess(display, err)
	}
	if !initial.Mode().IsRegular() || !os.SameFile(st, initial) || initial.Size() != st.Size() {
		f.Close()
		return s, queryError("changed", display, "source changed during open", nil)
	}
	s.file, s.info = f, initial
	return s, nil
}

func (s *querySource) close() error { return s.file.Close() }

func (s *querySource) readAll(maxBytes int64) ([]byte, error) {
	if s.info.Size() > maxBytes {
		return nil, queryError("resource-limit", filepath.Join(s.root.Name(), s.path), "source bytes", nil)
	}
	return s.read(0, s.info.Size())
}

type querySourceSample struct {
	head, tail []byte
	sampled    bool
}

func (s *querySource) sample(sampleBytes, maxBytes int64) (querySourceSample, error) {
	var result querySourceSample
	headSize := min(s.info.Size(), sampleBytes)
	tailStart := max(headSize, s.info.Size()-sampleBytes)
	result.sampled = tailStart > headSize
	// Read the preceding byte to distinguish a whole tail line from a fragment.
	// It counts toward both the read budget and verification, but not raw Tail.
	if result.sampled {
		tailStart--
	}
	tailSize := s.info.Size() - tailStart
	if headSize > maxBytes || tailSize > maxBytes-headSize {
		return result, queryError("resource-limit", filepath.Join(s.root.Name(), s.path), "metadata sample bytes", nil)
	}
	var err error
	result.head, err = s.read(0, headSize)
	if err != nil {
		return result, err
	}
	result.tail, err = s.read(tailStart, tailSize)
	return result, err
}

func (s *querySource) read(start, size int64) ([]byte, error) {
	data, err := readQueryRange(s.ctx, s.file, start, size)
	if err == nil {
		s.ranges = append(s.ranges, querySourceRange{start, size, sha256.Sum256(data)})
	}
	return data, err
}

// Verify the ranges actually read, the pinned handle and the original pathname.
// Looking up through root also observes replacement of an ancestor directory.
// This is a change detector, not an atomic filesystem snapshot.
func (s *querySource) verify() error {
	for _, span := range s.ranges {
		again, err := readQueryRange(s.ctx, s.file, span.start, span.size)
		if err != nil {
			return err
		}
		if sha256.Sum256(again) != span.hash {
			return queryError("changed", s.path, "source content changed", nil)
		}
	}
	final, err := s.file.Stat()
	if err != nil {
		return queryAccess(s.path, err)
	}
	current, err := s.root.Lstat(s.path)
	if err != nil {
		return queryError("changed", s.path, "source removed/replaced", err)
	}
	if !os.SameFile(s.info, current) || final.Size() != s.info.Size() || current.Size() != s.info.Size() || !final.ModTime().Equal(s.info.ModTime()) || !current.ModTime().Equal(s.info.ModTime()) {
		return queryError("changed", s.path, "source append/rewrite/replacement observed", nil)
	}
	return s.ctx.Err()
}

func readQueryRange(ctx context.Context, f *os.File, start, size int64) ([]byte, error) {
	var b bytes.Buffer
	r := io.NewSectionReader(f, start, size)
	chunk := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := r.Read(chunk)
		b.Write(chunk[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if int64(b.Len()) != size {
		return nil, queryError("changed", f.Name(), "source shortened", io.ErrUnexpectedEOF)
	}
	return b.Bytes(), nil
}
