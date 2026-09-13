package rpmrepo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const (
	testBaseOSBase    = "https://repo.test/almalinux/9/BaseOS/x86_64/os"
	testAppStreamBase = "https://repo.test/almalinux/9/AppStream/x86_64/os"
)

// testBaseOSPrimary mimics the primary metadata of a BaseOS repository: it ships nginx 1.24.0 and a
// package that only exists there.
const testBaseOSPrimary = `<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" xmlns:rpm="http://linux.duke.edu/metadata/rpm" packages="2">
  <package type="rpm">
    <name>nginx</name>
    <arch>x86_64</arch>
    <version epoch="1" ver="1.24.0" rel="1.el9"/>
    <checksum type="sha256" pkgid="YES">1111111111111111111111111111111111111111111111111111111111111111</checksum>
    <summary>nginx web server</summary>
    <size package="1200000" installed="3400000" archive="3600000"/>
    <location href="Packages/n/nginx-1.24.0-1.el9.x86_64.rpm"/>
    <time file="1700000000" build="1699900000"/>
  </package>
  <package type="rpm">
    <name>kernel</name>
    <arch>x86_64</arch>
    <version epoch="0" ver="5.14.0" rel="1.el9"/>
    <checksum type="sha256" pkgid="YES">2222222222222222222222222222222222222222222222222222222222222222</checksum>
    <summary>the Linux kernel</summary>
    <size package="9000000" installed="60000000" archive="62000000"/>
    <location href="Packages/k/kernel-5.14.0-1.el9.x86_64.rpm"/>
    <time file="1700000000" build="1699800000"/>
  </package>
</metadata>`

// testAppStreamPrimary mimics the primary metadata of an AppStream repository: it ships a newer nginx
// and its own package.
const testAppStreamPrimary = `<?xml version="1.0" encoding="UTF-8"?>
<metadata xmlns="http://linux.duke.edu/metadata/common" xmlns:rpm="http://linux.duke.edu/metadata/rpm" packages="2">
  <package type="rpm">
    <name>nginx</name>
    <arch>x86_64</arch>
    <version epoch="1" ver="1.26.0" rel="1.el9"/>
    <checksum type="sha256" pkgid="YES">3333333333333333333333333333333333333333333333333333333333333333</checksum>
    <summary>nginx web server</summary>
    <size package="1300000" installed="3500000" archive="3700000"/>
    <location href="Packages/n/nginx-1.26.0-1.el9.x86_64.rpm"/>
    <time file="1710000000" build="1709900000"/>
  </package>
  <package type="rpm">
    <name>htop</name>
    <arch>x86_64</arch>
    <version epoch="0" ver="3.3.0" rel="1.el9"/>
    <checksum type="sha256" pkgid="YES">4444444444444444444444444444444444444444444444444444444444444444</checksum>
    <summary>interactive process viewer</summary>
    <size package="180000" installed="450000" archive="470000"/>
    <location href="Packages/h/htop-3.3.0-1.el9.x86_64.rpm"/>
    <time file="1710000000" build="1709000000"/>
  </package>
</metadata>`

// mapFetcher is an in-memory Fetcher keyed by path, which makes it convenient to test the whole flow
// of several repositories offline.
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

func (f *mapFetcher) put(name string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[name] = data
}

// testRepositories returns an in-memory data source holding two repositories, the BaseOS and
// AppStream repositories of the same distribution.
func testRepositories(t *testing.T) *mapFetcher {
	t.Helper()
	fetcher := &mapFetcher{files: map[string][]byte{}}
	putTestRepository(t, fetcher, "/almalinux/9/BaseOS/x86_64/os", testBaseOSPrimary)
	putTestRepository(t, fetcher, "/almalinux/9/AppStream/x86_64/os", testAppStreamPrimary)
	return fetcher
}

// putTestRepository writes an offline repository below basePath: repodata/repomd.xml plus the primary
// metadata given as uncompressed XML (the checksums match, so verification can be enabled).
func putTestRepository(t *testing.T, fetcher *mapFetcher, basePath, primaryXML string) {
	t.Helper()
	primary := []byte(primaryXML)
	sum := sha256.Sum256(primary)
	digest := hex.EncodeToString(sum[:])
	repomd := `<?xml version="1.0" encoding="UTF-8"?>
<repomd xmlns="http://linux.duke.edu/metadata/repo" xmlns:rpm="http://linux.duke.edu/metadata/rpm">
  <revision>1700000000</revision>
  <data type="primary">
    <checksum type="sha256">` + digest + `</checksum>
    <open-checksum type="sha256">` + digest + `</open-checksum>
    <location href="repodata/primary.xml"/>
    <timestamp>1700000000</timestamp>
    <size>` + strconv.Itoa(len(primary)) + `</size>
    <open-size>` + strconv.Itoa(len(primary)) + `</open-size>
  </data>
</repomd>`
	fetcher.put(path.Join(basePath, "repodata/repomd.xml"), []byte(repomd))
	fetcher.put(path.Join(basePath, "repodata/primary.xml"), primary)
}

// testOptions returns the options shared by the multi-repository tests (offline data source with
// checksum verification enabled).
func testOptions(fetcher *mapFetcher, extra ...Option) []Option {
	return append([]Option{WithFetcher(fetcher), WithChecksumVerification(true)}, extra...)
}

func TestFindPackagesAcrossRepositories(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepositories(t)
	opts := testOptions(fetcher, WithRepositories(testAppStreamBase))

	pkgs, err := FindPackages(ctx, testBaseOSBase, Query{Name: "nginx"}, opts...)
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("matched %d packages, want 2: %+v", len(pkgs), pkgs)
	}
	// The merged list is sorted like a single repository: name ascending, version descending.
	if got := pkgs[0].Version.String(); got != "1:1.26.0-1.el9" {
		t.Errorf("newest nginx = %q", got)
	}
	if pkgs[0].RepoID != "repo.test/almalinux/9/AppStream/x86_64/os" ||
		pkgs[0].RepoURL != testAppStreamBase+"/" {
		t.Errorf("newest nginx comes from %q/%q", pkgs[0].RepoID, pkgs[0].RepoURL)
	}
	if pkgs[1].RepoID != "repo.test/almalinux/9/BaseOS/x86_64/os" {
		t.Errorf("nginx 1.24.0-1.el9 comes from %q", pkgs[1].RepoID)
	}
	if !strings.HasPrefix(pkgs[0].DownloadURL, testAppStreamBase+"/Packages/") ||
		!strings.HasPrefix(pkgs[1].DownloadURL, testBaseOSBase+"/Packages/") {
		t.Errorf("download URLs = %q, %q", pkgs[0].DownloadURL, pkgs[1].DownloadURL)
	}

	// Packages that only exist in one of the repositories are found as well.
	htop, err := FindPackages(ctx, testBaseOSBase, Query{Name: "htop"}, opts...)
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(htop) != 1 || htop[0].NEVRA() != "htop-3.3.0-1.el9.x86_64" {
		t.Errorf("htop = %+v", htop)
	}
	kernel, err := FindPackages(ctx, testBaseOSBase, Query{Name: "kernel"}, opts...)
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(kernel) != 1 || kernel[0].RepoID != "repo.test/almalinux/9/BaseOS/x86_64/os" {
		t.Errorf("kernel = %+v", kernel)
	}
}

// TestFindPackagesMergedQuery verifies that Latest, Limit, and the sort order apply to the merged
// result rather than to every repository on its own.
func TestFindPackagesMergedQuery(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepositories(t)

	latest, err := FindPackages(ctx, testBaseOSBase, Query{Name: "nginx", Latest: true},
		testOptions(fetcher, WithRepositories(testAppStreamBase))...)
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(latest) != 1 || latest[0].Version.String() != "1:1.26.0-1.el9" {
		t.Fatalf("Latest across repositories = %+v", latest)
	}

	limited, err := FindPackages(ctx, testBaseOSBase, Query{Limit: 2},
		testOptions(fetcher, WithRepositories(testAppStreamBase))...)
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("Limit = 2 returned %d packages, want 2", len(limited))
	}
	if limited[0].Name != "htop" || limited[1].Name != "kernel" {
		t.Errorf("the limited result is not sorted: %q, %q", limited[0].Name, limited[1].Name)
	}

	newest, err := FindPackages(ctx, testBaseOSBase, Query{Sort: SortBuildTimeDesc},
		testOptions(fetcher, WithRepositories(testAppStreamBase))...)
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(newest) != 4 || newest[0].Name != "nginx" || newest[0].Time.Build != 1709900000 {
		t.Errorf("SortBuildTimeDesc across repositories = %+v", newest)
	}
}

func TestFindPackageAcrossRepositories(t *testing.T) {
	ctx := context.Background()
	latest, err := FindPackage(ctx, testBaseOSBase, "nginx",
		testOptions(testRepositories(t), WithRepositories(testAppStreamBase))...)
	if err != nil {
		t.Fatalf("FindPackage failed: %v", err)
	}
	if latest.Version.String() != "1:1.26.0-1.el9" || latest.RepoID != "repo.test/almalinux/9/AppStream/x86_64/os" {
		t.Errorf("FindPackage = %s (%s)", latest.Version.String(), latest.RepoID)
	}
}

func TestListPackagesAcrossRepositories(t *testing.T) {
	ctx := context.Background()
	pkgs, err := ListPackages(ctx, testBaseOSBase, "nginx",
		testOptions(testRepositories(t), WithRepositories(testAppStreamBase))...)
	if err != nil {
		t.Fatalf("ListPackages failed: %v", err)
	}
	if len(pkgs) != 2 || pkgs[0].Name != "nginx" || pkgs[1].Name != "nginx" {
		t.Errorf("ListPackages = %+v", pkgs)
	}
}

// TestRepositoriesAddresses covers how the address list is built: the address of the call comes
// first, the address of the call may be empty, blanks are ignored, and repeated addresses are read
// once.
func TestRepositoriesAddresses(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepositories(t)

	// The same repository passed twice is only read once.
	pkgs, err := FindPackages(ctx, testBaseOSBase, Query{Name: "nginx"},
		testOptions(fetcher, WithRepositories(testAppStreamBase, testBaseOSBase, " "))...)
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("a repeated address should not duplicate packages: %+v", pkgs)
	}
	primaryRequests := 0
	for _, requested := range fetcher.requested() {
		if strings.HasSuffix(requested, "/repodata/primary.xml") {
			primaryRequests++
		}
	}
	if primaryRequests != 2 {
		t.Errorf("primary metadata was read %d times, want 2 (once per repository)", primaryRequests)
	}

	// An empty address is allowed when the option supplies every repository.
	pkgs, err = FindPackages(ctx, "", Query{Name: "nginx"},
		testOptions(fetcher, WithRepositories(testBaseOSBase, testAppStreamBase))...)
	if err != nil {
		t.Fatalf("FindPackages with an empty address failed: %v", err)
	}
	if len(pkgs) != 2 {
		t.Errorf("FindPackages without an address = %+v", pkgs)
	}
}

func TestFindPackagesWithoutRepository(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepositories(t)
	for _, tc := range []struct {
		name string
		opts []Option
	}{
		{"no address", testOptions(fetcher)},
		{"blank addresses", testOptions(fetcher, WithRepositories("", " "))},
	} {
		if _, err := FindPackages(ctx, "", Query{Name: "nginx"}, tc.opts...); !errors.Is(err, ErrNoRepository) {
			t.Errorf("%s: error is %v, want ErrNoRepository", tc.name, err)
		}
	}
	if _, err := ListPackages(ctx, "", "nginx", testOptions(fetcher)...); !errors.Is(err, ErrNoRepository) {
		t.Errorf("ListPackages error is %v, want ErrNoRepository", err)
	}
}

// TestFindPackagesRepositoryError verifies that a repository that cannot be read fails the whole
// query and that the error keeps the address and the underlying reason.
func TestFindPackagesRepositoryError(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepositories(t)
	const missing = "https://repo.test/almalinux/9/CRB/x86_64/os"

	_, err := FindPackages(ctx, testBaseOSBase, Query{Name: "nginx"},
		testOptions(fetcher, WithRepositories(missing))...)
	if err == nil {
		t.Fatal("reading a missing repository should fail")
	}
	if !errors.Is(err, ErrNotRepository) {
		t.Errorf("error is %v, want ErrNotRepository", err)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("the error should name the failing repository: %v", err)
	}
}

// TestOpenRejectsSeveralRepositories verifies that the single-repository entry point refuses a client
// configured with further addresses, because a *Repository describes one repository.
func TestOpenRejectsSeveralRepositories(t *testing.T) {
	fetcher := testRepositories(t)
	_, err := Open(context.Background(), testBaseOSBase,
		testOptions(fetcher, WithRepositories(testAppStreamBase))...)
	if !errors.Is(err, ErrMultipleRepositories) {
		t.Errorf("error is %v, want ErrMultipleRepositories", err)
	}
	// A client without further addresses keeps working.
	repo, err := Open(context.Background(), testBaseOSBase, testOptions(fetcher)...)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if repo.ID != "repo.test/almalinux/9/BaseOS/x86_64/os" {
		t.Errorf("ID = %q", repo.ID)
	}
}
