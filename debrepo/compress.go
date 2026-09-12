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

// 各压缩格式的 magic 前缀，用于按内容（而不是文件名后缀）识别格式。
var (
	gzipMagic      = []byte{0x1f, 0x8b}
	bzip2Magic     = []byte{'B', 'Z', 'h'}
	xzMagic        = []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}
	zstdMagic      = []byte{0x28, 0xb5, 0x2f, 0xfd}
	lz4Magic       = []byte{0x04, 0x22, 0x4d, 0x18}
	lz4LegacyMagic = []byte{0x02, 0x21, 0x4c, 0x18}
)

// IndexCompressions 是索引压缩格式的优先顺序：优先下载体积最小、解压最快的格式，
// 空字符串表示未压缩的原始索引（放在最后作为兜底）。
var IndexCompressions = []string{".xz", ".zst", ".gz", ".bz2", ".lz4", ""}

// decompress 按 magic 自动识别压缩格式并返回解压后的读取器与格式名。
// 未识别出压缩 magic 时按未压缩数据处理（Packages、Sources、Release 都可能是纯文本）。
func decompress(raw io.Reader) (io.ReadCloser, string, error) {
	buffered := bufio.NewReaderSize(raw, 4096)
	head, err := buffered.Peek(len(xzMagic))
	if err != nil && err != io.EOF {
		return nil, "", fmt.Errorf("debrepo: 读取索引数据失败: %w", err)
	}

	switch {
	case bytes.HasPrefix(head, gzipMagic):
		zr, err := gzip.NewReader(buffered)
		if err != nil {
			return nil, "", fmt.Errorf("debrepo: 解压 gzip 索引失败: %w", err)
		}
		return &compoundCloser{Reader: zr, closers: []io.Closer{zr, closerOf(raw)}}, "gzip", nil
	case bytes.HasPrefix(head, bzip2Magic):
		return &compoundCloser{Reader: bzip2.NewReader(buffered), closers: []io.Closer{closerOf(raw)}}, "bzip2", nil
	case bytes.HasPrefix(head, xzMagic):
		xr, err := xz.NewReader(buffered)
		if err != nil {
			return nil, "", fmt.Errorf("debrepo: 解压 xz 索引失败: %w", err)
		}
		return &compoundCloser{Reader: xr, closers: []io.Closer{closerOf(raw)}}, "xz", nil
	case bytes.HasPrefix(head, zstdMagic):
		zr, err := zstd.NewReader(buffered, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
		if err != nil {
			return nil, "", fmt.Errorf("debrepo: 解压 zstd 索引失败: %w", err)
		}
		return &compoundCloser{Reader: zr, closers: []io.Closer{zr.IOReadCloser(), closerOf(raw)}}, "zstd", nil
	case bytes.HasPrefix(head, lz4Magic), bytes.HasPrefix(head, lz4LegacyMagic):
		return &compoundCloser{Reader: lz4.NewReader(buffered), closers: []io.Closer{closerOf(raw)}}, "lz4", nil
	}

	return &compoundCloser{Reader: buffered, closers: []io.Closer{closerOf(raw)}}, "none", nil
}

// CompressionOf 根据索引文件名返回可读的压缩格式名，仅用于展示。
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
