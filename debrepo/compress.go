package debrepo

import (
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"
)

// Magic prefixes of the supported compression formats, used to detect the format by content rather
// than by file name suffix.
var (
	gzipMagic      = []byte{0x1f, 0x8b}
	bzip2Magic     = []byte{'B', 'Z', 'h'}
	xzMagic        = []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}
	zstdMagic      = []byte{0x28, 0xb5, 0x2f, 0xfd}
	lz4Magic       = []byte{0x04, 0x22, 0x4d, 0x18}
	lz4LegacyMagic = []byte{0x02, 0x21, 0x4c, 0x18}
)

// IndexCompressions is the preference order of index compression formats: formats that are smallest
// and fastest to decompress come first, and the empty string means the uncompressed index (kept last
// as a fallback).
var IndexCompressions = []string{".xz", ".zst", ".gz", ".bz2", ".lz4", ""}

// decompress detects the compression format by magic bytes and returns a reader over the decompressed
// data together with the format name. When no compression magic is recognized, the data is treated as
// uncompressed (Packages, Sources, and Release can all be plain text).
func decompress(raw io.Reader) (io.ReadCloser, string, error) {
	buffered := bufio.NewReaderSize(raw, 4096)
	head, err := buffered.Peek(len(xzMagic))
	if err != nil && err != io.EOF {
		return nil, "", fmt.Errorf("debrepo: reading index data failed: %w", err)
	}

	switch {
	case bytes.HasPrefix(head, gzipMagic):
		zr, err := gzip.NewReader(buffered)
		if err != nil {
			return nil, "", fmt.Errorf("debrepo: decompressing gzip index failed: %w", err)
		}
		return &compoundCloser{Reader: zr, closers: []io.Closer{zr, closerOf(raw)}}, "gzip", nil
	case bytes.HasPrefix(head, bzip2Magic):
		return &compoundCloser{Reader: bzip2.NewReader(buffered), closers: []io.Closer{closerOf(raw)}}, "bzip2", nil
	case bytes.HasPrefix(head, xzMagic):
		xr, err := xz.NewReader(buffered)
		if err != nil {
			return nil, "", fmt.Errorf("debrepo: decompressing xz index failed: %w", err)
		}
		return &compoundCloser{Reader: xr, closers: []io.Closer{closerOf(raw)}}, "xz", nil
	case bytes.HasPrefix(head, zstdMagic):
		zr, err := zstd.NewReader(buffered, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
		if err != nil {
			return nil, "", fmt.Errorf("debrepo: decompressing zstd index failed: %w", err)
		}
		return &compoundCloser{Reader: zr, closers: []io.Closer{zr.IOReadCloser(), closerOf(raw)}}, "zstd", nil
	case bytes.HasPrefix(head, lz4Magic), bytes.HasPrefix(head, lz4LegacyMagic):
		return &compoundCloser{Reader: lz4.NewReader(buffered), closers: []io.Closer{closerOf(raw)}}, "lz4", nil
	}

	return &compoundCloser{Reader: buffered, closers: []io.Closer{closerOf(raw)}}, "none", nil
}

// CompressionOf returns a human-readable compression format name based on the index file name; it is
// meant for display purposes only.
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
	case strings.HasSuffix(name, ".lz4"):
		return "lz4"
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
