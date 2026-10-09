package simple_storage

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

var (
	gzipMagic = []byte{0x1f, 0x8b}
	zstdMagic = []byte{0x28, 0xb5, 0x2f, 0xfd}
)

// layeredReadCloser reads from r and, on Close, releases the decompressor
// (if any) and the underlying file.
type layeredReadCloser struct {
	io.Reader
	closers []func() error
}

func (l *layeredReadCloser) Close() error {
	var errs []error
	for _, c := range l.closers {
		if err := c(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// OpenMaybeCompressed opens path for reading and transparently decompresses
// gzip or zstd content. The format is detected from the leading magic bytes,
// never from the file name; anything else is returned as plain text.
// Closing the result closes the decompressor and the file.
func OpenMaybeCompressed(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	br := bufio.NewReader(f)
	// A short file is not an error here: Peek returns what it has.
	head, _ := br.Peek(len(zstdMagic))
	switch {
	case bytes.HasPrefix(head, gzipMagic):
		zr, err := gzip.NewReader(br)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("invalid gzip data: %w", err)
		}
		return &layeredReadCloser{Reader: zr, closers: []func() error{zr.Close, f.Close}}, nil
	case bytes.HasPrefix(head, zstdMagic):
		zr, err := zstd.NewReader(br)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("invalid zstd data: %w", err)
		}
		return &layeredReadCloser{Reader: zr, closers: []func() error{func() error { zr.Close(); return nil }, f.Close}}, nil
	}
	return &layeredReadCloser{Reader: br, closers: []func() error{f.Close}}, nil
}

// AtomicWriteCloser writes to a temporary file next to its target. Close
// flushes and renames it into place only if everything succeeded, so a failed
// save never replaces a previous good file; Abort discards it.
type AtomicWriteCloser interface {
	io.WriteCloser
	Abort()
}

type atomicFile struct {
	io.Writer
	closers   []func() error // compressor first, then the file
	tmp, path string
	failed    bool
}

func (a *atomicFile) Write(p []byte) (int, error) {
	n, err := a.Writer.Write(p)
	if err != nil {
		a.failed = true
	}
	return n, err
}

func (a *atomicFile) Close() error {
	var errs []error
	for _, c := range a.closers {
		if err := c(); err != nil {
			errs = append(errs, err)
		}
	}
	if a.failed && len(errs) == 0 {
		errs = append(errs, errors.New("an earlier write failed"))
	}
	if len(errs) == 0 {
		mode := os.FileMode(0o644)
		if st, err := os.Stat(a.path); err == nil {
			mode = st.Mode().Perm() // keep an existing file's permissions
		}
		if err := os.Chmod(a.tmp, mode); err != nil {
			errs = append(errs, err)
		} else if err := os.Rename(a.tmp, a.path); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		_ = os.Remove(a.tmp)
		return errors.Join(errs...)
	}
	return nil
}

func (a *atomicFile) Abort() {
	a.failed = true
	_ = a.Close()
}

// CreateMaybeCompressed opens path for an atomic write, compressing by its
// extension: .gz gzip, .zst/.zstd zstd, anything else plain text. Data goes to
// a temporary file in the same directory; the caller must Close the result and
// check the error (the file only appears at path then), or Abort it.
func CreateMaybeCompressed(path string) (AtomicWriteCloser, error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return nil, err
	}
	a := &atomicFile{Writer: f, closers: []func() error{f.Close}, tmp: f.Name(), path: path}
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".gz"):
		zw := gzip.NewWriter(f)
		a.Writer, a.closers = zw, []func() error{zw.Close, f.Close}
	case strings.HasSuffix(lower, ".zst"), strings.HasSuffix(lower, ".zstd"):
		zw, err := zstd.NewWriter(f, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
		if err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
			return nil, err
		}
		a.Writer, a.closers = zw, []func() error{zw.Close, f.Close}
	}
	return a, nil
}
