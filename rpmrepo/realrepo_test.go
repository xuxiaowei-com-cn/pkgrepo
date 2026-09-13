package rpmrepo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// realRepo describes a real rpm repository used for end-to-end verification.
// These addresses come from the official Docker CentOS repositories and cover both the gzip and
// zstd primary metadata compression formats.
type realRepo struct {
	name               string
	url                string
	distTag            string
	primaryCompression string
	minPackages        int
	wantRequires       []string
}

// dockerRepoBase is the address of the official Docker repository.
//
// Behind a restricted network or in China, an environment variable can point at a mirror serving the
// same repository data, for example:
//
//	PKGREPO_DOCKER_BASE=https://mirrors.aliyun.com/docker-ce go test ./...
func dockerRepoBase() string {
	if base := strings.TrimSpace(os.Getenv("PKGREPO_DOCKER_BASE")); base != "" {
		return strings.TrimSuffix(base, "/")
	}
	return "https://download.docker.com"
}

// realRepos returns the real repositories used for end-to-end verification.
func realRepos() []realRepo {
	base := dockerRepoBase()
	repo := func(name, dist, compression string, minPackages int, requires ...string) realRepo {
		return realRepo{
			name:               name,
			url:                base + "/linux/centos/" + strings.TrimPrefix(name, "centos") + "/x86_64/stable/",
			distTag:            dist,
			primaryCompression: compression,
			minPackages:        minPackages,
			wantRequires:       requires,
		}
	}
	return []realRepo{
		repo("centos7", "el7", "gzip", 50, "containerd.io", "systemd", "libc.so.6(GLIBC_2.14)(64bit)"),
		repo("centos8", "el8", "gzip", 30, "containerd.io", "systemd", "libc.so.6(GLIBC_2.14)(64bit)"),
		repo("centos9", "el9", "zstd", 50,
			"containerd.io", "nftables", "(iptables-nft or iptables)", "libc.so.6(GLIBC_2.34)(64bit)"),
		repo("centos10", "el10", "zstd", 30,
			"containerd.io", "nftables", "(iptables-nft or iptables)", "libc.so.6(GLIBC_2.34)(64bit)"),
	}
}

// realRepoPackage is the software name used by the tests.
const realRepoPackage = "docker-ce"

// Network helpers
//
// networkUnavailable records whether this test run has already determined that the network is
// unreachable, which keeps every subtest in an offline environment from waiting for DNS timeouts
// again.
var networkUnavailable atomic.Bool

// realRepoHTTPClient returns the HTTP client used by the tests.
//
// IPv4 is used unconditionally here: on some networks (for example this machine) the IPv6 path to a
// mirror randomly resets connections, which causes failures unrelated to the SDK. The SDK itself
// does not restrict the address family; the test only makes the connection more stable.
func realRepoHTTPClient(timeout time.Duration) *http.Client {
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

// isNetworkError reports whether the error comes from the network layer (offline, DNS failure,
// connection reset, and so on).
func isNetworkError(err error) bool {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return false
	}
	var netErr net.Error
	return errors.As(urlErr.Err, &netErr)
}

// isOfflineError reports whether the error means "the current environment cannot reach the
// internet" (DNS resolution failure, unreachable network, and so on). Flakiness such as a reset
// connection does not count as offline and is left to the retry logic.
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
	var (
		value T
		err   error
	)
	attempts := 6
	if networkUnavailable.Load() {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		value, err = action()
		if err == nil || !isNetworkError(err) {
			return value, err
		}
		if attempt < attempts {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
	}
	t.Logf("accessing %s failed %d times in a row due to network errors: %v", repoURL, attempts, err)
	return value, err
}

// skipIfNetworkUnavailable only skips the test when the network cannot be reached (offline
// environment, DNS unavailable, flaky link, and so on); a genuine response from the repository such
// as a 404 still counts as a failure so that regressions are not hidden.
func skipIfNetworkUnavailable(t *testing.T, repoURL string, err error) {
	t.Helper()
	if isNetworkError(err) {
		if isOfflineError(err) {
			networkUnavailable.Store(true)
		}
		t.Skipf("skipping: the network is unavailable, cannot access the real repository %s (%v)", repoURL, err)
	}
}

// Real metadata cache
//
// realRepoData holds metadata downloaded from a real repository. Each repository is downloaded only
// once per test run and later tests reuse the same bytes, which keeps the data real while avoiding
// repeated requests that would trigger mirror rate limiting.
type realRepoData struct {
	repo       realRepo
	repomdURL  string
	primaryURL string
	// repomd holds the raw bytes of repodata/repomd.xml.
	repomd []byte
	// primary holds the raw bytes of the primary metadata (still compressed).
	primary []byte
	// fetchErr records the error from downloading the metadata (nil on success), which keeps every
	// test from retrying again.
	fetchErr error
}

var (
	realRepoDataMu  sync.Mutex
	realRepoDataSet = make(map[string]*realRepoData)
)

// loadRealRepoData returns the metadata of a real repository; each repository is downloaded only once
// per run.
func loadRealRepoData(t *testing.T, repo realRepo) *realRepoData {
	t.Helper()
	realRepoDataMu.Lock()
	defer realRepoDataMu.Unlock()
	if data, ok := realRepoDataSet[repo.url]; ok {
		if data.fetchErr != nil {
			skipIfNetworkUnavailable(t, repo.url, data.fetchErr)
			t.Fatalf("reading the real repository %s failed: %v", repo.url, data.fetchErr)
		}
		return data
	}
	requireNetwork(t, repo.url)

	ctx := context.Background()
	httpClient := realRepoHTTPClient(2 * time.Minute)
	base := strings.TrimSuffix(repo.url, "/")
	data := &realRepoData{
		repo:      repo,
		repomdURL: base + "/repodata/repomd.xml",
	}

	repomd, err := callWithRetry(t, data.repomdURL, func() ([]byte, error) {
		return httpGetBytes(ctx, httpClient, data.repomdURL)
	})
	if err != nil {
		data.fetchErr = err
		realRepoDataSet[repo.url] = data
		skipIfNetworkUnavailable(t, data.repomdURL, err)
		t.Fatalf("reading %s failed: %v", data.repomdURL, err)
	}
	data.repomd = repomd

	parsed, err := ParseRepoMD(bytes.NewReader(repomd))
	if err != nil {
		data.fetchErr = err
		realRepoDataSet[repo.url] = data
		t.Fatalf("parsing %s failed: %v", data.repomdURL, err)
	}
	primary, err := parsed.Primary()
	if err != nil {
		data.fetchErr = err
		realRepoDataSet[repo.url] = data
		t.Fatalf("%s is missing the primary metadata: %v", data.repomdURL, err)
	}
	data.primaryURL = base + "/" + primary.Location.Href

	payload, err := callWithRetry(t, data.primaryURL, func() ([]byte, error) {
		return httpGetBytes(ctx, httpClient, data.primaryURL)
	})
	if err != nil {
		data.fetchErr = err
		realRepoDataSet[repo.url] = data
		skipIfNetworkUnavailable(t, data.primaryURL, err)
		t.Fatalf("reading %s failed: %v", data.primaryURL, err)
	}
	if int64(len(payload)) != primary.Size {
		t.Errorf("the actual size %d of %s does not match the %d recorded in repomd.xml",
			len(payload), data.primaryURL, primary.Size)
	}
	data.primary = payload

	t.Logf("%s: downloaded real metadata repomd.xml=%d bytes primary=%d bytes (%s)",
		repo.name, len(data.repomd), len(data.primary), CompressionOf(primary.Location.Href))
	realRepoDataSet[repo.url] = data
	return data
}

func httpGetBytes(ctx context.Context, httpClient *http.Client, rawURL string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rpmrepo: request %s returned %s", rawURL, response.Status)
	}
	return io.ReadAll(response.Body)
}

// fixtureFetcher answers the SDK's requests with already downloaded real metadata, which keeps the
// tests from hitting the network again.
type fixtureFetcher struct {
	data *realRepoData
	// tamper rewrites repomd.xml to verify the error branches.
	tamper func([]byte) []byte
}

func (f *fixtureFetcher) Open(_ context.Context, rawURL string) (io.ReadCloser, error) {
	switch rawURL {
	case f.data.repomdURL:
		body := f.data.repomd
		if f.tamper != nil {
			body = f.tamper(body)
		}
		return io.NopCloser(bytes.NewReader(body)), nil
	case f.data.primaryURL:
		return io.NopCloser(bytes.NewReader(f.data.primary)), nil
	default:
		return nil, fmt.Errorf("test stub: address %s is not cached", rawURL)
	}
}

// openRealRepo opens a repository with real metadata (with checksum verification enabled).
func openRealRepo(t *testing.T, repo realRepo) *Repository {
	t.Helper()
	data := loadRealRepoData(t, repo)
	repository, err := New(WithFetcher(&fixtureFetcher{data: data}), WithChecksumVerification(true)).
		Open(context.Background(), repo.url)
	if err != nil {
		t.Fatalf("opening the real repository %s failed: %v", repo.url, err)
	}
	return repository
}

// findRealPackages queries packages against real metadata.
func findRealPackages(t *testing.T, repository *Repository, query Query) []Package {
	t.Helper()
	pkgs, err := repository.FindPackages(context.Background(), query)
	if err != nil {
		t.Fatalf("querying %s failed: %v", repository.URL, err)
	}
	return pkgs
}

// TestRealReposListPackages verifies the core feature against real repositories: repository address
// + software name -> package list.
func TestRealReposListPackages(t *testing.T) {
	for _, repo := range realRepos() {
		t.Run(repo.name, func(t *testing.T) {
			repository := openRealRepo(t, repo)
			pkgs := findRealPackages(t, repository, Query{Name: realRepoPackage, Arch: []string{"x86_64"}})
			if len(pkgs) < repo.minPackages {
				t.Fatalf("the docker-ce package count is %d, fewer than the expected %d; the repository metadata may be unusual", len(pkgs), repo.minPackages)
			}

			// The default sort is version descending, so the first entry is the newest version.
			latest := pkgs[0]
			for i := range pkgs {
				pkg := &pkgs[i]
				if pkg.Version.Compare(latest.Version) > 0 {
					t.Fatalf("wrong sort result: %s is newer than %s", pkg.NEVRA(), latest.NEVRA())
				}
				if pkg.Name != realRepoPackage || pkg.Arch != "x86_64" {
					t.Fatalf("the query result contains another package: %s", pkg.NEVRA())
				}
				if pkg.Checksum.Type != "sha256" || len(pkg.Checksum.Value) != sha256.Size*2 {
					t.Fatalf("unexpected checksum for package %s: %+v", pkg.NEVRA(), pkg.Checksum)
				}
				if pkg.Size.Package <= 0 || pkg.Size.Installed <= 0 {
					t.Fatalf("unexpected size for package %s: %+v", pkg.NEVRA(), pkg.Size)
				}
				if !strings.HasPrefix(pkg.DownloadURL, repo.url) ||
					!strings.HasSuffix(pkg.DownloadURL, pkg.Filename()) {
					t.Fatalf("unexpected download address of package %s: %q", pkg.NEVRA(), pkg.DownloadURL)
				}
				if want := pkg.Name + "-" + pkg.Version.String() + "." + pkg.Arch; pkg.NEVRA() != want {
					t.Fatalf("NEVRA is %q, want %q", pkg.NEVRA(), want)
				}
				if pkg.RepoID == "" || pkg.RepoURL == "" {
					t.Fatalf("package %s is missing repository information", pkg.NEVRA())
				}
				if len(pkg.Format.Requires) == 0 {
					t.Fatalf("no dependency was parsed for package %s", pkg.NEVRA())
				}
			}

			if !strings.HasSuffix(latest.Version.Release, repo.distTag) {
				t.Errorf("the release of the newest version %s does not contain %s", latest.NEVRA(), repo.distTag)
			}
			for _, want := range repo.wantRequires {
				if !latest.Requires(want) {
					t.Errorf("the dependencies of %s are missing %s", latest.NEVRA(), want)
				}
			}
			if latest.Time.Build.IsZero() || latest.Time.File.IsZero() {
				t.Errorf("%s is missing its build/add time", latest.NEVRA())
			}
			if latest.Format.HeaderRange.End == 0 || latest.Format.SourceRPM == "" {
				t.Errorf("%s is missing rpm header information: %+v", latest.NEVRA(), latest.Format)
			}
			if !latest.Provides(realRepoPackage) {
				t.Errorf("%s does not provide the %s capability", latest.NEVRA(), realRepoPackage)
			}
			t.Logf("%s: %d docker-ce packages in total, newest %s, size %d bytes, checksum %s, %d dependencies, download address %s",
				repo.name, len(pkgs), latest.NEVRA(), latest.Size.Package, latest.Checksum,
				len(latest.Format.Requires), latest.DownloadURL)
		})
	}
}

// TestRealReposRepoMD verifies repository index parsing with a real repomd.xml.
func TestRealReposRepoMD(t *testing.T) {
	for _, repo := range realRepos() {
		t.Run(repo.name, func(t *testing.T) {
			repository := openRealRepo(t, repo)
			repomd := repository.RepoMD
			if repomd.Revision == "" {
				t.Fatal("repomd.xml is missing the revision")
			}
			if _, err := strconv.ParseInt(repomd.Revision, 10, 64); err != nil {
				t.Errorf("revision %q is not a Unix timestamp: %v", repomd.Revision, err)
			}
			if _, ok := repomd.DataByType(DataTypePrimary); !ok {
				t.Fatalf("repomd.xml is missing the primary metadata; the types are %v", repomd.Types())
			}

			primary := repository.Primary
			if primary.Location.Href == "" || primary.Checksum.Value == "" || primary.OpenChecksum.Value == "" {
				t.Fatalf("the primary entry is incomplete: %+v", primary)
			}
			if got := CompressionOf(primary.Location.Href); got != repo.primaryCompression {
				t.Errorf("the primary compression format is %q, want %q", got, repo.primaryCompression)
			}
			if primary.OpenSize <= primary.Size {
				t.Errorf("the decompressed primary size %d should not be smaller than the compressed size %d", primary.OpenSize, primary.Size)
			}
			if primary.Timestamp.IsZero() {
				t.Error("primary is missing its creation time")
			}

			primaryURL, err := repository.PrimaryURL()
			if err != nil {
				t.Fatalf("PrimaryURL failed: %v", err)
			}
			if !strings.HasPrefix(primaryURL, strings.TrimSuffix(repo.url, "/")+"/") {
				t.Errorf("the primary address %q is not below the repository address", primaryURL)
			}
			t.Logf("%s: revision=%s metadata types=%v primary=%s (%s) size=%d open-size=%d",
				repo.name, repomd.Revision, repomd.Types(), primary.Location.Href,
				CompressionOf(primary.Location.Href), primary.Size, primary.OpenSize)
		})
	}
}

// TestRealReposParsePrimaryStream streams a real primary.xml directly, covering a full metadata
// traversal.
func TestRealReposParsePrimaryStream(t *testing.T) {
	for _, repo := range realRepos() {
		t.Run(repo.name, func(t *testing.T) {
			data := loadRealRepoData(t, repo)
			reader, compressed, err := decompress(bytes.NewReader(data.primary))
			if err != nil {
				t.Fatalf("decompressing the primary metadata failed: %v", err)
			}
			defer reader.Close()
			if !compressed {
				t.Errorf("%s should be compressed data", data.primaryURL)
			}

			count, dependencies := 0, 0
			var sample Package
			err = ParsePrimary(reader, func(pkg *Package) error {
				count++
				if count == 1 {
					sample = *pkg
					dependencies = len(pkg.Format.Requires)
				}
				if pkg.Name == "" || pkg.Arch == "" || pkg.Version.Version == "" || pkg.Checksum.Value == "" {
					t.Fatalf("the parsed package has incomplete fields: %+v", pkg)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("ParsePrimary failed: %v", err)
			}
			if count < repo.minPackages {
				t.Fatalf("streamed %d packages, fewer than the expected %d", count, repo.minPackages)
			}
			if dependencies == 0 {
				t.Errorf("the first package %s has no dependencies", sample.NEVRA())
			}
			t.Logf("%s: streamed %d packages, the first is %s (%d dependencies)",
				repo.name, count, sample.NEVRA(), dependencies)
		})
	}
}

// TestRealReposDownloadURLIsReachable verifies that the download address given by the metadata really
// works.
func TestRealReposDownloadURLIsReachable(t *testing.T) {
	requireNetwork(t, realRepos()[0].url)
	httpClient := realRepoHTTPClient(2 * time.Minute)
	for _, repo := range realRepos() {
		t.Run(repo.name, func(t *testing.T) {
			ctx := context.Background()
			repository := openRealRepo(t, repo)
			latest, err := repository.FindPackage(ctx, Query{Name: realRepoPackage, Arch: []string{"x86_64"}})
			if err != nil {
				t.Fatalf("FindPackage failed: %v", err)
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, latest.DownloadURL, nil)
			if err != nil {
				t.Fatalf("building the request failed: %v", err)
			}
			// Fetch only the first two bytes to confirm the address works without downloading
			// the whole rpm.
			request.Header.Set("Range", "bytes=0-1")
			response, err := callWithRetry(t, latest.DownloadURL, func() (*http.Response, error) {
				return httpClient.Do(request)
			})
			if err != nil {
				skipIfNetworkUnavailable(t, latest.DownloadURL, err)
				t.Fatalf("downloading %s failed: %v", latest.DownloadURL, err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
				t.Fatalf("the download address %s returned %s", latest.DownloadURL, response.Status)
			}
			t.Logf("%s: %s is reachable (%s)", repo.name, latest.NEVRA(), response.Status)
		})
	}
}

// TestRealReposPackageChecksum downloads a real rpm package and checks it against the checksum in the
// metadata; it is skipped by default.
// How to run: PKGREPO_FULL_DOWNLOAD=1 go test ./rpmrepo/ -run TestRealReposPackageChecksum -v
func TestRealReposPackageChecksum(t *testing.T) {
	if os.Getenv("PKGREPO_FULL_DOWNLOAD") != "1" {
		t.Skip("skipping the full download by default; set PKGREPO_FULL_DOWNLOAD=1 to enable it")
	}
	requireNetwork(t, realRepos()[0].url)
	httpClient := realRepoHTTPClient(10 * time.Minute)
	for _, repo := range realRepos() {
		t.Run(repo.name, func(t *testing.T) {
			ctx := context.Background()
			repository := openRealRepo(t, repo)
			latest, err := repository.FindPackage(ctx, Query{Name: realRepoPackage, Arch: []string{"x86_64"}})
			if err != nil {
				t.Fatalf("FindPackage failed: %v", err)
			}

			type downloadResult struct {
				written int64
				sha256  string
			}
			result, err := callWithRetry(t, latest.DownloadURL, func() (downloadResult, error) {
				response, err := httpClient.Get(latest.DownloadURL)
				if err != nil {
					return downloadResult{}, err
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusOK {
					return downloadResult{}, fmt.Errorf("rpmrepo: request %s returned %s", latest.DownloadURL, response.Status)
				}
				hasher := sha256.New()
				written, err := io.Copy(hasher, response.Body)
				if err != nil {
					return downloadResult{}, err
				}
				return downloadResult{written: written, sha256: hex.EncodeToString(hasher.Sum(nil))}, nil
			})
			if err != nil {
				skipIfNetworkUnavailable(t, latest.DownloadURL, err)
				t.Fatalf("downloading %s failed: %v", latest.DownloadURL, err)
			}
			if result.written != latest.Size.Package {
				t.Errorf("the download size %d does not match the %d recorded in the metadata", result.written, latest.Size.Package)
			}
			if !strings.EqualFold(result.sha256, latest.Checksum.Value) {
				t.Errorf("checksum mismatch: actual %s, metadata %s", result.sha256, latest.Checksum.Value)
			}
			t.Logf("%s: downloaded %d bytes of %s, SHA256 %s matches the metadata",
				repo.name, result.written, latest.NEVRA(), result.sha256)
		})
	}
}

// TestRealReposChecksumVerification verifies metadata checksums against real data: it passes normally
// and fails when the data is tampered with.
func TestRealReposChecksumVerification(t *testing.T) {
	repo := realRepos()[0]
	repository := openRealRepo(t, repo) // WithChecksumVerification(true) is already enabled.
	if len(findRealPackages(t, repository, Query{Name: realRepoPackage})) == 0 {
		t.Fatal("verification passed but no package was found")
	}

	data := loadRealRepoData(t, repo)
	tampered := New(WithFetcher(&fixtureFetcher{data: data, tamper: zeroOpenChecksum}), WithChecksumVerification(true))
	_, err := tampered.ListPackages(context.Background(), repo.url, realRepoPackage)
	if err == nil {
		t.Fatal("verification still passed after open-checksum was tampered with")
	}
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("error is %v, want ErrChecksumMismatch", err)
	}
	t.Logf("%s: tampering with open-checksum returned the expected error: %v", repo.name, err)
}

// TestRealReposMissingPrimary removes the primary entry from a real repomd.xml to verify the error
// branch.
func TestRealReposMissingPrimary(t *testing.T) {
	repo := realRepos()[0]
	data := loadRealRepoData(t, repo)
	client := New(WithFetcher(&fixtureFetcher{data: data, tamper: dropPrimaryData}))
	if _, err := client.Open(context.Background(), repo.url); !errors.Is(err, ErrPrimaryNotFound) {
		t.Fatalf("error is %v, want ErrPrimaryNotFound", err)
	}
}

// TestRealReposNotFound verifies that a nonexistent repository address returns a clear error.
func TestRealReposNotFound(t *testing.T) {
	missingURL := dockerRepoBase() + "/linux/centos/7/x86_64/does-not-exist/"
	requireNetwork(t, missingURL)
	_, err := callWithRetry(t, missingURL, func() (*Repository, error) {
		return Open(context.Background(), missingURL, WithHTTPClient(realRepoHTTPClient(2*time.Minute)))
	})
	if err == nil {
		t.Fatal("an error was expected")
	}
	skipIfNetworkUnavailable(t, missingURL, err)
	if !errors.Is(err, ErrNotRepository) {
		t.Errorf("error is %v, want it to wrap ErrNotRepository", err)
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
		t.Errorf("error is %v, want an HTTPError containing a 404", err)
	}
}

// zeroOpenChecksum replaces every open-checksum recorded in repomd.xml with zeros.
func zeroOpenChecksum(body []byte) []byte {
	pattern := regexp.MustCompile(`(<open-checksum type="[a-zA-Z0-9]+">)[0-9a-fA-F]+(</open-checksum>)`)
	return pattern.ReplaceAll(body, []byte("${1}"+strings.Repeat("0", 64)+"${2}"))
}

// dropPrimaryData removes the primary metadata entry from repomd.xml.
func dropPrimaryData(body []byte) []byte {
	pattern := regexp.MustCompile(`(?s)\s*<data type="primary">.*?</data>`)
	return pattern.ReplaceAll(body, nil)
}
