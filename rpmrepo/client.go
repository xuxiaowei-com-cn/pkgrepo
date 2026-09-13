package rpmrepo

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

// Client is an RPM repository client; it can be reused across repositories and queries.
// The package-level functions Open/ListPackages/FindPackages can be used instead; they use a client
// with the default configuration.
type Client struct {
	httpClient   *http.Client
	userAgent    string
	timeout      time.Duration
	fetcher      Fetcher
	verify       bool
	repositories []string
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

// WithChecksumVerification controls whether metadata checksums are verified (disabled by default).
//
// When enabled, the digest of every primary metadata is computed while it is parsed and compared
// with the open-checksum recorded in repomd.xml, which detects incomplete or tampered metadata.
func WithChecksumVerification(enable bool) Option {
	return func(c *Client) { c.verify = enable }
}

// WithRepositories sets further repository addresses, which are queried together with the address
// given to ListPackages/FindPackages. Several values can be passed; blank values are ignored and
// repeated addresses are read once.
//
// Every address is read independently and the results are merged into a single list: Query.Latest,
// Query.Sort, and Query.Limit apply to the merged list, and every returned package keeps the RepoURL
// and RepoID of the repository it came from. For example, this queries the baseos and appstream
// repositories of AlmaLinux together:
//
//	pkgs, err := rpmrepo.ListPackages(ctx,
//		"https://repo.almalinux.org/almalinux/9/BaseOS/x86_64/os", "nginx",
//		rpmrepo.WithRepositories("https://repo.almalinux.org/almalinux/9/AppStream/x86_64/os"))
//
// The address given to the call may be empty when this option supplies every address. The last call
// wins, so pass every address in a single call.
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

// ListPackages reads the repository at repoURL and returns all packages whose name matches name.
// name supports wildcards, for example "nginx*".
//
// Further repository addresses can be passed with WithRepositories.
func (c *Client) ListPackages(ctx context.Context, repoURL, name string) ([]Package, error) {
	return c.FindPackages(ctx, repoURL, Query{Name: name})
}

// FindPackages reads the repositories at repoURL and returns all packages matching q.
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

// Open reads and parses the repository metadata (repodata/repomd.xml).
// When the same repository is queried several times, calling Open first and reusing the returned
// Repository saves a repeated read of repomd.xml.
func Open(ctx context.Context, repoURL string, opts ...Option) (*Repository, error) {
	return New(opts...).Open(ctx, repoURL)
}

// ListPackages reads the repository at repoURL and returns the packages whose name matches name,
// including metadata such as the download link, size, checksum, and dependencies.
//
// Further repository addresses can be passed with WithRepositories.
//
//	pkgs, err := rpmrepo.ListPackages(ctx, "https://download.docker.com/linux/centos/7/x86_64/stable", "docker-ce")
func ListPackages(ctx context.Context, repoURL, name string, opts ...Option) ([]Package, error) {
	return New(opts...).ListPackages(ctx, repoURL, name)
}

// FindPackages reads the repositories at repoURL and returns the packages matching q.
// Further repository addresses can be passed with WithRepositories.
func FindPackages(ctx context.Context, repoURL string, q Query, opts ...Option) ([]Package, error) {
	return New(opts...).FindPackages(ctx, repoURL, q)
}

// FindPackage returns the newest matching package in the repository, or ErrPackageNotFound when
// there is none.
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
