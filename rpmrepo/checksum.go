package rpmrepo

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"strings"
)

// newHash 按算法名创建哈希计算器，算法名不区分大小写。
func newHash(algo string) (hash.Hash, error) {
	switch strings.ToLower(strings.TrimSpace(algo)) {
	case "sha256":
		return sha256.New(), nil
	case "sha512":
		return sha512.New(), nil
	case "sha384":
		return sha512.New384(), nil
	case "sha224":
		return sha256.New224(), nil
	case "sha1":
		return sha1.New(), nil
	case "md5":
		return md5.New(), nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedChecksum, algo)
	}
}

// verifyReader 边读边计算摘要，并在读到结尾时与期望值比对。
type verifyReader struct {
	r        io.Reader
	hash     hash.Hash
	algo     string
	expected string
	err      error
}

func (v *verifyReader) Read(p []byte) (int, error) {
	if v.err != nil {
		return 0, v.err
	}
	n, err := v.r.Read(p)
	if n > 0 {
		v.hash.Write(p[:n])
	}
	if err == io.EOF {
		actual := hex.EncodeToString(v.hash.Sum(nil))
		if !strings.EqualFold(actual, v.expected) {
			v.err = fmt.Errorf("%w: 期望 %s %s，实际 %s",
				ErrChecksumMismatch, v.algo, v.expected, actual)
			return n, v.err
		}
	}
	return n, err
}
