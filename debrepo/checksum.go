package debrepo

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

// Checksum 是一个"算法 + 摘要值"，例如 sha256:c3f1d0…。
type Checksum struct {
	// Type 是算法名：md5、sha1、sha256、sha512。
	Type string `json:"type"`
	// Value 是十六进制的摘要值。
	Value string `json:"value"`
}

// String 返回 "type:value"，校验值为空时返回空字符串。
func (c Checksum) String() string {
	if c.Value == "" {
		return ""
	}
	return c.Type + ":" + c.Value
}

// IsZero 判断校验值是否为空。
func (c Checksum) IsZero() bool { return c.Value == "" }

// Matches 判断摘要值是否与 value 相同（不区分大小写）。
func (c Checksum) Matches(value string) bool {
	return c.Value != "" && strings.EqualFold(c.Value, value)
}

// Equal 判断两个校验值的算法与摘要是否都相同。
func (c Checksum) Equal(o Checksum) bool {
	return strings.EqualFold(c.Type, o.Type) && strings.EqualFold(c.Value, o.Value)
}

// Checksums 是同一个软件包文件在不同算法下的校验值。
type Checksums struct {
	MD5    string `json:"md5,omitempty"`
	SHA1   string `json:"sha1,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	SHA512 string `json:"sha512,omitempty"`
}

// Strongest 返回可用的最强校验值（sha512 > sha256 > sha1 > md5）。
func (c Checksums) Strongest() (Checksum, bool) {
	for _, algo := range []string{"sha512", "sha256", "sha1", "md5"} {
		if value, ok := c.Get(algo); ok {
			return Checksum{Type: algo, Value: value}, true
		}
	}
	return Checksum{}, false
}

// Get 返回指定算法的校验值，算法名不区分大小写。
func (c Checksums) Get(algo string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(algo)) {
	case "md5", "md5sum":
		return c.MD5, c.MD5 != ""
	case "sha1":
		return c.SHA1, c.SHA1 != ""
	case "sha256":
		return c.SHA256, c.SHA256 != ""
	case "sha512":
		return c.SHA512, c.SHA512 != ""
	default:
		return "", false
	}
}

// IsZero 判断是否没有任何校验值。
func (c Checksums) IsZero() bool {
	return c.MD5 == "" && c.SHA1 == "" && c.SHA256 == "" && c.SHA512 == ""
}

// checksumRank 返回算法的强度排序，用于在多个候选校验值中挑选最强的。
func checksumRank(algo string) int {
	switch strings.ToLower(strings.TrimSpace(algo)) {
	case "sha512":
		return 4
	case "sha256":
		return 3
	case "sha1":
		return 2
	case "md5":
		return 1
	default:
		return 0
	}
}

// newHash 按算法名创建哈希计算器，算法名不区分大小写。
func newHash(algo string) (hash.Hash, error) {
	switch strings.ToLower(strings.TrimSpace(algo)) {
	case "sha512":
		return sha512.New(), nil
	case "sha384":
		return sha512.New384(), nil
	case "sha256":
		return sha256.New(), nil
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
			v.err = fmt.Errorf("%w: 期望 %s %s，实际 %s", ErrChecksumMismatch, v.algo, v.expected, actual)
			return n, v.err
		}
	}
	return n, err
}

// readCloser 把 io.Reader 与 io.Closer 组合成 io.ReadCloser。
type readCloser struct {
	io.Reader
	closer io.Closer
}

func (c *readCloser) Close() error { return c.closer.Close() }
