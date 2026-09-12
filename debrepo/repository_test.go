package debrepo

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/ulikunitz/xz"
)

const testRepoBase = "https://repo.test/debian"

// testPackagesAMD64 mimics the content of a real Packages index (the fields match the official
// Debian index).
const testPackagesAMD64 = `Package: nginx
Version: 1.22.1-9
Architecture: amd64
Multi-Arch: foreign
Maintainer: Debian Nginx Maintainers <pkg-nginx-maintainers@lists.alioth.debian.org>
Installed-Size: 1600
Depends: libc6 (>= 2.34), libssl3 (>= 3.0.0)
Provides: httpd, httpd-cgi
Section: httpd
Priority: optional
Homepage: https://nginx.org
Description: small, powerful, scalable web/proxy server
 Nginx is a high-performance web and reverse proxy server.
Filename: pool/main/n/nginx/nginx_1.22.1-9_amd64.deb
Size: 523456
MD5sum: 8fd3d1f4f0f4a1b2c3d4e5f60718293a
SHA256: 1111111111111111111111111111111111111111111111111111111111111111

Package: nginx
Version: 1.24.0-1
Architecture: amd64
Multi-Arch: foreign
Maintainer: Debian Nginx Maintainers <pkg-nginx-maintainers@lists.alioth.debian.org>
Installed-Size: 1700
Depends: libc6 (>= 2.34)
Provides: httpd
Section: httpd
Priority: optional
Homepage: https://nginx.org
Description: small, powerful, scalable web/proxy server
Filename: pool/main/n/nginx/nginx_1.24.0-1_amd64.deb
Size: 600000
SHA256: 2222222222222222222222222222222222222222222222222222222222222222

Package: docker-ce
Version: 5:27.0.3-1~debian.12~bookworm
Architecture: amd64
Maintainer: Docker <support@docker.com>
Installed-Size: 70000
Depends: containerd.io (>= 1.6.24), libc6 (>= 2.34)
Provides: docker-engine
Section: admin
Priority: optional
Description: Docker: the open-source application container engine
Filename: pool/stable/amd64/docker-ce_27.0.3-1~debian.12~bookworm_amd64.deb
Size: 12345678
SHA256: 3333333333333333333333333333333333333333333333333333333333333333
`

// testPackagesAll mimics the separate binary-all index introduced in Debian 13.
const testPackagesAll = `Package: ca-certificates
Version: 20230311
Architecture: all
Multi-Arch: foreign
Maintainer: Julien Cristau <jcristau@debian.org>
Installed-Size: 400
Depends: openssl (>= 1.1.1), debconf (>= 0.5) | debconf-2.0
Section: misc
Priority: optional
Description: common CA certificates
Filename: pool/main/c/ca-certificates/ca-certificates_20230311_all.deb
Size: 152000
SHA256: 4444444444444444444444444444444444444444444444444444444444444444
`

// testSources mimics the content of a Sources index.
const testSources = `Package: nginx
Binary: nginx, nginx-doc
Version: 1.24.0-1
Maintainer: Debian Nginx Maintainers <pkg-nginx-maintainers@lists.alioth.debian.org>
Build-Depends: debhelper-compat (= 13), libpcre2-dev
Architecture: any
Standards-Version: 4.6.2
Format: 3.0 (quilt)
Files:
 8aff5863d586bb906822c63366a6cdae 1972 nginx_1.24.0-1.dsc
 b2f9f4b3f3643fdd2551ec6cbe9d6dd7 561880 nginx_1.24.0.orig.tar.gz
Checksums-Sha256:
 2050df2094dcf6a014ab27341e899c81af4c4eb09ba68e23d465c849be934889 1972 nginx_1.24.0-1.dsc
 5629a304c82f1e6733454e13999d46baaad14ae50cb17b1c57d7a23165a55a72 561880 nginx_1.24.0.orig.tar.gz
Directory: pool/main/n/nginx
Priority: optional
Section: httpd
Homepage: https://nginx.org
Vcs-Git: https://salsa.debian.org/nginx-team/nginx.git
Vcs-Browser: https://salsa.debian.org/nginx-team/nginx
`

// mapFetcher is an in-memory Fetcher keyed by path, which makes it convenient to test the whole flow
// offline.
type mapFetcher struct {
	files  map[string][]byte
	mu     sync.Mutex
	opened []string
}

func (f *mapFetcher) Open(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.opened = append(f.opened, parsed.Path)
	data, ok := f.files[parsed.Path]
	f.mu.Unlock()
	if !ok {
		return nil, &HTTPError{URL: rawURL, StatusCode: 404, Status: "404 Not Found"}
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (f *mapFetcher) requested() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.opened...)
}

func (f *mapFetcher) remove(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, path)
}

func (f *mapFetcher) put(path string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = data
}

// testRepository builds a complete offline repository: Release + Packages(xz/gz) + binary-all +
// Sources.
func testRepository(t *testing.T) *mapFetcher {
	t.Helper()
	xzAMD64 := xzBytes(t, []byte(testPackagesAMD64))
	gzAMD64 := gzipBytes(t, []byte(testPackagesAMD64))
	gzAll := gzipBytes(t, []byte(testPackagesAll))
	gzSources := gzipBytes(t, []byte(testSources))

	release := buildRelease(t, []releaseFile{
		{"main/binary-amd64/Packages.xz", xzAMD64},
		{"main/binary-amd64/Packages.gz", gzAMD64},
		{"main/binary-all/Packages.gz", gzAll},
		{"main/source/Sources.gz", gzSources},
	})

	fetcher := &mapFetcher{files: map[string][]byte{
		"/debian/dists/bookworm/Release":                                                release,
		"/debian/dists/bookworm/main/binary-amd64/Packages.xz":                          xzAMD64,
		"/debian/dists/bookworm/main/binary-amd64/Packages.gz":                          gzAMD64,
		"/debian/dists/bookworm/main/binary-all/Packages.gz":                            gzAll,
		"/debian/dists/bookworm/main/source/Sources.gz":                                 gzSources,
		"/debian/dists/bookworm/main/binary-amd64/by-hash/SHA256/" + sha256Hex(xzAMD64): xzAMD64,
		"/debian/dists/bookworm/main/binary-all/by-hash/SHA256/" + sha256Hex(gzAll):     gzAll,
		"/debian/dists/bookworm/main/source/by-hash/SHA256/" + sha256Hex(gzSources):     gzSources,
	}}
	return fetcher
}

// releaseFile is the input of buildRelease: an index path and its content.
type releaseFile struct {
	path string
	data []byte
}

// buildRelease generates a Release file matching a real repository (SHA256 section + by-hash).
func buildRelease(t *testing.T, files []releaseFile) []byte {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("Origin: pkgrepo-test\n")
	sb.WriteString("Label: pkgrepo-test\n")
	sb.WriteString("Suite: bookworm\n")
	sb.WriteString("Codename: bookworm\n")
	sb.WriteString("Version: 12.0\n")
	sb.WriteString("Date: Sat, 11 Jul 2026 09:02:23 UTC\n")
	sb.WriteString("Valid-Until: Sat, 11 Jul 2027 09:02:23 UTC\n")
	sb.WriteString("Acquire-By-Hash: yes\n")
	sb.WriteString("Architectures: amd64 all\n")
	sb.WriteString("Components: main\n")
	sb.WriteString("Description: pkgrepo test repository\n")
	sb.WriteString("SHA256:\n")
	for _, file := range files {
		sb.WriteString(" " + sha256Hex(file.data) + " " + strconv.Itoa(len(file.data)) + " " + file.path + "\n")
	}
	return []byte(sb.String())
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(data); err != nil {
		t.Fatalf("gzip write failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close failed: %v", err)
	}
	return buffer.Bytes()
}

func xzBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer, err := xz.NewWriter(&buffer)
	if err != nil {
		t.Fatalf("xz initialization failed: %v", err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatalf("xz write failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("xz close failed: %v", err)
	}
	return buffer.Bytes()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// testClient returns a client backed by the in-memory data source.
func testClient(t *testing.T, fetcher *mapFetcher, opts ...Option) *Client {
	t.Helper()
	return New(append([]Option{WithFetcher(fetcher)}, opts...)...)
}

// openRepo opens the test repository with the in-memory data source.
func openRepo(t *testing.T, fetcher *mapFetcher, opts ...Option) *Repository {
	t.Helper()
	repo, err := Open(context.Background(), testRepoBase, append([]Option{WithFetcher(fetcher)}, opts...)...)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	return repo
}

func TestOpenAndFindPackages(t *testing.T) {
	ctx := context.Background()
	repo := openRepo(t, testRepository(t), WithSuite("bookworm"), WithArchitecture("amd64"))
	if repo.Suite != "bookworm" || repo.Codename != "bookworm" {
		t.Errorf("Suite/Codename = %q/%q", repo.Suite, repo.Codename)
	}
	if strings.Join(repo.Components, ",") != "main" {
		t.Errorf("Components = %v", repo.Components)
	}
	if strings.Join(repo.Architectures, ",") != "amd64,all" {
		t.Errorf("Architectures = %v", repo.Architectures)
	}
	if repo.Release == nil || !repo.Release.AcquireByHash {
		t.Fatalf("unexpected Release parse result: %+v", repo.Release)
	}
	if repo.ReleaseURL != testRepoBase+"/dists/bookworm/Release" {
		t.Errorf("ReleaseURL = %q", repo.ReleaseURL)
	}

	pkgs, err := repo.FindPackages(ctx, Query{Name: "nginx"})
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("matched %d packages, want 2: %+v", len(pkgs), pkgs)
	}
	if pkgs[0].Version.String() != "1.24.0-1" || pkgs[1].Version.String() != "1.22.1-9" {
		t.Errorf("default sort = %q, %q", pkgs[0].Version.String(), pkgs[1].Version.String())
	}
	for _, pkg := range pkgs {
		if pkg.Component != "main" || pkg.Suite != "bookworm" {
			t.Errorf("the context information of the package is missing: %+v", pkg)
		}
		if pkg.RepoID != "repo.test/debian" {
			t.Errorf("RepoID = %q", pkg.RepoID)
		}
		if !strings.HasPrefix(pkg.DownloadURL, testRepoBase+"/pool/") {
			t.Errorf("DownloadURL = %q", pkg.DownloadURL)
		}
	}

	// Architecture-independent packages live in the binary-all index, and the default scan includes
	// it automatically.
	all, err := repo.FindPackages(ctx, Query{Name: "ca-certificates"})
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(all) != 1 || all[0].Architecture != "all" {
		t.Fatalf("the package in binary-all was not scanned: %+v", all)
	}
	if all[0].DownloadURL != testRepoBase+"/pool/main/c/ca-certificates/ca-certificates_20230311_all.deb" {
		t.Errorf("DownloadURL = %q", all[0].DownloadURL)
	}
}

func TestFindPackageLatest(t *testing.T) {
	ctx := context.Background()
	pkg := openRepo(t, testRepository(t), WithSuite("bookworm"), WithArchitecture("amd64"))
	latest, err := pkg.FindPackage(ctx, Query{Name: "nginx"})
	if err != nil {
		t.Fatalf("FindPackage failed: %v", err)
	}
	if latest.Version.String() != "1.24.0-1" {
		t.Errorf("newest version = %q", latest.Version.String())
	}
	if _, err := pkg.FindPackage(ctx, Query{Name: "no-such-package"}); !errors.Is(err, ErrPackageNotFound) {
		t.Errorf("a missing package should return ErrPackageNotFound, got %v", err)
	}
	pinned, err := pkg.FindPackages(ctx, Query{Name: "nginx", Version: "1.22.1-9"})
	if err != nil || len(pinned) != 1 || pinned[0].Version.String() != "1.22.1-9" {
		t.Errorf("version query = %+v, %v", pinned, err)
	}
	provides, err := pkg.FindPackages(ctx, Query{Provides: "docker-engine"})
	if err != nil || len(provides) != 1 || provides[0].Name != "docker-ce" {
		t.Errorf("Provides query = %+v, %v", provides, err)
	}
}

func TestPackageLevelHelpers(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepository(t)
	pkgs, err := ListPackages(ctx, testRepoBase, "docker-ce",
		WithFetcher(fetcher), WithSuite("bookworm"), WithArchitecture("amd64"))
	if err != nil {
		t.Fatalf("ListPackages failed: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0].Version.String() != "5:27.0.3-1~debian.12~bookworm" {
		t.Fatalf("ListPackages = %+v", pkgs)
	}
	if pkgs[0].Version.Epoch != 5 {
		t.Errorf("Epoch = %d", pkgs[0].Version.Epoch)
	}
	found, err := FindPackage(ctx, testRepoBase, "nginx",
		WithFetcher(fetcher), WithSuite("bookworm"), WithArchitecture("amd64"))
	if err != nil {
		t.Fatalf("FindPackage failed: %v", err)
	}
	if found.Version.String() != "1.24.0-1" {
		t.Errorf("FindPackage = %q", found.Version.String())
	}
	// The same fetcher is an in-memory data source and can be reused.
	if _, err := FindPackages(ctx, testRepoBase, Query{Name: "curl"},
		WithFetcher(fetcher), WithSuite("bookworm"), WithArchitecture("amd64")); err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
}

func TestChecksumVerification(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepository(t)
	repo := openRepo(t, fetcher, WithSuite("bookworm"), WithArchitecture("amd64"), WithChecksumVerification(true))
	if _, err := repo.FindPackages(ctx, Query{Name: "nginx"}); err != nil {
		t.Fatalf("the query failed while verification was enabled: %v", err)
	}

	// Tamper with the SHA256 recorded in the Release file (simulating a tampered or stale index).
	tampered := testRepository(t)
	releasePath := "/debian/dists/bookworm/Release"
	release := string(tampered.files[releasePath])
	xzHash := sha256Hex(tampered.files["/debian/dists/bookworm/main/binary-amd64/Packages.xz"])
	release = strings.Replace(release, xzHash, strings.Repeat("9", 64), 1)
	tampered.put(releasePath, []byte(release))

	repo = openRepo(t, tampered, WithSuite("bookworm"), WithArchitecture("amd64"), WithChecksumVerification(true))
	_, err := repo.FindPackages(ctx, Query{Name: "nginx"})
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Errorf("a checksum mismatch should return ErrChecksumMismatch, got %v", err)
	}
}

func TestByHashFallback(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepository(t)
	// Simulate a mirror that only serves by-hash addresses (the regular path returns 404).
	fetcher.remove("/debian/dists/bookworm/main/binary-amd64/Packages.xz")
	fetcher.remove("/debian/dists/bookworm/main/binary-amd64/Packages.gz")

	repo := openRepo(t, fetcher, WithSuite("bookworm"), WithArchitecture("amd64"))
	pkgs, err := repo.FindPackages(ctx, Query{Name: "nginx"})
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("matched %d packages, want 2", len(pkgs))
	}
	found := false
	for _, path := range fetcher.requested() {
		if strings.Contains(path, "/by-hash/SHA256/") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("it should fall back to the by-hash address: %v", fetcher.requested())
	}

	// WithByHashFirst should request the by-hash address first.
	first := testRepository(t)
	repo = openRepo(t, first, WithSuite("bookworm"), WithArchitecture("amd64"), WithByHashFirst(true))
	if _, err := repo.FindPackages(ctx, Query{Name: "nginx"}); err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	requested := first.requested()
	index := -1
	for i, path := range requested {
		if strings.Contains(path, "Packages") || strings.Contains(path, "by-hash") {
			index = i
			break
		}
	}
	if index < 0 || !strings.Contains(requested[index], "/by-hash/SHA256/") {
		t.Errorf("WithByHashFirst should request by-hash first, got %v", requested)
	}
}

func TestOpenWithIndexURL(t *testing.T) {
	ctx := context.Background()
	indexURL := testRepoBase + "/dists/bookworm/main/binary-amd64/Packages.gz"
	repo, err := Open(ctx, indexURL, WithFetcher(testRepository(t)))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if repo.Suite != "bookworm" || strings.Join(repo.Components, ",") != "main" {
		t.Errorf("information inferred from the index address = %q, %v", repo.Suite, repo.Components)
	}
	if _, err := repo.FindPackages(ctx, Query{Name: "nginx"}); err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}

	// When the repository has no Release file, the index address given by the caller is used directly
	// (for example in a small self-hosted repository).
	onlyIndex := &mapFetcher{files: map[string][]byte{
		"/debian/dists/bookworm/main/binary-amd64/Packages.gz": gzipBytes(t, []byte(testPackagesAMD64)),
	}}
	repo, err = Open(ctx, indexURL, WithFetcher(onlyIndex))
	if err != nil {
		t.Fatalf("Open without a Release file failed: %v", err)
	}
	if repo.Release != nil {
		t.Error("Release should be nil when there is no Release file")
	}
	pkgs, err := repo.FindPackages(ctx, Query{Name: "docker-ce"})
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0].DownloadURL != testRepoBase+"/pool/stable/amd64/docker-ce_27.0.3-1~debian.12~bookworm_amd64.deb" {
		t.Errorf("result = %+v", pkgs)
	}
}

func TestOpenErrors(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepository(t)
	if _, err := Open(ctx, testRepoBase, WithFetcher(fetcher)); !errors.Is(err, ErrSuiteRequired) {
		t.Errorf("an unset suite should return ErrSuiteRequired, got %v", err)
	}
	if _, err := Open(ctx, testRepoBase, WithFetcher(fetcher), WithSuite("nosuchsuite")); !errors.Is(err, ErrNotRepository) {
		t.Errorf("a nonexistent suite should return ErrNotRepository, got %v", err)
	}
	if _, err := Open(ctx, "ftp://repo.test/debian", WithFetcher(fetcher), WithSuite("bookworm")); err == nil {
		t.Error("an unsupported scheme should return an error")
	}
	if _, err := Open(ctx, "  ", WithFetcher(fetcher), WithSuite("bookworm")); err == nil {
		t.Error("an empty address should return an error")
	}
}

func TestFindSources(t *testing.T) {
	ctx := context.Background()
	repo := openRepo(t, testRepository(t), WithSuite("bookworm"), WithArchitecture("amd64"))
	source, err := repo.FindSource(ctx, SourceQuery{Name: "nginx"})
	if err != nil {
		t.Fatalf("FindSource failed: %v", err)
	}
	if source.ID() != "nginx_1.24.0-1" || source.Directory != "pool/main/n/nginx" {
		t.Errorf("source package = %s, directory = %s", source.ID(), source.Directory)
	}
	if strings.Join(source.Binary, ",") != "nginx,nginx-doc" {
		t.Errorf("Binary = %v", source.Binary)
	}
	if !source.BuildDepends.Has("debhelper-compat") {
		t.Errorf("BuildDepends = %v", source.BuildDepends)
	}
	if len(source.Files) != 2 {
		t.Fatalf("Files = %+v", source.Files)
	}
	dsc, ok := source.DSC()
	if !ok || dsc.Path != "nginx_1.24.0-1.dsc" {
		t.Fatalf("DSC = %+v, %t", dsc, ok)
	}
	if checksum, ok := source.ChecksumOf("nginx_1.24.0-1.dsc"); !ok || checksum.Type != "sha256" {
		t.Errorf("ChecksumOf = %+v, %t", checksum, ok)
	}
	dscURL, err := source.FileURL(dsc.Path)
	if err != nil {
		t.Fatalf("FileURL failed: %v", err)
	}
	if dscURL != testRepoBase+"/pool/main/n/nginx/nginx_1.24.0-1.dsc" {
		t.Errorf("FileURL = %q", dscURL)
	}
	if _, err := repo.FindSource(ctx, SourceQuery{Name: "no-such-source"}); !errors.Is(err, ErrPackageNotFound) {
		t.Errorf("a missing source package should return ErrPackageNotFound, got %v", err)
	}
}

func TestLocalDirectoryRepository(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepository(t)
	dir := t.TempDir()
	for path, data := range fetcher.files {
		filePath := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
			t.Fatalf("creating the directory failed: %v", err)
		}
		if err := os.WriteFile(filePath, data, 0o644); err != nil {
			t.Fatalf("writing the file failed: %v", err)
		}
	}
	repo, err := Open(ctx, filepath.Join(dir, "debian"), WithSuite("bookworm"), WithArchitecture("amd64"))
	if err != nil {
		t.Fatalf("Open of a local directory failed: %v", err)
	}
	if repo.BaseURL.Scheme != "file" {
		t.Errorf("BaseURL = %q", repo.BaseURL)
	}
	pkgs, err := repo.FindPackages(ctx, Query{Name: "nginx", Latest: true})
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0].Version.String() != "1.24.0-1" {
		t.Fatalf("result = %+v", pkgs)
	}
	if !strings.HasPrefix(pkgs[0].DownloadURL, "file://") {
		t.Errorf("DownloadURL = %q", pkgs[0].DownloadURL)
	}

	// A file:// address works as well.
	repoURL := (&url.URL{Scheme: "file", Path: filepath.Join(dir, "debian")}).String()
	if _, err := Open(ctx, repoURL, WithSuite("bookworm"), WithArchitecture("amd64")); err != nil {
		t.Fatalf("Open of a file:// address failed: %v", err)
	}
}

func TestScanErrorPropagation(t *testing.T) {
	ctx := context.Background()
	repo := openRepo(t, testRepository(t), WithSuite("bookworm"), WithArchitecture("amd64"))
	sentinel := errors.New("stop scanning")
	count := 0
	err := repo.Scan(ctx, func(*Package) error {
		count++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want %v", err, sentinel)
	}
	if count != 1 {
		t.Errorf("scanned %d packages, want 1", count)
	}
	// Every package (including those from binary-all) can be scanned.
	total := 0
	if err := repo.Scan(ctx, func(*Package) error { total++; return nil }); err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	if total != 4 {
		t.Errorf("scanned %d packages, want 4", total)
	}
}

func TestIndexes(t *testing.T) {
	fetcher := testRepository(t)
	repo := openRepo(t, fetcher, WithSuite("bookworm"), WithArchitecture("amd64"))
	indexes := repo.Indexes()
	if len(indexes) != 2 {
		t.Fatalf("index count = %d, want 2 (amd64 + all)", len(indexes))
	}
	first := indexes[0]
	if first.Kind != IndexPackages || first.Component != "main" || first.Architecture != "amd64" {
		t.Errorf("index = %+v", first)
	}
	if !first.FromRelease || !strings.HasSuffix(first.Path, "Packages.xz") {
		t.Errorf("preferred index = %+v", first)
	}
	if CompressionOf(first.Path) != "xz" || CompressionOf("Packages.gz") != "gzip" {
		t.Errorf("CompressionOf returned a wrong result")
	}
	if len(first.Candidates) != 4 {
		t.Errorf("candidate count = %d, want 4 (xz/by-hash + gz/by-hash)", len(first.Candidates))
	}
	if !first.Candidates[1].ByHash || first.Candidates[1].Size == 0 {
		t.Errorf("by-hash candidate = %+v", first.Candidates[1])
	}
	if indexes[1].Architecture != "all" {
		t.Errorf("second index = %+v", indexes[1])
	}
	sourceIndexes := repo.SourceIndexes()
	if len(sourceIndexes) != 1 || sourceIndexes[0].Kind != IndexSources ||
		!strings.HasSuffix(sourceIndexes[0].Path, "Sources.gz") {
		t.Errorf("source indexes = %+v", sourceIndexes)
	}
}

func TestParseRepoURL(t *testing.T) {
	cases := []struct {
		in           string
		base         string
		suite        string
		component    string
		architecture string
		kind         IndexKind
		indexPath    string
		indexURL     string
	}{
		{
			in:   "https://deb.debian.org/debian",
			base: "https://deb.debian.org/debian/",
		},
		{
			in:   "https://deb.debian.org/debian/",
			base: "https://deb.debian.org/debian/",
		},
		{
			in:    "https://deb.debian.org/debian/dists/bookworm",
			base:  "https://deb.debian.org/debian/",
			suite: "bookworm",
		},
		{
			in:           "https://deb.debian.org/debian/dists/bookworm/main/binary-amd64",
			base:         "https://deb.debian.org/debian/",
			suite:        "bookworm",
			component:    "main",
			architecture: "amd64",
			kind:         IndexPackages,
		},
		{
			in:           "https://deb.debian.org/debian/dists/bookworm/main/binary-amd64/Packages.gz",
			base:         "https://deb.debian.org/debian/",
			suite:        "bookworm",
			component:    "main",
			architecture: "amd64",
			kind:         IndexPackages,
			indexPath:    "dists/bookworm/main/binary-amd64/Packages.gz",
			indexURL:     "https://deb.debian.org/debian/dists/bookworm/main/binary-amd64/Packages.gz",
		},
		{
			in:        "https://deb.debian.org/debian/dists/bookworm/main/source/Sources.xz",
			base:      "https://deb.debian.org/debian/",
			suite:     "bookworm",
			component: "main",
			kind:      IndexSources,
			indexPath: "dists/bookworm/main/source/Sources.xz",
			indexURL:  "https://deb.debian.org/debian/dists/bookworm/main/source/Sources.xz",
		},
		{
			in:    "https://deb.debian.org/debian/dists/bookworm/Release",
			base:  "https://deb.debian.org/debian/",
			suite: "bookworm",
		},
		{
			in:           "file:///srv/repo/dists/trixie/main/binary-arm64/Packages.xz",
			base:         "file:///srv/repo/",
			suite:        "trixie",
			component:    "main",
			architecture: "arm64",
			kind:         IndexPackages,
			indexPath:    "dists/trixie/main/binary-arm64/Packages.xz",
			indexURL:     "file:///srv/repo/dists/trixie/main/binary-arm64/Packages.xz",
		},
	}
	for _, tc := range cases {
		location, err := parseRepoURL(tc.in)
		if err != nil {
			t.Errorf("parseRepoURL(%q) failed: %v", tc.in, err)
			continue
		}
		if got := location.base.String(); got != tc.base {
			t.Errorf("parseRepoURL(%q) base = %q, want %q", tc.in, got, tc.base)
		}
		if location.suite != tc.suite || location.component != tc.component ||
			location.architecture != tc.architecture || location.kind != tc.kind {
			t.Errorf("parseRepoURL(%q) = %q/%q/%q/%q, want %q/%q/%q/%q", tc.in,
				location.suite, location.component, location.architecture, location.kind,
				tc.suite, tc.component, tc.architecture, tc.kind)
		}
		if location.indexPath != tc.indexPath || location.indexURL != tc.indexURL {
			t.Errorf("parseRepoURL(%q) index = %q/%q, want %q/%q", tc.in,
				location.indexPath, location.indexURL, tc.indexPath, tc.indexURL)
		}
	}

	if _, err := parseRepoURL(""); err == nil {
		t.Error("an empty address should return an error")
	}
	if _, err := parseRepoURL("ftp://repo.test/debian"); err == nil {
		t.Error("an unsupported scheme should return an error")
	}
}

func TestHostArchitecture(t *testing.T) {
	cases := map[string]string{
		"amd64": "amd64", "386": "i386", "arm": "armhf", "arm64": "arm64",
		"ppc64le": "ppc64el", "s390x": "s390x", "riscv64": "riscv64", "loong64": "loong64",
	}
	for goarch, want := range cases {
		if got := dpkgArchitecture(goarch); got != want {
			t.Errorf("dpkgArchitecture(%q) = %q, want %q", goarch, got, want)
		}
	}
	if HostArchitecture() == "" {
		t.Error("HostArchitecture should not be empty")
	}
}

func TestByHashURL(t *testing.T) {
	checksum := Checksum{Type: "sha256", Value: strings.Repeat("a", 64)}
	got, err := byHashURL("https://repo.test/debian/dists/bookworm/main/binary-amd64/Packages.xz", checksum)
	if err != nil {
		t.Fatalf("byHashURL failed: %v", err)
	}
	want := "https://repo.test/debian/dists/bookworm/main/binary-amd64/by-hash/SHA256/" + strings.Repeat("a", 64)
	if got != want {
		t.Errorf("byHashURL = %q, want %q", got, want)
	}
	if _, err := byHashURL("", Checksum{Type: "crc32", Value: "x"}); err == nil {
		t.Error("an unsupported algorithm should return an error")
	}
}

func TestIndexNotFound(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepository(t)
	// Delete every binary index and by-hash candidate to simulate a repository that does not provide
	// an index for this architecture.
	for path := range fetcher.files {
		if strings.Contains(path, "binary-") {
			fetcher.remove(path)
		}
	}
	release := string(fetcher.files["/debian/dists/bookworm/Release"])
	var kept []string
	for _, line := range strings.Split(release, "\n") {
		if !strings.Contains(line, "binary-") {
			kept = append(kept, line)
		}
	}
	fetcher.put("/debian/dists/bookworm/Release", []byte(strings.Join(kept, "\n")))
	repo := openRepo(t, fetcher, WithSuite("bookworm"), WithArchitecture("amd64"))
	if _, err := repo.FindPackages(ctx, Query{Name: "nginx"}); !errors.Is(err, ErrIndexNotFound) {
		t.Errorf("a missing index should return ErrIndexNotFound, got %v", err)
	}
}

func TestCompressionDetection(t *testing.T) {
	const data = "Package: nginx\nVersion: 1.0\n\n"
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"plain", []byte(data), "none"},
		{"gzip", gzipBytes(t, []byte(data)), "gzip"},
		{"xz", xzBytes(t, []byte(data)), "xz"},
	}
	for _, tc := range cases {
		body, format, err := decompress(bytes.NewReader(tc.data))
		if err != nil {
			t.Errorf("%s: decompress failed: %v", tc.name, err)
			continue
		}
		decoded, err := io.ReadAll(body)
		if err != nil {
			t.Errorf("%s: read failed: %v", tc.name, err)
		}
		body.Close()
		if format != tc.want {
			t.Errorf("%s: format = %q, want %q", tc.name, format, tc.want)
		}
		if string(decoded) != data {
			t.Errorf("%s: content = %q", tc.name, decoded)
		}
	}
	if _, _, err := decompress(bytes.NewReader(nil)); err != nil {
		t.Errorf("empty data should not fail: %v", err)
	}
}

func TestFindPackagesLimitAndSort(t *testing.T) {
	ctx := context.Background()
	repo := openRepo(t, testRepository(t), WithSuite("bookworm"), WithArchitecture("amd64"))
	pkgs, err := repo.FindPackages(ctx, Query{Name: "*", Limit: 2, Sort: SortVersionAsc})
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("Limit = %d", len(pkgs))
	}
	if pkgs[0].Name != "ca-certificates" {
		t.Errorf("the first package in name+version-ascending order = %q", pkgs[0].Name)
	}
	if pkgs[0].String() == "" {
		t.Error("Package.String() should not be empty")
	}
}
