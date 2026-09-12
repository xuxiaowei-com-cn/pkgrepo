package rpmrepo

import (
	"fmt"
	"io"
	"strings"
)

// charsetReader 支持 XML 声明中的常见字符集：
// UTF-8 / US-ASCII 直接透传，ISO-8859-1（Latin-1）转换为 UTF-8。
// 仓库元数据绝大多数是 UTF-8，这里主要是为了兼容老仓库。
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return input, nil
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1":
		return &latin1Reader{src: input}, nil
	default:
		return nil, fmt.Errorf("rpmrepo: 不支持的 XML 字符集 %q", charset)
	}
}

// latin1Reader 把 Latin-1 字节流转换为 UTF-8。
type latin1Reader struct {
	src     io.Reader
	pending []byte
	err     error
}

func (l *latin1Reader) Read(p []byte) (int, error) {
	if len(l.pending) == 0 {
		if l.err != nil {
			return 0, l.err
		}
		buf := make([]byte, 4096)
		n, err := l.src.Read(buf)
		if n > 0 {
			converted := make([]byte, 0, n*2)
			for _, b := range buf[:n] {
				if b < 0x80 {
					converted = append(converted, b)
					continue
				}
				converted = append(converted, 0xc0|b>>6, 0x80|b&0x3f)
			}
			l.pending = converted
		}
		if err != nil {
			l.err = err
		}
		if len(l.pending) == 0 {
			return 0, l.err
		}
	}
	n := copy(p, l.pending)
	l.pending = l.pending[n:]
	return n, nil
}
