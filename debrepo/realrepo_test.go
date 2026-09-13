package debrepo

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// dockerRepoBase returns the address of Docker's deb repository.
//
// The address can be overridden with an environment variable; local development in China can point it
// at a mirror:
//
//	PKGREPO_DOCKER_BASE=https://mirrors.aliyun.com/docker-ce go test ./...
func dockerRepoBase() string {
	if base := strings.TrimSpace(os.Getenv("PKGREPO_DOCKER_BASE")); base != "" {
		return strings.TrimSuffix(base, "/")
	}
	return "https://download.docker.com"
}

// realRepo describes a real deb repository used for end-to-end verification.
type realRepo struct {
	name string
	// distro is the distribution directory name (debian, ubuntu).
	distro string
	// url is the distribution root address, for example https://download.docker.com/linux/debian.
	url         string
	suite       string
	component   string
	arch        string
	distTag     string
	minPackages int
}

// realRepos returns the real repositories used for end-to-end verification: the official Docker
// Debian and Ubuntu repositories. They cover both the Debian and Ubuntu distributions and four
// suites, and all of them use the 5: epoch.
func realRepos() []realRepo {
	base := dockerRepoBase()
	repo := func(name, distro, suite, distTag string) realRepo {
		return realRepo{
			name:        name,
			distro:      distro,
			url:         base + "/linux/" + distro,
			suite:       suite,
			component:   "stable",
			arch:        "amd64",
			distTag:     distTag,
			minPackages: 100,
		}
	}
	return []realRepo{
		repo("debian-trixie", "debian", "trixie", "debian.13"),
		repo("debian-bookworm", "debian", "bookworm", "debian.12"),
		repo("debian-bullseye", "debian", "bullseye", "debian.11"),
		repo("ubuntu-resolute", "ubuntu", "resolute", "ubuntu.26.04"),
		repo("ubuntu-noble", "ubuntu", "noble", "ubuntu.24.04"),
		repo("ubuntu-jammy", "ubuntu", "jammy", "ubuntu.22.04"),
	}
}

// realRepoPackage is the software name used by the end-to-end tests.
const realRepoPackage = "docker-ce"

// networkUnavailable records whether this test run has already determined that the network is
// unreachable, which keeps every subtest in an offline environment from waiting for DNS timeouts
// again.
var networkUnavailable atomic.Bool

// realRepoClient returns the HTTP client used by the tests (IPv4 is used unconditionally, which makes
// the connection more stable).
func realRepoClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, "tcp4", addr)
			},
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   15 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
}

// requireNetwork skips immediately when this run has already determined that the network is
// unavailable.
func requireNetwork(t *testing.T, repoURL string) {
	t.Helper()
	if networkUnavailable.Load() {
		t.Skipf("skipping: this run determined that the network is unavailable, cannot access %s", repoURL)
	}
}

// isOfflineError reports whether the error means "the current environment cannot reach the internet"
// (DNS resolution failure, unreachable network, and so on).
func isOfflineError(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		message := urlErr.Err.Error()
		for _, marker := range []string{"no such host", "network is unreachable", "name resolution"} {
			if strings.Contains(message, marker) {
				return true
			}
		}
	}
	return false
}

// callWithRetry retries on network flakiness (at most 6 times with growing backoff) and returns the
// last result. A genuine response from the repository such as a 404 is not retried and is returned
// directly.
func callWithRetry[T any](t *testing.T, repoURL string, action func() (T, error)) (T, error) {
	t.Helper()
	var zero T
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		zero, err = action()
		if err == nil {
			return zero, nil
		}
		if isOfflineError(err) {
			networkUnavailable.Store(true)
			t.Logf("accessing %s failed due to network errors: %v", repoURL, err)
			t.Skipf("skipping: the network is unavailable, cannot access the real repository %s (%v)", repoURL, err)
		}
		var httpErr *HTTPError
		if errors.As(err, &httpErr) {
			return zero, err
		}
		if attempt < 5 {
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
		}
	}
	return zero, err
}

// cachingFetcher caches downloaded files in memory, which keeps the same index from being downloaded
// repeatedly. The index of a real repository can be tens of MB and the tests scan it several times,
// so the cache minimizes network cost.
type cachingFetcher struct {
	base Fetcher

	mu    sync.Mutex
	files map[string][]byte
}

func newCachingFetcher(base Fetcher) *cachingFetcher {
	return &cachingFetcher{base: base, files: map[string][]byte{}}
}

func (f *cachingFetcher) Open(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	f.mu.Lock()
	data, ok := f.files[rawURL]
	f.mu.Unlock()
	if ok {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	body, err := f.base.Open(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err = io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.files[rawURL] = data
	f.mu.Unlock()
	return io.NopCloser(bytes.NewReader(data)), nil
}

// repoCache caches opened repositories, which keeps every subtest from downloading the same index
// again.
type repoCache struct {
	mu      sync.Mutex
	entries map[string]*Repository
}

var openRealRepos = &repoCache{entries: map[string]*Repository{}}

func (c *repoCache) load(key string, open func() (*Repository, error)) (*Repository, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if repo, ok := c.entries[key]; ok {
		return repo, nil
	}
	repo, err := open()
	if err != nil {
		return nil, err
	}
	c.entries[key] = repo
	return repo, nil
}

// openRealRepo opens a real repository (with checksum verification enabled) and reuses it within this
// test run.
func openRealRepo(t *testing.T, repo realRepo) *Repository {
	t.Helper()
	requireNetwork(t, repo.url)
	key := repo.name + "|" + repo.suite + "|" + repo.arch
	opened, err := openRealRepos.load(key, func() (*Repository, error) {
		fetcher := newCachingFetcher(&HTTPFetcher{Client: realRepoClient(4 * time.Minute)})
		client := New(
			WithFetcher(fetcher),
			WithSuite(repo.suite),
			WithComponent(repo.component),
			WithArchitecture(repo.arch),
			WithChecksumVerification(true),
		)
		return callWithRetry(t, repo.url, func() (*Repository, error) {
			return client.Open(context.Background(), repo.url)
		})
	})
	if err != nil {
		if isOfflineError(err) {
			networkUnavailable.Store(true)
			t.Skipf("skipping: the network is unavailable, cannot access the real repository %s (%v)", repo.url, err)
		}
		t.Fatalf("opening the real repository %s failed: %v", repo.url, err)
	}
	return opened
}

func TestRealReposReleaseMetadata(t *testing.T) {
	for _, repo := range realRepos() {
		repo := repo
		t.Run(repo.name, func(t *testing.T) {
			opened := openRealRepo(t, repo)
			release := opened.Release
			if release == nil {
				t.Fatal("Release is empty")
			}
			if release.Origin == "" || release.Label == "" {
				t.Errorf("Origin/Label = %q/%q", release.Origin, release.Label)
			}
			if release.Suite != repo.suite {
				t.Errorf("Suite = %q, want %q", release.Suite, repo.suite)
			}
			if release.Date.IsZero() {
				t.Error("Date should be parsed")
			}
			if !containsString(release.Components, repo.component) {
				t.Errorf("Components = %v, want it to contain %q", release.Components, repo.component)
			}
			if !containsString(release.Architectures, repo.arch) {
				t.Errorf("Architectures = %v, want it to contain %q", release.Architectures, repo.arch)
			}
			if !strings.HasPrefix(opened.ReleaseURL, repo.url+"/dists/"+repo.suite+"/") {
				t.Errorf("ReleaseURL = %q", opened.ReleaseURL)
			}
			t.Logf("%s: %s (InRelease: %t), Architectures=%v Components=%v",
				repo.name, opened.ReleaseURL, opened.InRelease, release.Architectures, release.Components)
		})
	}
}

func TestRealReposIndexes(t *testing.T) {
	for _, repo := range realRepos() {
		repo := repo
		t.Run(repo.name, func(t *testing.T) {
			opened := openRealRepo(t, repo)
			indexes := opened.Indexes()
			if len(indexes) == 0 {
				t.Fatal("no Packages index was found")
			}
			index := indexes[0]
			if index.Component != repo.component || index.Architecture != repo.arch {
				t.Errorf("index = %+v", index)
			}
			if !index.FromRelease || !strings.Contains(index.Path, "/Packages") {
				t.Errorf("index path = %q", index.Path)
			}
			if index.Size <= 0 || index.Checksum.Value == "" {
				t.Errorf("the index is missing its size or checksum: %+v", index)
			}
			releasePath := strings.TrimPrefix(index.Path, "dists/"+opened.Suite+"/")
			entry, ok := opened.Release.Lookup(releasePath)
			if !ok {
				t.Fatalf("index %q was not found in the Release file", releasePath)
			}
			if entry.Size != index.Size || !entry.Checksum.Equal(index.Checksum) {
				t.Errorf("index information in the Release file = %+v, index = %+v", entry, index)
			}
			switch entry.Checksum.Type {
			case "sha512", "sha256":
			default:
				t.Errorf("index checksum algorithm = %q", entry.Checksum.Type)
			}
			t.Logf("%s: %s (%s, %d bytes, %d candidates)", repo.name, index.Path,
				CompressionOf(index.Path), index.Size, len(index.Candidates))
		})
	}
}

func TestRealReposListPackages(t *testing.T) {
	ctx := context.Background()
	for _, repo := range realRepos() {
		repo := repo
		t.Run(repo.name, func(t *testing.T) {
			opened := openRealRepo(t, repo)
			pkgs, err := opened.FindPackages(ctx, Query{Name: realRepoPackage})
			if err != nil {
				t.Fatalf("querying %s failed: %v", realRepoPackage, err)
			}
			if len(pkgs) < 5 {
				t.Fatalf("only %d versions of %s were found, want at least 5", len(pkgs), realRepoPackage)
			}
			// Default sort: name ascending and version descending, so the newest version comes
			// first.
			for i := 1; i < len(pkgs); i++ {
				if CompareVersions(pkgs[i-1].Version.String(), pkgs[i].Version.String()) < 0 {
					t.Errorf("versions are not sorted in descending order: %s < %s",
						pkgs[i-1].Version.String(), pkgs[i].Version.String())
				}
			}
			latest := pkgs[0]
			if latest.Version.Epoch != 5 {
				t.Errorf("the epoch of the newest version %s = %d, want 5", latest.Version.String(), latest.Version.Epoch)
			}
			if !strings.Contains(latest.Version.String(), repo.distTag) {
				t.Errorf("the newest version %s does not contain the distribution tag %q", latest.Version.String(), repo.distTag)
			}
			if latest.Component != repo.component || latest.Suite != repo.suite {
				t.Errorf("package context = %s/%s, want %s/%s",
					latest.Suite, latest.Component, repo.suite, repo.component)
			}
			if !strings.HasPrefix(latest.DownloadURL, repo.url+"/dists/"+repo.suite+"/pool/") ||
				!strings.HasSuffix(latest.DownloadURL, ".deb") {
				t.Errorf("DownloadURL = %q", latest.DownloadURL)
			}
			if latest.Size.File <= 0 || latest.Size.Installed <= 0 {
				t.Errorf("unexpected size: %+v", latest.Size)
			}
			if checksum, ok := latest.Checksum(); !ok ||
				(checksum.Type != "sha256" && checksum.Type != "sha512") {
				t.Errorf("checksum = %+v, %t", checksum, ok)
			}
			if latest.Section == "" || latest.Priority == "" || latest.Description.Synopsis == "" {
				t.Errorf("description information is missing: %+v", latest)
			}
			if !latest.DependsOn("containerd.io") || !latest.DependsOn("libc6") {
				t.Errorf("unexpected dependency parse result for %s: %v", realRepoPackage, latest.Depends)
			}
			found, err := opened.FindPackage(ctx, Query{Name: realRepoPackage})
			if err != nil {
				t.Fatalf("FindPackage failed: %v", err)
			}
			if found.Version.String() != latest.Version.String() {
				t.Errorf("FindPackage = %s, want %s", found.Version.String(), latest.Version.String())
			}
			t.Logf("%s: %d versions, newest %s (%d bytes)", repo.name, len(pkgs),
				latest.Version.String(), latest.Size.File)
		})
	}
}

func TestRealReposPackageCount(t *testing.T) {
	for _, repo := range realRepos() {
		repo := repo
		t.Run(repo.name, func(t *testing.T) {
			opened := openRealRepo(t, repo)
			index := opened.Indexes()[0]
			// Read only the preferred candidate, which avoids downloading unused compression
			// formats.
			index.Candidates = index.Candidates[:1]
			body, err := opened.openIndex(context.Background(), index)
			if err != nil {
				t.Fatalf("reading the index failed: %v", err)
			}
			defer body.Close()
			total := 0
			if err := ParsePackages(body, func(*DebPackage) error {
				total++
				return nil
			}); err != nil {
				t.Fatalf("parsing the index failed: %v", err)
			}
			if total < repo.minPackages {
				t.Errorf("package count in the index = %d, want at least %d", total, repo.minPackages)
			}
		})
	}
}

// TestRealReposDownloadReachable checks that the download address given by the index really works.
func TestRealReposDownloadReachable(t *testing.T) {
	ctx := context.Background()
	for _, repo := range realRepos() {
		repo := repo
		t.Run(repo.name, func(t *testing.T) {
			opened := openRealRepo(t, repo)
			pkg, err := opened.FindPackage(ctx, Query{Name: realRepoPackage})
			if err != nil {
				t.Fatalf("looking up %s failed: %v", realRepoPackage, err)
			}
			// Mirrors occasionally reset connections, so retry a few times before deciding.
			size, err := callWithRetry(t, pkg.DownloadURL, func() (int64, error) {
				client := realRepoClient(2 * time.Minute)
				req, err := http.NewRequestWithContext(ctx, http.MethodHead, pkg.DownloadURL, nil)
				if err != nil {
					return 0, err
				}
				req.Header.Set("User-Agent", defaultUserAgent)
				resp, err := client.Do(req)
				if err != nil {
					return 0, err
				}
				defer resp.Body.Close()
				if resp.StatusCode < 200 || resp.StatusCode > 299 {
					return 0, fmt.Errorf("HEAD %s = %s", pkg.DownloadURL, resp.Status)
				}
				return resp.ContentLength, nil
			})
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			if size > 0 && size != pkg.Size.File {
				t.Errorf("the remote size %d does not match the %d in the index", size, pkg.Size.File)
			}
		})
	}
}

// TestRealReposPackageChecksum downloads a complete .deb and checks its checksum (skipped by default
// because it is slow).
func TestRealReposPackageChecksum(t *testing.T) {
	if os.Getenv("PKGREPO_FULL_DOWNLOAD") == "" {
		t.Skip("skipping the full download test by default; set PKGREPO_FULL_DOWNLOAD=1 to enable it")
	}
	ctx := context.Background()
	for _, repo := range realRepos() {
		repo := repo
		t.Run(repo.name, func(t *testing.T) {
			opened := openRealRepo(t, repo)
			pkg, err := opened.FindPackage(ctx, Query{Name: realRepoPackage})
			if err != nil {
				t.Fatalf("looking up %s failed: %v", realRepoPackage, err)
			}
			expected, ok := pkg.Checksums.Strongest()
			if !ok {
				t.Fatalf("the index has no checksum: %+v", pkg.Checksums)
			}
			client := realRepoClient(10 * time.Minute)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, pkg.DownloadURL, nil)
			if err != nil {
				t.Fatalf("building the request failed: %v", err)
			}
			req.Header.Set("User-Agent", defaultUserAgent)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("download failed: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s = %s", pkg.DownloadURL, resp.Status)
			}
			hasher, err := newHash(expected.Type)
			if err != nil {
				t.Fatalf("unsupported algorithm %q: %v", expected.Type, err)
			}
			size, err := io.Copy(hasher, resp.Body)
			if err != nil {
				t.Fatalf("reading the response failed: %v", err)
			}
			if size != pkg.Size.File {
				t.Errorf("download size = %d, want %d", size, pkg.Size.File)
			}
			actual := hex.EncodeToString(hasher.Sum(nil))
			if !strings.EqualFold(actual, expected.Value) {
				t.Errorf("checksum mismatch: want %s, got %s", expected.Value, actual)
			}
		})
	}
}

// TestUpstreamDebianRepository verifies the capabilities specific to the official Debian repository:
// the binary-all index (since Debian 13 architecture-independent packages only appear there) and the
// Sources index.
//
// This test is skipped by default (the official repository is slow in China); set a mirror address to
// enable it:
//
//	PKGREPO_DEBIAN_BASE=https://deb.debian.org/debian go test ./debrepo/ -run Upstream -v
func TestUpstreamDebianRepository(t *testing.T) {
	base := strings.TrimSuffix(strings.TrimSpace(os.Getenv("PKGREPO_DEBIAN_BASE")), "/")
	if base == "" {
		t.Skip("skipping the upstream repository test by default; set PKGREPO_DEBIAN_BASE to enable it (for example https://deb.debian.org/debian)")
	}
	fetcher := newCachingFetcher(&HTTPFetcher{Client: realRepoClient(4 * time.Minute)})
	client := New(
		WithFetcher(fetcher),
		WithSuite("stable"),
		WithComponent("main"),
		WithArchitecture("amd64"),
		WithChecksumVerification(true),
	)
	ctx := context.Background()
	repo, err := callWithRetry(t, base, func() (*Repository, error) {
		return client.Open(ctx, base)
	})
	if err != nil {
		if isOfflineError(err) {
			networkUnavailable.Store(true)
			t.Skipf("skipping: the network is unavailable (%v)", err)
		}
		t.Fatalf("opening the Debian repository failed: %v", err)
	}
	if len(repo.SourceIndexes()) == 0 {
		t.Error("the official Debian repository should provide a Sources index")
	}
	// binary-all: architecture-independent packages (Architecture: all).
	pkg, err := repo.FindPackage(ctx, Query{Name: "ca-certificates"})
	if err != nil {
		t.Fatalf("looking up ca-certificates failed: %v", err)
	}
	if pkg.Architecture != "all" {
		t.Errorf("Architecture = %q, want all", pkg.Architecture)
	}
	// Sources: source packages and .dsc files.
	source, err := repo.FindSource(ctx, SourceQuery{Name: "nginx"})
	if err != nil {
		t.Fatalf("looking up the source package failed: %v", err)
	}
	dsc, ok := source.DSC()
	if !ok {
		t.Fatal("the source package has no .dsc file")
	}
	dscURL, err := source.FileURL(dsc.Path)
	if err != nil {
		t.Fatalf("FileURL failed: %v", err)
	}
	if !strings.HasPrefix(dscURL, base+"/pool/") || !strings.HasSuffix(dscURL, ".dsc") {
		t.Errorf("dsc address = %q", dscURL)
	}
	t.Logf("Debian %s: ca-certificates %s (all), source package nginx %s",
		repo.Suite, pkg.Version.String(), source.Version.String())
}
