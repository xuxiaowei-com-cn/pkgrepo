package rpmrepo

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// Note: end-to-end parsing of real repositories (gzip / zstd) is verified in realrepo_test.go; this
// file only covers the edge cases of the decompressor itself. bzip2 and xz are rare in production
// repositories, so the decoders are verified with in-memory compressed data.

const decompressSample = `<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" packages="1">
  <package type="rpm"><name>bz2-test</name><arch>noarch</arch>
    <version epoch="0" ver="1.0" rel="1"/>
    <checksum type="sha256" pkgid="YES">deadbeef</checksum>
  </package>
</metadata>`

// bzip2Original is the decompressed content of bzip2Sample.
const bzip2Original = `<metadata xmlns="http://linux.duke.edu/metadata/common" packages="1">` +
	`<package type="rpm"><name>bz2-test</name><arch>noarch</arch>` +
	`<version epoch="0" ver="1.0" rel="1"/>` +
	`<checksum type="sha256" pkgid="YES">deadbeef</checksum></package></metadata>`

// bzip2Sample is bzip2Original compressed with bzip2 -9 (the standard library has no bzip2 writer).
const bzip2Sample = "425a683931415926535942d67ec200001b9f805003f317020008203fefdf703000acd8694da868c9a1a3401a" +
	"01a0354d3d4f534d34d0189a00001a135346a1e21a83d40c8c7a90f415ef43a36282eb0128b4c0a4e0b6d160637c32b2403e" +
	"956eeb67e308889da337d4a154e92bc415133cdd10a732327ec5906e3da6063858616c994023380b56adca412823787ed74b5d" +
	"c429c09f228ee054c75bbbaa9658a0539666858798de0940292fa2182d84ab66537e087d82b889a41919938352465f519a868121" +
	"674e0848cff17724538509042d67ec20"

func TestDecompress(t *testing.T) {
	original := []byte(decompressSample)
	cases := []struct {
		name string
		data []byte
		want []byte
	}{
		{name: "gzip", data: compressGzip(t, original), want: original},
		{name: "xz", data: compressXz(t, original), want: original},
		{name: "zstd", data: compressZstd(t, original), want: original},
		{name: "bzip2", data: decodeHex(t, bzip2Sample), want: []byte(bzip2Original)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The compression format is detected by content, independent of the file name suffix.
			reader, compressed, err := decompress(bytes.NewReader(tc.data))
			if err != nil {
				t.Fatalf("decompress failed: %v", err)
			}
			defer reader.Close()
			if !compressed {
				t.Error("compressed data should be marked as compressed")
			}
			got, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("reading the decompressed data failed: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Errorf("the decompressed result differs from the original data (%d != %d bytes)", len(got), len(tc.want))
			}

			// Stream the decompressed data through the parser to confirm the handoff works.
			var pkg Package
			if err := ParsePrimary(bytes.NewReader(got), func(p *Package) error {
				pkg = *p
				return nil
			}); err != nil {
				t.Fatalf("ParsePrimary failed: %v", err)
			}
			if pkg.Name != "bz2-test" {
				t.Errorf("the parsed package name is %q", pkg.Name)
			}
		})
	}
}

func TestDecompressPlainXML(t *testing.T) {
	reader, compressed, err := decompress(strings.NewReader("  <?xml version=\"1.0\"?>"))
	if err != nil {
		t.Fatalf("decompress failed: %v", err)
	}
	defer reader.Close()
	if compressed {
		t.Error("uncompressed data should not be marked as compressed")
	}
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("read failed: %v", err)
	}
}

func TestDecompressUnsupported(t *testing.T) {
	_, _, err := decompress(strings.NewReader("\x00\x01\x02\x03"))
	if err == nil {
		t.Fatal("unrecognized data should fail")
	}
	if !strings.Contains(err.Error(), "unsupported metadata compression format") {
		t.Errorf("error is %v, want it to mention an unsupported compression format", err)
	}
}

func TestCompressionOf(t *testing.T) {
	cases := map[string]string{
		"repodata/x-primary.xml":      "none",
		"repodata/x-primary.xml.gz":   "gzip",
		"repodata/x-primary.xml.bz2":  "bzip2",
		"repodata/x-primary.xml.xz":   "xz",
		"repodata/x-primary.xml.zst":  "zstd",
		"repodata/x-primary.xml.zstd": "zstd",
	}
	for name, want := range cases {
		if got := CompressionOf(name); got != want {
			t.Errorf("CompressionOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestChecksumAlgorithms(t *testing.T) {
	for _, algo := range []string{"sha256", "SHA512", "sha384", "sha224", "sha1", "md5"} {
		if _, err := newHash(algo); err != nil {
			t.Errorf("algorithm %s should be supported: %v", algo, err)
		}
	}
	if _, err := newHash("sha3-256"); err == nil {
		t.Error("sha3-256 should return an unsupported error")
	}

	body := io.NopCloser(strings.NewReader("<metadata/>"))
	if _, err := withVerification(body, &RepoMDData{
		OpenChecksum: Checksum{Type: "sha3-256", Value: "00"},
	}, true); err == nil || !strings.Contains(err.Error(), "unsupported checksum algorithm") {
		t.Errorf("error is %v, want an unsupported checksum algorithm", err)
	}

	body = io.NopCloser(strings.NewReader("<metadata/>"))
	reader, err := withVerification(body, &RepoMDData{
		OpenChecksum: Checksum{Type: "sha256", Value: sha256Hex([]byte("other"))},
	}, true)
	if err != nil {
		t.Fatalf("withVerification failed: %v", err)
	}
	if _, err := io.ReadAll(reader); err == nil {
		t.Error("a mismatching digest should fail")
	} else if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error is %v, want a checksum mismatch", err)
	}

	// Uncompressed data is verified with checksum.
	payload := []byte("<metadata/>")
	body = io.NopCloser(bytes.NewReader(payload))
	reader, err = withVerification(body, &RepoMDData{
		Checksum: Checksum{Type: "sha256", Value: sha256Hex(payload)},
	}, false)
	if err != nil {
		t.Fatalf("withVerification failed: %v", err)
	}
	if _, err := io.ReadAll(reader); err != nil {
		t.Errorf("verification should pass: %v", err)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func compressGzip(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(data); err != nil {
		t.Fatalf("gzip compression failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip compression failed: %v", err)
	}
	return buffer.Bytes()
}

func compressXz(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer, err := xz.NewWriter(&buffer)
	if err != nil {
		t.Fatalf("xz compression failed: %v", err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatalf("xz compression failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("xz compression failed: %v", err)
	}
	return buffer.Bytes()
}

func compressZstd(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer, err := zstd.NewWriter(&buffer)
	if err != nil {
		t.Fatalf("zstd compression failed: %v", err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatalf("zstd compression failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("zstd compression failed: %v", err)
	}
	return buffer.Bytes()
}

func decodeHex(t *testing.T, value string) []byte {
	t.Helper()
	data, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("invalid hexadecimal data: %v", err)
	}
	return data
}
