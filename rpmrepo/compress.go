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

// 各压缩格式的 magic 前缀，用于按内容（而不是文件名后缀）识别格式。
var (
	gzipMagic  = []byte{0x1f, 0x8b}
	bzip2Magic = []byte{'B', 'Z', 'h'}
	xzMagic    = []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}
	zstdMagic  = []byte{0x28, 0xb5, 0x2f, 0xfd}
)

// decompress 按 magic 自动识别压缩格式并返回解压后的读取器。
// 第二个返回值表示数据是否经过压缩；返回的 io.ReadCloser 关闭时会一并关闭底层读取器。
func decompress(raw io.Reader) (io.ReadCloser, bool, error) {
	buffered := bufio.NewReaderSize(raw, 4096)
	head, err := buffered.Peek(len(xzMagic))
	if err != nil && err != io.EOF {
		return nil, false, fmt.Errorf("rpmrepo: 读取元数据失败: %w", err)
	}

	switch {
	case bytes.HasPrefix(head, gzipMagic):
		zr, err := gzip.NewReader(buffered)
		if err != nil {
			return nil, true, fmt.Errorf("rpmrepo: 解压 gzip 元数据失败: %w", err)
		}
		return &compoundCloser{Reader: zr, closers: []io.Closer{zr, closerOf(raw)}}, true, nil
	case bytes.HasPrefix(head, bzip2Magic):
		return &compoundCloser{Reader: bzip2.NewReader(buffered), closers: []io.Closer{closerOf(raw)}}, true, nil
	case bytes.HasPrefix(head, xzMagic):
		xr, err := xz.NewReader(buffered)
		if err != nil {
			return nil, true, fmt.Errorf("rpmrepo: 解压 xz 元数据失败: %w", err)
		}
		return &compoundCloser{Reader: xr, closers: []io.Closer{closerOf(raw)}}, true, nil
	case bytes.HasPrefix(head, zstdMagic):
		zr, err := zstd.NewReader(buffered, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
		if err != nil {
			return nil, true, fmt.Errorf("rpmrepo: 解压 zstd 元数据失败: %w", err)
		}
		return &compoundCloser{Reader: zr, closers: []io.Closer{zr.IOReadCloser(), closerOf(raw)}}, true, nil
	}

	// 未识别的 magic：按未压缩的 XML 处理，首个非空白字符必须是 '<'。
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
			return fmt.Errorf("%w: 既不是 XML，也不是 gzip/bzip2/xz/zstd 压缩数据",
				ErrUnsupportedCompression)
		}
	}
	return nil
}

// CompressionOf 根据元数据文件名返回可读的压缩格式名，仅用于展示。
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
