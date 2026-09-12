package rpmrepo

import (
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// Magic prefixes of the supported compression formats, used to detect the format by content rather
// than by file name suffix.
var (
	gzipMagic  = []byte{0x1f, 0x8b}
	bzip2Magic = []byte{'B', 'Z', 'h'}
	xzMagic    = []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}
	zstdMagic  = []byte{0x28, 0xb5, 0x2f, 0xfd}
)

// decompress detects the compression format by magic bytes and returns a reader over the
// decompressed data. The second return value reports whether the data was compressed; closing the
// returned io.ReadCloser also closes the underlying reader.
func decompress(raw io.Reader) (io.ReadCloser, bool, error) {
	buffered := bufio.NewReaderSize(raw, 4096)
	head, err := buffered.Peek(len(xzMagic))
	if err != nil && err != io.EOF {
		return nil, false, fmt.Errorf("rpmrepo: reading metadata failed: %w", err)
	}

	switch {
	case bytes.HasPrefix(head, gzipMagic):
		zr, err := gzip.NewReader(buffered)
		if err != nil {
			return nil, true, fmt.Errorf("rpmrepo: decompressing gzip metadata failed: %w", err)
		}
		return &compoundCloser{Reader: zr, closers: []io.Closer{zr, closerOf(raw)}}, true, nil
	case bytes.HasPrefix(head, bzip2Magic):
		return &compoundCloser{Reader: bzip2.NewReader(buffered), closers: []io.Closer{closerOf(raw)}}, true, nil
	case bytes.HasPrefix(head, xzMagic):
		xr, err := xz.NewReader(buffered)
		if err != nil {
			return nil, true, fmt.Errorf("rpmrepo: decompressing xz metadata failed: %w", err)
		}
		return &compoundCloser{Reader: xr, closers: []io.Closer{closerOf(raw)}}, true, nil
	case bytes.HasPrefix(head, zstdMagic):
		zr, err := zstd.NewReader(buffered, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
		if err != nil {
			return nil, true, fmt.Errorf("rpmrepo: decompressing zstd metadata failed: %w", err)
		}
		return &compoundCloser{Reader: zr, closers: []io.Closer{zr.IOReadCloser(), closerOf(raw)}}, true, nil
	}

	// Unrecognized magic: treat the data as uncompressed XML, whose first non-whitespace character
	// must be '<'.
	if err := checkPlainXML(head); err != nil {
		return nil, false, err
	}
	return &compoundCloser{Reader: buffered, closers: []io.Closer{closerOf(raw)}}, false, nil
}

func checkPlainXML(head []byte) error {
	for _, c := range head {
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		case '<':
			return nil
		default:
			return fmt.Errorf("%w: data is neither XML nor gzip/bzip2/xz/zstd compressed",
				ErrUnsupportedCompression)
		}
	}
	return nil
}

// CompressionOf returns a human-readable compression format name based on the metadata file name;
// it is meant for display purposes only.
func CompressionOf(name string) string {
	switch {
	case strings.HasSuffix(name, ".gz"):
		return "gzip"
	case strings.HasSuffix(name, ".bz2"):
		return "bzip2"
	case strings.HasSuffix(name, ".xz"):
		return "xz"
	case strings.HasSuffix(name, ".zst"), strings.HasSuffix(name, ".zstd"):
		return "zstd"
	default:
		return "none"
	}
}

type compoundCloser struct {
	io.Reader
	closers []io.Closer
}

func (c *compoundCloser) Close() error {
	var firstErr error
	for _, closer := range c.closers {
		if closer == nil {
			continue
		}
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func closerOf(r io.Reader) io.Closer {
	if c, ok := r.(io.Closer); ok {
		return c
	}
	return nil
}
