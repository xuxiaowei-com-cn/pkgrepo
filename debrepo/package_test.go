package debrepo

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const packageSample = `Package: nginx
Source: nginx (1.22.1-9)
Version: 1:1.22.1-9~deb12u1
Architecture: amd64
Multi-Arch: foreign
Essential: no
Maintainer: Debian Nginx Maintainers <pkg-nginx-maintainers@lists.alioth.debian.org>
Original-Maintainer: Nginx Maintainers <nginx@packages.debian.org>
Installed-Size: 1600
Depends: libc6 (>= 2.34), libcrypt1 (>= 1:4.4.10-10), libssl3 (>= 3.0.0)
Pre-Depends: dpkg (>= 1.17.14)
Recommends: python3-certbot-nginx | python3-certbot
Suggests: nginx-doc
Provides: httpd, httpd-cgi
Conflicts: nginx-extras
Replaces: nginx-common
Breaks: nginx-core (<< 1.22)
Enhances: libnginx-mod-http-geoip2
Section: httpd
Priority: optional
Homepage: https://nginx.org
Description: small, powerful, scalable web/proxy server
 Nginx ("engine X") is a high-performance web and reverse proxy server.
 It can also be used as a mail proxy server.
 .
 This package provides the nginx binary.
Tag: role::program, use::proxying
Filename: pool/main/n/nginx/nginx_1.22.1-9~deb12u1_amd64.deb
Size: 523456
MD5sum: 8fd3d1f4f0f4a1b2c3d4e5f60718293a
SHA1: 1e2b0e5d4e6f70819293a4b5c6d7e8f901234567
SHA256: 5c8e0d3a2b1f4e6d8c7b6a5f4e3d2c1b0a9f8e7d6c5b4a39281706f5e4d3c2b1a
Description-md5: a1b2c3d4e5f60718293a4b5c6d7e8f90
`

func TestParsePackage(t *testing.T) {
	pkg, err := ParsePackageStanza(mustStanza(t, packageSample))
	if err != nil {
		t.Fatalf("ParsePackageStanza failed: %v", err)
	}
	if pkg.Name != "nginx" {
		t.Errorf("Name = %q", pkg.Name)
	}
	if pkg.Source != "nginx" || pkg.SourceVersion != "1.22.1-9" {
		t.Errorf("Source/SourceVersion = %q/%q", pkg.Source, pkg.SourceVersion)
	}
	if pkg.Version.String() != "1:1.22.1-9~deb12u1" {
		t.Errorf("Version = %q", pkg.Version.String())
	}
	if pkg.Architecture != "amd64" || pkg.MultiArch != "foreign" {
		t.Errorf("Architecture/MultiArch = %q/%q", pkg.Architecture, pkg.MultiArch)
	}
	if pkg.Essential {
		t.Error("Essential should be false")
	}
	if pkg.Section != "httpd" || pkg.Priority != "optional" {
		t.Errorf("Section/Priority = %q/%q", pkg.Section, pkg.Priority)
	}
	if pkg.Maintainer == "" || !strings.Contains(pkg.OriginalMaintainer, "Nginx Maintainers") {
		t.Errorf("Maintainer = %q, OriginalMaintainer = %q", pkg.Maintainer, pkg.OriginalMaintainer)
	}
	if pkg.Homepage != "https://nginx.org" {
		t.Errorf("Homepage = %q", pkg.Homepage)
	}
	if pkg.Size.File != 523456 || pkg.Size.Installed != 1600 {
		t.Errorf("Size = %+v", pkg.Size)
	}
	if pkg.Checksums.MD5 == "" || pkg.Checksums.SHA1 == "" || pkg.Checksums.SHA256 == "" {
		t.Errorf("Checksums = %+v", pkg.Checksums)
	}
	if checksum, ok := pkg.Checksum(); !ok || checksum.Type != "sha256" {
		t.Errorf("Checksum() = %+v, %t", checksum, ok)
	}
	if pkg.Description.Synopsis != "small, powerful, scalable web/proxy server" {
		t.Errorf("Description.Synopsis = %q", pkg.Description.Synopsis)
	}
	wantLong := "Nginx (\"engine X\") is a high-performance web and reverse proxy server.\n" +
		"It can also be used as a mail proxy server.\n\nThis package provides the nginx binary."
	if pkg.Description.Long != wantLong {
		t.Errorf("Description.Long = %q, want %q", pkg.Description.Long, wantLong)
	}
	if !strings.HasPrefix(pkg.Description.String(), pkg.Description.Synopsis) {
		t.Errorf("Description.String() = %q", pkg.Description.String())
	}
	if pkg.Filename != "pool/main/n/nginx/nginx_1.22.1-9~deb12u1_amd64.deb" {
		t.Errorf("Filename = %q", pkg.Filename)
	}
	if pkg.BaseFilename() != "nginx_1.22.1-9~deb12u1_amd64.deb" {
		t.Errorf("BaseFilename() = %q", pkg.BaseFilename())
	}
	if pkg.ID() != "nginx_1:1.22.1-9~deb12u1_amd64" {
		t.Errorf("ID() = %q", pkg.ID())
	}
	if pkg.IsArchitectureIndependent() {
		t.Error("an amd64 package should not be treated as architecture-independent")
	}
	if pkg.SourceName() != "nginx" {
		t.Errorf("SourceName() = %q", pkg.SourceName())
	}
	if got := pkg.Field("Description-md5"); got != "a1b2c3d4e5f60718293a4b5c6d7e8f90" {
		t.Errorf("Field(Description-md5) = %q", got)
	}
	if !pkg.HasField("tag") || pkg.HasField("no-such-field") {
		t.Errorf("HasField returned a wrong result")
	}
}

func TestPackageDependencies(t *testing.T) {
	pkg, err := ParsePackageStanza(mustStanza(t, packageSample))
	if err != nil {
		t.Fatalf("ParsePackageStanza failed: %v", err)
	}
	if len(pkg.Depends) != 3 {
		t.Fatalf("Depends count = %d, want 3", len(pkg.Depends))
	}
	if !pkg.DependsOn("libc6") || pkg.DependsOn("libssl1.1") {
		t.Errorf("DependsOn returned a wrong result: %v", pkg.Depends.Names())
	}
	if !pkg.ProvidesPackage("httpd") || pkg.ProvidesPackage("postfix") {
		t.Errorf("ProvidesPackage returned a wrong result: %v", pkg.Provides.Names())
	}
	if !pkg.PreDepends.Has("dpkg") {
		t.Errorf("PreDepends = %v", pkg.PreDepends)
	}
	if !pkg.Recommends.Has("python3-certbot") {
		t.Errorf("Recommends = %v", pkg.Recommends)
	}
	if !pkg.Conflicts.Has("nginx-extras") || !pkg.Replaces.Has("nginx-common") ||
		!pkg.Breaks.Has("nginx-core") || !pkg.Enhances.Has("libnginx-mod-http-geoip2") {
		t.Errorf("another dependency field was parsed incorrectly")
	}
	// The version constraint of the dependency must be preserved, and the epoch form
	// (1:4.4.10-10) must not be damaged.
	dep, ok := pkg.Depends.Find("libcrypt1")
	if !ok {
		t.Fatal("the libcrypt1 dependency should be found")
	}
	if dep.Alternatives[0].Operator != ">=" || dep.Alternatives[0].Version != "1:4.4.10-10" {
		t.Errorf("libcrypt1 dependency = %+v", dep.Alternatives[0])
	}
}

func TestParsePackagesStreaming(t *testing.T) {
	data := packageSample + "\n" + strings.ReplaceAll(packageSample, "nginx", "nginx-doc") + "\n"
	var names []string
	err := ParsePackages(strings.NewReader(data), func(pkg *DebPackage) error {
		names = append(names, pkg.Name)
		return nil
	})
	if err != nil {
		t.Fatalf("ParsePackages failed: %v", err)
	}
	if strings.Join(names, ",") != "nginx,nginx-doc" {
		t.Errorf("parse result = %v", names)
	}

	// An error returned by fn should stop parsing and be returned unchanged.
	sentinel := errors.New("stop parsing")
	count := 0
	err = ParsePackages(strings.NewReader(data), func(*DebPackage) error {
		count++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want %v", err, sentinel)
	}
	if count != 1 {
		t.Errorf("parsed %d packages, want 1", count)
	}
}

func TestParsePackageErrors(t *testing.T) {
	if _, err := ParsePackageStanza(mustStanza(t, "Version: 1.0\n")); !errors.Is(err, ErrInvalidPackage) {
		t.Errorf("a missing Package field should return ErrInvalidPackage, got %v", err)
	}
	if _, err := ParsePackageStanza(mustStanza(t, "Package: a\nSize: huge\n")); !errors.Is(err, ErrInvalidPackage) {
		t.Errorf("an invalid Size should return ErrInvalidPackage, got %v", err)
	}
	if _, err := ParsePackageStanza(mustStanza(t, "Package: a\nDepends: libc6 (>=\n")); !errors.Is(err, ErrInvalidPackage) {
		t.Errorf("an invalid Depends should return ErrInvalidPackage, got %v", err)
	}
	if _, err := ParsePackageStanza(mustStanza(t, "Package: a\nVersion: :1.0\n")); !errors.Is(err, ErrInvalidPackage) {
		t.Errorf("an invalid Version should return ErrInvalidPackage, got %v", err)
	}
}

func TestPackageJSON(t *testing.T) {
	pkg, err := ParsePackageStanza(mustStanza(t, packageSample))
	if err != nil {
		t.Fatalf("ParsePackageStanza failed: %v", err)
	}
	pkg.Component = "main"
	pkg.Suite = "bookworm"
	pkg.DownloadURL = "https://repo.test/debian/" + pkg.Filename
	data, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var decoded struct {
		Name         string   `json:"name"`
		Version      string   `json:"version"`
		Architecture string   `json:"architecture"`
		Depends      []string `json:"depends"`
		Size         struct {
			File      int64 `json:"file"`
			Installed int64 `json:"installed"`
		} `json:"size"`
		Description struct {
			Synopsis string `json:"synopsis"`
		} `json:"description"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded.Name != "nginx" || decoded.Version != "1:1.22.1-9~deb12u1" || decoded.Architecture != "amd64" {
		t.Errorf("JSON = %s", data)
	}
	if len(decoded.Depends) != 3 || !strings.HasPrefix(decoded.Depends[0], "libc6 (>= 2.34)") {
		t.Errorf("JSON Depends = %v", decoded.Depends)
	}
	if decoded.Size.File != 523456 || decoded.Size.Installed != 1600 {
		t.Errorf("JSON Size = %+v", decoded.Size)
	}
	if decoded.Description.Synopsis == "" {
		t.Errorf("JSON Description = %+v", decoded.Description)
	}
}

// mustStanza parses a single paragraph, which makes it convenient to build test data.
func mustStanza(t *testing.T, data string) *Stanza {
	t.Helper()
	stanzas, err := ParseStanzas(strings.NewReader(data))
	if err != nil {
		t.Fatalf("parsing the test data failed: %v", err)
	}
	if len(stanzas) != 1 {
		t.Fatalf("the test data should contain exactly 1 paragraph, got %d", len(stanzas))
	}
	return &stanzas[0]
}
