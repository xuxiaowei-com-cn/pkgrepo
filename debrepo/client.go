package debrepo

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// defaultTimeout is the default HTTP timeout, which avoids hanging for a long time on an
// unresponsive mirror.
const defaultTimeout = 2 * time.Minute

// Client is a deb repository client; it can be reused across repositories and queries.
// The package-level functions Open/ListPackages/FindPackages can be used instead; they use a client
// with the default configuration.
type Client struct {
	httpClient    *http.Client
	userAgent     string
	timeout       time.Duration
	fetcher       Fetcher
	verify        bool
	byHashFirst   bool
	suite         string
	components    []string
	architectures []string
	repositories  []string
}

// Option configures a Client.
type Option func(*Client)

// New creates a repository client.
func New(opts ...Option) *Client {
	client := &Client{}
	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	return client
}

// WithHTTPClient uses a custom HTTP client (for example one with a proxy, retries, or timeout
// control).
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) { c.httpClient = httpClient }
}

// WithUserAgent sets the User-Agent request header.
func WithUserAgent(userAgent string) Option {
	return func(c *Client) { c.userAgent = userAgent }
}

// WithTimeout sets the HTTP request timeout; the default is 2 minutes when unset.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) { c.timeout = timeout }
}

// WithFetcher replaces the default download implementation; it can be used for a local cache, an
// offline data source, or a test stub.
func WithFetcher(fetcher Fetcher) Option {
	return func(c *Client) { c.fetcher = fetcher }
}

// WithChecksumVerification controls whether index checksums are verified (disabled by default).
//
// When enabled, the digest of every Packages/Sources index is computed while it is parsed and
// compared with the SHA256/SHA512 checksums recorded in the Release file, which detects incomplete
// or tampered indexes.
func WithChecksumVerification(enable bool) Option {
	return func(c *Client) { c.verify = enable }
}

// WithByHashFirst controls whether indexes are fetched from by-hash addresses first (disabled by
// default, which tries the regular path first and falls back to by-hash on failure). When the
// repository supports by-hash (Acquire-By-Hash: yes in the Release file), by-hash addresses prevent
// reading a half-updated index while a mirror is synchronizing.
func WithByHashFirst(enable bool) Option {
	return func(c *Client) { c.byHashFirst = enable }
}

// WithSuite sets the distribution suite or codename, for example bookworm, trixie, jammy, or stable.
// It can be omitted when the address already contains dists/<suite>.
func WithSuite(suite string) Option {
	return func(c *Client) { c.suite = suite }
}

// WithComponent sets the components to scan, for example main, contrib, or universe.
// Several values can be passed; "*" or "any" means every component listed in the Release file. When
// unset, main is preferred and the first component in the Release file is used when the repository
// has no main.
func WithComponent(components ...string) Option {
	return func(c *Client) { c.components = components }
}

// WithArchitecture sets the architectures to scan, for example amd64, arm64, or all.
// Several values can be passed; "*" or "any" means every architecture listed in the Release file.
// When unset, the host architecture is used (and the binary-all index is included automatically).
func WithArchitecture(architectures ...string) Option {
	return func(c *Client) { c.architectures = architectures }
}

// WithRepositories sets further repository addresses, which are queried together with the address
// given to ListPackages/FindPackages/ListSources/FindSources. Several values can be passed; blank
// values are ignored and repeated addresses are read once.
//
// Every address is read with the same suite, component, and architecture settings, and the results
// are merged into a single list: Query.Latest, Query.Sort, and Query.Limit apply to the merged list,
// and every returned package keeps the RepoURL and RepoID of the repository it came from. For
// example, this queries Debian main together with the security archive:
//
//	pkgs, err := debrepo.ListPackages(ctx, "https://deb.debian.org/debian", "nginx",
//		debrepo.WithSuite("bookworm"), debrepo.WithArchitecture("amd64"),
//		debrepo.WithRepositories("https://security.debian.org/debian-security"))
//
// The address given to the call may be empty when this option supplies every address. The last call
// wins, so pass every address in a single call.
//
// WithSuite, WithComponent, and WithArchitecture apply to every address. When the repositories use
// different suites (for example the Debian security archive, whose suite is bookworm-security), pass
// the suite in the address instead of WithSuite:
//
//	pkgs, err := debrepo.ListPackages(ctx, "https://deb.debian.org/debian/dists/bookworm", "nginx",
//		debrepo.WithArchitecture("amd64"),
//		debrepo.WithRepositories("https://security.debian.org/debian-security/dists/bookworm-security"))
//
// Open reads a single repository and returns ErrMultipleRepositories when further addresses are
// configured.
func WithRepositories(repoURLs ...string) Option {
	return func(c *Client) {
		addresses := make([]string, 0, len(repoURLs))
		for _, repoURL := range repoURLs {
			if address := strings.TrimSpace(repoURL); address != "" {
				addresses = append(addresses, address)
			}
		}
		c.repositories = addresses
	}
}

// fetcherInstance returns the download implementation actually in use.
func (c *Client) fetcherInstance() Fetcher {
	if c.fetcher != nil {
		return c.fetcher
	}
	timeout := c.timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	} else if httpClient.Timeout == 0 && c.timeout > 0 {
		clone := *httpClient
		clone.Timeout = c.timeout
		httpClient = &clone
	}
	return &schemeFetcher{
		http: &HTTPFetcher{Client: httpClient, UserAgent: c.userAgent},
		file: &FileFetcher{},
	}
}

// ListPackages reads the repository at repoURL and returns every binary package whose name matches
// name. name supports wildcards, for example "nginx*".
//
// Further repository addresses can be passed with WithRepositories.
func (c *Client) ListPackages(ctx context.Context, repoURL, name string) ([]Package, error) {
	return c.FindPackages(ctx, repoURL, Query{Name: name})
}

// FindPackages reads the repositories at repoURL and returns every binary package matching q.
// Further repository addresses can be passed with WithRepositories.
//
// A single address is read like Open + Repository.FindPackages. With several addresses every
// repository is read independently and the results are merged, so Query.Latest, Query.Sort, and
// Query.Limit apply to the merged list.
func (c *Client) FindPackages(ctx context.Context, repoURL string, q Query) ([]Package, error) {
	addresses := c.repositoryAddresses(repoURL)
	if len(addresses) == 1 {
		repo, err := c.Open(ctx, addresses[0])
		if err != nil {
			return nil, err
		}
		return repo.FindPackages(ctx, q)
	}
	return c.findPackages(ctx, addresses, q)
}

// ListSources reads the repository at repoURL and returns every source package whose name matches
// name.
//
// Further repository addresses can be passed with WithRepositories.
func (c *Client) ListSources(ctx context.Context, repoURL, name string) ([]Source, error) {
	return c.FindSources(ctx, repoURL, SourceQuery{Name: name})
}

// FindSources reads the repositories at repoURL and returns every source package matching q.
// Further repository addresses can be passed with WithRepositories; the results are merged like
// those of FindPackages.
func (c *Client) FindSources(ctx context.Context, repoURL string, q SourceQuery) ([]Source, error) {
	addresses := c.repositoryAddresses(repoURL)
	if len(addresses) == 1 {
		repo, err := c.Open(ctx, addresses[0])
		if err != nil {
			return nil, err
		}
		return repo.FindSources(ctx, q)
	}
	return c.findSources(ctx, addresses, q)
}

// repositoryAddresses returns the addresses to read for one query: the address given to the call
// (when it is not empty) followed by the addresses set with WithRepositories, with repeated
// addresses removed.
func (c *Client) repositoryAddresses(repoURL string) []string {
	addresses := make([]string, 0, len(c.repositories)+1)
	seen := make(map[string]struct{}, len(c.repositories)+1)
	for _, candidate := range append([]string{repoURL}, c.repositories...) {
		address := strings.TrimSpace(candidate)
		if address == "" {
			continue
		}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		addresses = append(addresses, address)
	}
	return addresses
}

// findPackages queries every repository in addresses and merges the results.
//
// Latest, sorting, and the limit are applied to the merged list, so the per-repository queries only
// filter.
func (c *Client) findPackages(ctx context.Context, addresses []string, q Query) ([]Package, error) {
	if len(addresses) == 0 {
		return nil, ErrNoRepository
	}
	scan := q
	scan.Latest = false
	scan.Limit = 0
	scan.Sort = SortNone
	packages := make([]Package, 0, 32)
	for _, address := range addresses {
		repo, err := c.open(ctx, address)
		if err != nil {
			return nil, fmt.Errorf("%w (%s)", err, address)
		}
		found, err := repo.FindPackages(ctx, scan)
		if err != nil {
			return nil, fmt.Errorf("%w (%s)", err, address)
		}
		packages = append(packages, found...)
	}
	if q.Latest {
		packages = retainLatest(packages)
	}
	q.sort(packages)
	if q.Limit > 0 && len(packages) > q.Limit {
		packages = packages[:q.Limit]
	}
	return packages, nil
}

// findSources queries every repository in addresses and merges the results; it works like
// findPackages.
func (c *Client) findSources(ctx context.Context, addresses []string, q SourceQuery) ([]Source, error) {
	if len(addresses) == 0 {
		return nil, ErrNoRepository
	}
	scan := q
	scan.Latest = false
	scan.Limit = 0
	scan.Sort = SortNone
	sources := make([]Source, 0, 32)
	for _, address := range addresses {
		repo, err := c.open(ctx, address)
		if err != nil {
			return nil, fmt.Errorf("%w (%s)", err, address)
		}
		found, err := repo.FindSources(ctx, scan)
		if err != nil {
			return nil, fmt.Errorf("%w (%s)", err, address)
		}
		sources = append(sources, found...)
	}
	if q.Latest {
		sources = retainLatestSources(sources)
	}
	q.sort(sources)
	if q.Limit > 0 && len(sources) > q.Limit {
		sources = sources[:q.Limit]
	}
	return sources, nil
}

// Open reads and parses the repository metadata (dists/<suite>/Release or InRelease).
// When the same repository is queried several times, calling Open first and reusing the returned
// Repository saves a repeated read of the Release file.
func Open(ctx context.Context, repoURL string, opts ...Option) (*Repository, error) {
	return New(opts...).Open(ctx, repoURL)
}

// ListPackages reads the repository at repoURL and returns the packages whose name matches name,
// including metadata such as the download link, size, checksum, and dependencies.
//
// Further repository addresses can be passed with WithRepositories.
//
//	pkgs, err := debrepo.ListPackages(ctx, "https://deb.debian.org/debian", "nginx",
//		debrepo.WithSuite("bookworm"), debrepo.WithArchitecture("amd64"))
func ListPackages(ctx context.Context, repoURL, name string, opts ...Option) ([]Package, error) {
	return New(opts...).ListPackages(ctx, repoURL, name)
}

// FindPackages reads the repositories at repoURL and returns the packages matching q.
// Further repository addresses can be passed with WithRepositories.
func FindPackages(ctx context.Context, repoURL string, q Query, opts ...Option) ([]Package, error) {
	return New(opts...).FindPackages(ctx, repoURL, q)
}

// FindPackage returns the newest matching package in the repository, or ErrPackageNotFound when there
// is none.
//
// Further repository addresses can be passed with WithRepositories; the newest package across every
// repository is returned.
func FindPackage(ctx context.Context, repoURL, name string, opts ...Option) (*Package, error) {
	pkgs, err := New(opts...).FindPackages(ctx, repoURL, Query{Name: name, Latest: true, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, ErrPackageNotFound
	}
	return &pkgs[0], nil
}

// ListSources reads the repository at repoURL and returns every source package whose name matches
// name.
//
// Further repository addresses can be passed with WithRepositories.
func ListSources(ctx context.Context, repoURL, name string, opts ...Option) ([]Source, error) {
	return New(opts...).ListSources(ctx, repoURL, name)
}

// FindSources reads the repository at repoURL and returns every source package matching q.
//
// Further repository addresses can be passed with WithRepositories.
func FindSources(ctx context.Context, repoURL string, q SourceQuery, opts ...Option) ([]Source, error) {
	return New(opts...).FindSources(ctx, repoURL, q)
}
