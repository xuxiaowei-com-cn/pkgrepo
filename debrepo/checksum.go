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

// Checksum is an "algorithm + digest value" pair, for example sha256:c3f1d0...
type Checksum struct {
	// Type is the algorithm name: md5, sha1, sha256, or sha512.
	Type string `json:"type"`
	// Value is the hexadecimal digest value.
	Value string `json:"value"`
}

// String returns "type:value", or an empty string when the checksum value is empty.
func (c Checksum) String() string {
	if c.Value == "" {
		return ""
	}
	return c.Type + ":" + c.Value
}

// IsZero reports whether the checksum value is empty.
func (c Checksum) IsZero() bool { return c.Value == "" }

// Matches reports whether the digest equals value (case-insensitively).
func (c Checksum) Matches(value string) bool {
	return c.Value != "" && strings.EqualFold(c.Value, value)
}

// Equal reports whether two checksums have the same algorithm and digest.
func (c Checksum) Equal(o Checksum) bool {
	return strings.EqualFold(c.Type, o.Type) && strings.EqualFold(c.Value, o.Value)
}

// Checksums holds the checksums of a single package file under different algorithms.
type Checksums struct {
	MD5    string `json:"md5,omitempty"`
	SHA1   string `json:"sha1,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	SHA512 string `json:"sha512,omitempty"`
}

// Strongest returns the strongest available checksum (sha512 > sha256 > sha1 > md5).
func (c Checksums) Strongest() (Checksum, bool) {
	for _, algo := range []string{"sha512", "sha256", "sha1", "md5"} {
		if value, ok := c.Get(algo); ok {
			return Checksum{Type: algo, Value: value}, true
		}
	}
	return Checksum{}, false
}

// Get returns the checksum of the given algorithm; the algorithm name is case-insensitive.
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

// IsZero reports whether there are no checksums at all.
func (c Checksums) IsZero() bool {
	return c.MD5 == "" && c.SHA1 == "" && c.SHA256 == "" && c.SHA512 == ""
}

// checksumRank returns the strength ranking of an algorithm, used to pick the strongest among
// several candidate checksums.
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

// newHash creates a hash calculator from an algorithm name; the name is case-insensitive.
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

// verifyReader computes the digest while reading and compares it with the expected value at end of
// file.
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
			v.err = fmt.Errorf("%w: expected %s %s, got %s", ErrChecksumMismatch, v.algo, v.expected, actual)
			return n, v.err
		}
	}
	return n, err
}

// readCloser combines an io.Reader and an io.Closer into an io.ReadCloser.
type readCloser struct {
	io.Reader
	closer io.Closer
}

func (c *readCloser) Close() error { return c.closer.Close() }
