package debrepo

import (
	"context"
	"errors"
	"maps"
	"path"
	"slices"
	"strings"
	"testing"
)

const testSecurityBase = "https://repo.test/debian-security"

// testSecurityPackages mimics the Packages index of a security archive: it ships a newer nginx than
// the main repository and a package that only exists there.
const testSecurityPackages = `Package: nginx
Version: 1.26.0-1
Architecture: amd64
Maintainer: Debian Security Team <team@security.debian.org>
Installed-Size: 1800
Depends: libc6 (>= 2.34)
Provides: httpd
Section: httpd
Priority: optional
Description: small, powerful, scalable web/proxy server (security update)
Filename: pool/updates/main/n/nginx/nginx_1.26.0-1_amd64.deb
Size: 650000
SHA256: 5555555555555555555555555555555555555555555555555555555555555555

Package: curl
Version: 8.5.0-2
Architecture: amd64
Maintainer: Debian Curl Maintainers <team+curl@tracker.debian.org>
Installed-Size: 450
Depends: libc6 (>= 2.34), libcurl4 (= 8.5.0-2)
Section: web
Priority: optional
Description: command line tool for transferring data with URL syntax
Filename: pool/updates/main/c/curl/curl_8.5.0-2_amd64.deb
Size: 340000
SHA256: 6666666666666666666666666666666666666666666666666666666666666666
`

// testSecuritySources mimics the Sources index of that archive.
const testSecuritySources = `Package: nginx
Binary: nginx, nginx-doc
Version: 1.26.0-1
Maintainer: Debian Nginx Maintainers <pkg-nginx-maintainers@lists.alioth.debian.org>
Build-Depends: debhelper-compat (= 13), libpcre2-dev
Architecture: any
Format: 3.0 (quilt)
Files:
 8aff5863d586bb906822c63366a6cdae 1972 nginx_1.26.0-1.dsc
 b2f9f4b3f3643fdd2551ec6cbe9d6dd7 561880 nginx_1.26.0.orig.tar.gz
Checksums-Sha256:
 2050df2094dcf6a014ab27341e899c81af4c4eb09ba68e23d465c849be934889 1972 nginx_1.26.0-1.dsc
 5629a304c82f1e6733454e13999d46baaad14ae50cb17b1c57d7a23165a55a72 561880 nginx_1.26.0.orig.tar.gz
Directory: pool/updates/main/n/nginx
Priority: optional
Section: httpd
`

// testRepositories returns an in-memory data source holding the main repository (testRepository)
// plus a security archive, which makes multi-repository queries testable offline.
func testRepositories(t *testing.T) *mapFetcher {
	t.Helper()
	fetcher := testRepository(t)
	putTestRepository(t, fetcher, "/debian-security", "bookworm", map[string]string{
		"main/binary-amd64/Packages.xz": testSecurityPackages,
		"main/binary-all/Packages.gz":   "",
		"main/source/Sources.gz":        testSecuritySources,
	})
	return fetcher
}

// putTestRepository writes an offline repository below basePath: a Release file of the given suite
// plus every index in indexes, whose key is a path below dists/<suite> (the compression follows the
// file suffix).
func putTestRepository(t *testing.T, fetcher *mapFetcher, basePath, suite string, indexes map[string]string) {
	t.Helper()
	files := make([]releaseFile, 0, len(indexes))
	for _, indexPath := range slices.Sorted(maps.Keys(indexes)) {
		content := []byte(indexes[indexPath])
		var data []byte
		switch {
		case strings.HasSuffix(indexPath, ".xz"):
			data = xzBytes(t, content)
		case strings.HasSuffix(indexPath, ".gz"):
			data = gzipBytes(t, content)
		default:
			data = content
		}
		fetcher.put(path.Join(basePath, "dists", suite, indexPath), data)
		files = append(files, releaseFile{indexPath, data})
	}
	fetcher.put(path.Join(basePath, "dists", suite, "Release"), buildRelease(t, files))
}

// testOptions returns the options shared by the multi-repository tests (bookworm/amd64 offline).
func testOptions(fetcher *mapFetcher, extra ...Option) []Option {
	return append([]Option{WithFetcher(fetcher), WithSuite("bookworm"), WithArchitecture("amd64")}, extra...)
}

func TestFindPackagesAcrossRepositories(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepositories(t)
	opts := testOptions(fetcher, WithRepositories(testSecurityBase))

	pkgs, err := FindPackages(ctx, testRepoBase, Query{Name: "nginx"}, opts...)
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(pkgs) != 3 {
		t.Fatalf("matched %d packages, want 3: %+v", len(pkgs), pkgs)
	}
	// The merged list is sorted like a single repository: name ascending, version descending.
	versions := make([]string, 0, len(pkgs))
	for i := range pkgs {
		versions = append(versions, pkgs[i].Version.String())
	}
	if got := strings.Join(versions, " "); got != "1.26.0-1 1.24.0-1 1.22.1-9" {
		t.Errorf("merged sort = %q", got)
	}
	// Every package keeps the repository it came from.
	if pkgs[0].RepoID != "repo.test/debian-security" || pkgs[0].RepoURL != testSecurityBase+"/" {
		t.Errorf("newest nginx comes from %q/%q", pkgs[0].RepoID, pkgs[0].RepoURL)
	}
	if pkgs[1].RepoID != "repo.test/debian" || pkgs[1].RepoURL != testRepoBase+"/" {
		t.Errorf("nginx 1.24.0-1 comes from %q/%q", pkgs[1].RepoID, pkgs[1].RepoURL)
	}
	if !strings.HasPrefix(pkgs[0].DownloadURL, testSecurityBase+"/pool/") {
		t.Errorf("DownloadURL = %q", pkgs[0].DownloadURL)
	}

	// A package that only exists in one of the repositories is found as well.
	curl, err := FindPackages(ctx, testRepoBase, Query{Name: "curl"}, opts...)
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(curl) != 1 || curl[0].Version.String() != "8.5.0-2" || curl[0].RepoID != "repo.test/debian-security" {
		t.Errorf("curl = %+v", curl)
	}

	// Each repository is read with its own metadata, so the architecture-independent index of the
	// main repository is still scanned.
	all, err := FindPackages(ctx, testRepoBase, Query{Name: "ca-certificates"}, opts...)
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(all) != 1 || all[0].RepoID != "repo.test/debian" {
		t.Errorf("ca-certificates = %+v", all)
	}
}

// TestFindPackagesMergedQuery verifies that Latest, Limit, and the sort order apply to the merged
// result rather than to every repository on its own.
func TestFindPackagesMergedQuery(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepositories(t)

	latest, err := FindPackages(ctx, testRepoBase, Query{Name: "nginx", Latest: true},
		testOptions(fetcher, WithRepositories(testSecurityBase))...)
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(latest) != 1 || latest[0].Version.String() != "1.26.0-1" {
		t.Fatalf("Latest across repositories = %+v", latest)
	}

	limited, err := FindPackages(ctx, testRepoBase, Query{Limit: 2},
		testOptions(fetcher, WithRepositories(testSecurityBase))...)
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("Limit = 2 returned %d packages, want 2", len(limited))
	}
	if !(limited[0].Name <= limited[1].Name) {
		t.Errorf("the limited result is not sorted: %q, %q", limited[0].Name, limited[1].Name)
	}

	oldest, err := FindPackages(ctx, testRepoBase, Query{Name: "nginx", Sort: SortVersionAsc},
		testOptions(fetcher, WithRepositories(testSecurityBase))...)
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(oldest) != 3 || oldest[0].Version.String() != "1.22.1-9" {
		t.Errorf("SortVersionAsc across repositories = %+v", oldest)
	}
}

func TestFindPackageAcrossRepositories(t *testing.T) {
	ctx := context.Background()
	latest, err := FindPackage(ctx, testRepoBase, "nginx",
		testOptions(testRepositories(t), WithRepositories(testSecurityBase))...)
	if err != nil {
		t.Fatalf("FindPackage failed: %v", err)
	}
	if latest.Version.String() != "1.26.0-1" || latest.RepoID != "repo.test/debian-security" {
		t.Errorf("FindPackage = %s (%s)", latest.Version.String(), latest.RepoID)
	}
}

func TestListPackagesAcrossRepositories(t *testing.T) {
	ctx := context.Background()
	pkgs, err := ListPackages(ctx, testRepoBase, "curl",
		testOptions(testRepositories(t), WithRepositories(testSecurityBase))...)
	if err != nil {
		t.Fatalf("ListPackages failed: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0].Name != "curl" {
		t.Errorf("ListPackages = %+v", pkgs)
	}
}

func TestFindSourcesAcrossRepositories(t *testing.T) {
	ctx := context.Background()
	sources, err := FindSources(ctx, testRepoBase, SourceQuery{Name: "nginx", Latest: true},
		testOptions(testRepositories(t), WithRepositories(testSecurityBase))...)
	if err != nil {
		t.Fatalf("FindSources failed: %v", err)
	}
	if len(sources) != 1 || sources[0].Version.String() != "1.26.0-1" {
		t.Fatalf("FindSources = %+v", sources)
	}
	if sources[0].RepoID != "repo.test/debian-security" {
		t.Errorf("the newest source comes from %q", sources[0].RepoID)
	}

	listed, err := ListSources(ctx, testRepoBase, "nginx",
		testOptions(testRepositories(t), WithRepositories(testSecurityBase))...)
	if err != nil {
		t.Fatalf("ListSources failed: %v", err)
	}
	if len(listed) != 2 {
		t.Errorf("ListSources returned %d sources, want 2: %+v", len(listed), listed)
	}
}

// TestRepositoriesAddresses covers how the address list is built: the address of the call comes
// TestFindPackagesWithDifferentSuites reads repositories that use different suites (for example the
// Debian security archive, whose suite is bookworm-security). WithSuite applies to every address, so
// the suite is given in each address instead.
func TestFindPackagesWithDifferentSuites(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepository(t)
	putTestRepository(t, fetcher, "/debian-security", "bookworm-security", map[string]string{
		"main/binary-amd64/Packages.xz": testSecurityPackages,
		"main/binary-all/Packages.gz":   "",
	})

	const (
		mainSuiteURL     = testRepoBase + "/dists/bookworm"
		securitySuiteURL = testSecurityBase + "/dists/bookworm-security"
	)
	pkgs, err := FindPackages(ctx, mainSuiteURL, Query{Name: "nginx", Latest: true},
		WithFetcher(fetcher), WithArchitecture("amd64"), WithRepositories(securitySuiteURL))
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(pkgs) != 1 || pkgs[0].Version.String() != "1.26.0-1" {
		t.Fatalf("the newest package across both suites = %+v", pkgs)
	}
	if pkgs[0].Suite != "bookworm-security" || pkgs[0].RepoID != "repo.test/debian-security" {
		t.Errorf("the package comes from suite %q of %q", pkgs[0].Suite, pkgs[0].RepoID)
	}
}

// TestRepositoriesAddresses covers how the address list is built: the address of the call comes
// first, the address of the call may be empty, blanks are ignored, and repeated addresses are read
// once.
func TestRepositoriesAddresses(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepositories(t)

	// The same repository passed twice is only read once.
	opts := testOptions(fetcher, WithRepositories(testSecurityBase, testRepoBase, " "))
	pkgs, err := FindPackages(ctx, testRepoBase, Query{Name: "nginx"}, opts...)
	if err != nil {
		t.Fatalf("FindPackages failed: %v", err)
	}
	if len(pkgs) != 3 {
		t.Fatalf("a repeated address should not duplicate packages: %+v", pkgs)
	}
	indexRequests := 0
	for _, requested := range fetcher.requested() {
		if strings.Contains(requested, "/dists/bookworm/main/binary-amd64/Packages") {
			indexRequests++
		}
	}
	if indexRequests != 2 {
		t.Errorf("the amd64 Packages index was read %d times, want 2 (once per repository)", indexRequests)
	}

	// An empty address is allowed when the option supplies every repository.
	pkgs, err = FindPackages(ctx, "", Query{Name: "nginx"},
		testOptions(fetcher, WithRepositories(testRepoBase, testSecurityBase))...)
	if err != nil {
		t.Fatalf("FindPackages with an empty address failed: %v", err)
	}
	if len(pkgs) != 3 {
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
	if _, err := FindSources(ctx, "", SourceQuery{Name: "nginx"}, testOptions(fetcher)...); !errors.Is(err, ErrNoRepository) {
		t.Errorf("FindSources error is %v, want ErrNoRepository", err)
	}
}

// TestFindPackagesRepositoryError verifies that a repository that cannot be read fails the whole
// query and that the error keeps the address and the underlying reason.
func TestFindPackagesRepositoryError(t *testing.T) {
	ctx := context.Background()
	fetcher := testRepositories(t)
	const missing = "https://repo.test/missing"

	_, err := FindPackages(ctx, testRepoBase, Query{Name: "nginx"},
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
	_, err := Open(context.Background(), testRepoBase,
		testOptions(fetcher, WithRepositories(testSecurityBase))...)
	if !errors.Is(err, ErrMultipleRepositories) {
		t.Errorf("error is %v, want ErrMultipleRepositories", err)
	}
	// A client without further addresses keeps working.
	repo, err := Open(context.Background(), testRepoBase, testOptions(fetcher)...)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	if repo.ID != "repo.test/debian" {
		t.Errorf("ID = %q", repo.ID)
	}
}
