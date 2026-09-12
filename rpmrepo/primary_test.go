package rpmrepo

import (
	"errors"
	"strings"
	"testing"
)

// 说明：核心解析逻辑在 realrepo_test.go 中直接使用真实仓库的 primary.xml 验证，
// 这里只覆盖不依赖网络的解析细节与边界情况。

func TestParsePrimaryMeta(t *testing.T) {
	document := `<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" packages="3">
  <package type="rpm"><name>nginx</name><arch>x86_64</arch><version epoch="0" ver="1.0"/></package>
</metadata>`
	meta, err := ParsePrimaryMeta(strings.NewReader(document))
	if err != nil {
		t.Fatalf("ParsePrimaryMeta 失败: %v", err)
	}
	if meta.Packages != 3 {
		t.Errorf("packages 属性为 %d，期望 3", meta.Packages)
	}
}

func TestParsePrimaryMetaMissing(t *testing.T) {
	if _, err := ParsePrimaryMeta(strings.NewReader("<other/>")); err == nil {
		t.Fatal("缺少 metadata 元素时应当报错")
	}
}

func TestParsePrimaryStopsOnError(t *testing.T) {
	document := `<?xml version="1.0" encoding="UTF-8"?>
<metadata packages="2">
  <package type="rpm"><name>a</name><arch>noarch</arch><version epoch="0" ver="1"/></package>
  <package type="rpm"><name>b</name><arch>noarch</arch><version epoch="0" ver="1"/></package>
</metadata>`
	sentinel := errors.New("停止解析")
	count := 0
	err := ParsePrimary(strings.NewReader(document), func(*Package) error {
		count++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("错误为 %v，期望回调返回的错误", err)
	}
	if count != 1 {
		t.Errorf("回调执行了 %d 次，期望 1 次", count)
	}
}

func TestParsePrimaryInvalidXML(t *testing.T) {
	if err := ParsePrimary(strings.NewReader("<metadata><package>"), func(*Package) error { return nil }); err == nil {
		t.Fatal("非法 XML 应当报错")
	}
}

// TestParsePrimaryLatin1 覆盖 xml 声明为 ISO-8859-1 的老仓库元数据。
func TestParsePrimaryLatin1(t *testing.T) {
	// 这里用 é（0xE9）验证 Latin-1 字节到 UTF-8 的转换。
	document := "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?>\n" +
		"<metadata xmlns=\"http://linux.duke.edu/metadata/common\" packages=\"1\">" +
		"<package type=\"rpm\"><name>caf\xe9</name><arch>noarch</arch>" +
		"<version epoch=\"0\" ver=\"1.0\" rel=\"1\"/>" +
		"<checksum type=\"md5\" pkgid=\"YES\">0123456789abcdef</checksum>" +
		"<summary>caf\xe9 summary</summary>" +
		"</package></metadata>"

	var pkg Package
	err := ParsePrimary(strings.NewReader(document), func(p *Package) error {
		pkg = *p
		return nil
	})
	if err != nil {
		t.Fatalf("ParsePrimary 失败: %v", err)
	}
	if pkg.Name != "café" || pkg.Summary != "café summary" {
		t.Errorf("Latin-1 转 UTF-8 失败: name=%q summary=%q", pkg.Name, pkg.Summary)
	}
	if !bool(pkg.Checksum.PkgID) {
		t.Error(`pkgid="YES" 应当解析为 true`)
	}
	if got, want := pkg.Checksum.String(), "md5:0123456789abcdef"; got != want {
		t.Errorf("Checksum.String() 为 %q，期望 %q", got, want)
	}
}

func TestParsePrimaryUnsupportedCharset(t *testing.T) {
	if _, err := ParsePrimaryMeta(strings.NewReader(`<?xml version="1.0" encoding="SHIFT_JIS"?><metadata/>`)); err == nil {
		t.Fatal("不支持的字符集应当报错")
	}
}

func TestPackageHelpers(t *testing.T) {
	pkg := Package{
		Type:     "rpm",
		Name:     "nginx",
		Arch:     "src",
		Version:  EVR{Epoch: "1", Version: "1.24.0", Release: "1.el9"},
		Location: Location{Href: "Source/nginx-1.24.0-1.el9.src.rpm"},
		Format: Format{
			Provides: []Dependency{{Name: "webserver"}},
			Requires: []Dependency{{Name: "openssl", Flags: "GE", Version: "3.0"}},
		},
	}
	if got, want := pkg.NEVRA(), "nginx-1:1.24.0-1.el9.src"; got != want {
		t.Errorf("NEVRA 为 %q，期望 %q", got, want)
	}
	if got, want := pkg.Filename(), "nginx-1.24.0-1.el9.src.rpm"; got != want {
		t.Errorf("Filename 为 %q，期望 %q", got, want)
	}
	if !pkg.IsSource() {
		t.Error("src 架构应当判定为源码包")
	}
	if !pkg.Provides("webserver") || pkg.Provides("docker") {
		t.Error("Provides 判定有误")
	}
	if !pkg.Requires("openssl") || pkg.Requires("nginx") {
		t.Error("Requires 判定有误")
	}
	if got, want := pkg.Format.Requires[0].String(), "openssl >= 3.0"; got != want {
		t.Errorf("依赖描述为 %q，期望 %q", got, want)
	}
	if pkg.Format.Requires[0].EVR().EpochInt() != 0 {
		t.Error("依赖的 Epoch 应为 0")
	}

	escaped := Package{Location: Location{Href: "Packages/nginx%20extras-1.0-1.noarch.rpm"}}
	if got, want := escaped.Filename(), "nginx extras-1.0-1.noarch.rpm"; got != want {
		t.Errorf("转义文件名解析为 %q，期望 %q", got, want)
	}
	if (&Package{Arch: "nosrc"}).IsSource() == false {
		t.Error("nosrc 架构也属于源码包")
	}
}
