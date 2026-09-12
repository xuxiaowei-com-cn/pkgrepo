package rpmrepo

import (
	"errors"
	"strings"
	"testing"
)

// Note: the core parsing logic is verified in realrepo_test.go against the primary.xml of a real
// repository; this file only covers parsing details and edge cases that do not need network access.

func TestParsePrimaryMeta(t *testing.T) {
	document := `<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" packages="3">
  <package type="rpm"><name>nginx</name><arch>x86_64</arch><version epoch="0" ver="1.0"/></package>
</metadata>`
	meta, err := ParsePrimaryMeta(strings.NewReader(document))
	if err != nil {
		t.Fatalf("ParsePrimaryMeta failed: %v", err)
	}
	if meta.Packages != 3 {
		t.Errorf("the packages attribute is %d, want 3", meta.Packages)
	}
}

func TestParsePrimaryMetaMissing(t *testing.T) {
	if _, err := ParsePrimaryMeta(strings.NewReader("<other/>")); err == nil {
		t.Fatal("a missing metadata element should fail")
	}
}

func TestParsePrimaryStopsOnError(t *testing.T) {
	document := `<?xml version="1.0" encoding="UTF-8"?>
<metadata packages="2">
  <package type="rpm"><name>a</name><arch>noarch</arch><version epoch="0" ver="1"/></package>
  <package type="rpm"><name>b</name><arch>noarch</arch><version epoch="0" ver="1"/></package>
</metadata>`
	sentinel := errors.New("stop parsing")
	count := 0
	err := ParsePrimary(strings.NewReader(document), func(*Package) error {
		count++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("error is %v, want the error returned by the callback", err)
	}
	if count != 1 {
		t.Errorf("the callback ran %d times, want 1", count)
	}
}

func TestParsePrimaryInvalidXML(t *testing.T) {
	if err := ParsePrimary(strings.NewReader("<metadata><package>"), func(*Package) error { return nil }); err == nil {
		t.Fatal("invalid XML should fail")
	}
}

// TestParsePrimaryLatin1 covers metadata from old repositories whose XML declaration is ISO-8859-1.
func TestParsePrimaryLatin1(t *testing.T) {
	// The é (0xE9) below verifies the conversion from Latin-1 bytes to UTF-8.
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
		t.Fatalf("ParsePrimary failed: %v", err)
	}
	if pkg.Name != "café" || pkg.Summary != "café summary" {
		t.Errorf("Latin-1 to UTF-8 conversion failed: name=%q summary=%q", pkg.Name, pkg.Summary)
	}
	if !bool(pkg.Checksum.PkgID) {
		t.Error(`pkgid="YES" should parse as true`)
	}
	if got, want := pkg.Checksum.String(), "md5:0123456789abcdef"; got != want {
		t.Errorf("Checksum.String() is %q, want %q", got, want)
	}
}

func TestParsePrimaryUnsupportedCharset(t *testing.T) {
	if _, err := ParsePrimaryMeta(strings.NewReader(`<?xml version="1.0" encoding="SHIFT_JIS"?><metadata/>`)); err == nil {
		t.Fatal("an unsupported charset should fail")
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
		t.Errorf("NEVRA is %q, want %q", got, want)
	}
	if got, want := pkg.Filename(), "nginx-1.24.0-1.el9.src.rpm"; got != want {
		t.Errorf("Filename is %q, want %q", got, want)
	}
	if !pkg.IsSource() {
		t.Error("the src architecture should be reported as a source package")
	}
	if !pkg.Provides("webserver") || pkg.Provides("docker") {
		t.Error("Provides returned a wrong result")
	}
	if !pkg.Requires("openssl") || pkg.Requires("nginx") {
		t.Error("Requires returned a wrong result")
	}
	if got, want := pkg.Format.Requires[0].String(), "openssl >= 3.0"; got != want {
		t.Errorf("the dependency description is %q, want %q", got, want)
	}
	if pkg.Format.Requires[0].EVR().EpochInt() != 0 {
		t.Error("the dependency Epoch should be 0")
	}

	escaped := Package{Location: Location{Href: "Packages/nginx%20extras-1.0-1.noarch.rpm"}}
	if got, want := escaped.Filename(), "nginx extras-1.0-1.noarch.rpm"; got != want {
		t.Errorf("the escaped file name parsed as %q, want %q", got, want)
	}
	if (&Package{Arch: "nosrc"}).IsSource() == false {
		t.Error("the nosrc architecture is also a source package")
	}
}
