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

// 说明：真实仓库（gzip / zstd）的端到端解析在 realrepo_test.go 中验证，
// 这里只覆盖解压器自身的边界情况。bzip2 与 xz 在生产仓库中已少见，
// 因此用内存中的压缩数据验证解码器。

const decompressSample = `<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" packages="1">
  <package type="rpm"><name>bz2-test</name><arch>noarch</arch>
    <version epoch="0" ver="1.0" rel="1"/>
    <checksum type="sha256" pkgid="YES">deadbeef</checksum>
  </package>
</metadata>`

// bzip2Original 是 bzip2Sample 解压后的内容。
const bzip2Original = `<metadata xmlns="http://linux.duke.edu/metadata/common" packages="1">` +
	`<package type="rpm"><name>bz2-test</name><arch>noarch</arch>` +
	`<version epoch="0" ver="1.0" rel="1"/>` +
	`<checksum type="sha256" pkgid="YES">deadbeef</checksum></package></metadata>`

// bzip2Sample 是 bzip2Original 经 bzip2 -9 压缩后的结果（标准库没有 bzip2 写入实现）。
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
			// 压缩格式按内容识别，与文件名后缀无关。
			reader, compressed, err := decompress(bytes.NewReader(tc.data))
			if err != nil {
				t.Fatalf("decompress 失败: %v", err)
			}
			defer reader.Close()
			if !compressed {
				t.Error("压缩数据应当标记为 compressed")
			}
			got, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("读取解压数据失败: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Errorf("解压结果与原始数据不一致（%d != %d 字节）", len(got), len(tc.want))
			}

			// 用解压后的数据流式解析，确认与解析器衔接正常。
			var pkg Package
			if err := ParsePrimary(bytes.NewReader(got), func(p *Package) error {
				pkg = *p
				return nil
			}); err != nil {
				t.Fatalf("ParsePrimary 失败: %v", err)
			}
			if pkg.Name != "bz2-test" {
				t.Errorf("解析出的包名为 %q", pkg.Name)
			}
		})
	}
}

func TestDecompressPlainXML(t *testing.T) {
	reader, compressed, err := decompress(strings.NewReader("  <?xml version=\"1.0\"?>"))
	if err != nil {
		t.Fatalf("decompress 失败: %v", err)
	}
	defer reader.Close()
	if compressed {
		t.Error("未压缩数据不应标记为 compressed")
	}
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatalf("读取失败: %v", err)
	}
}

func TestDecompressUnsupported(t *testing.T) {
	_, _, err := decompress(strings.NewReader("\x00\x01\x02\x03"))
	if err == nil {
		t.Fatal("无法识别的数据应当报错")
	}
	if !strings.Contains(err.Error(), "不支持的元数据压缩格式") {
		t.Errorf("错误为 %v，期望包含不支持的压缩格式", err)
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
			t.Errorf("CompressionOf(%q) = %q，期望 %q", name, got, want)
		}
	}
}

func TestChecksumAlgorithms(t *testing.T) {
	for _, algo := range []string{"sha256", "SHA512", "sha384", "sha224", "sha1", "md5"} {
		if _, err := newHash(algo); err != nil {
			t.Errorf("算法 %s 应当被支持: %v", algo, err)
		}
	}
	if _, err := newHash("sha3-256"); err == nil {
		t.Error("sha3-256 应当返回不支持的错误")
	}

	body := io.NopCloser(strings.NewReader("<metadata/>"))
	if _, err := withVerification(body, &RepoMDData{
		OpenChecksum: Checksum{Type: "sha3-256", Value: "00"},
	}, true); err == nil || !strings.Contains(err.Error(), "不支持的校验算法") {
		t.Errorf("错误为 %v，期望不支持的校验算法", err)
	}

	body = io.NopCloser(strings.NewReader("<metadata/>"))
	reader, err := withVerification(body, &RepoMDData{
		OpenChecksum: Checksum{Type: "sha256", Value: sha256Hex([]byte("other"))},
	}, true)
	if err != nil {
		t.Fatalf("withVerification 失败: %v", err)
	}
	if _, err := io.ReadAll(reader); err == nil {
		t.Error("摘要不匹配时应当报错")
	} else if !strings.Contains(err.Error(), "校验和不匹配") {
		t.Errorf("错误为 %v，期望校验和不匹配", err)
	}

	// 未压缩的数据使用 checksum 校验。
	payload := []byte("<metadata/>")
	body = io.NopCloser(bytes.NewReader(payload))
	reader, err = withVerification(body, &RepoMDData{
		Checksum: Checksum{Type: "sha256", Value: sha256Hex(payload)},
	}, false)
	if err != nil {
		t.Fatalf("withVerification 失败: %v", err)
	}
	if _, err := io.ReadAll(reader); err != nil {
		t.Errorf("校验应当通过: %v", err)
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
		t.Fatalf("gzip 压缩失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip 压缩失败: %v", err)
	}
	return buffer.Bytes()
}

func compressXz(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer, err := xz.NewWriter(&buffer)
	if err != nil {
		t.Fatalf("xz 压缩失败: %v", err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatalf("xz 压缩失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("xz 压缩失败: %v", err)
	}
	return buffer.Bytes()
}

func compressZstd(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer, err := zstd.NewWriter(&buffer)
	if err != nil {
		t.Fatalf("zstd 压缩失败: %v", err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatalf("zstd 压缩失败: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("zstd 压缩失败: %v", err)
	}
	return buffer.Bytes()
}

func decodeHex(t *testing.T, value string) []byte {
	t.Helper()
	data, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("十六进制数据非法: %v", err)
	}
	return data
}
