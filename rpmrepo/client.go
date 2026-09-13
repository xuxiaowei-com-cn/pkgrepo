package rpmrepo

import (
	"context"
	"net/http"
	"time"
)

// defaultTimeout is the default HTTP timeout, which avoids hanging for a long time on an
// unresponsive mirror.
const defaultTimeout = 2 * time.Minute

// Client is an RPM repository client; it can be reused across repositories and queries.
// The package-level functions Open/ListPackages/FindPackages can be used instead; they use a client
// with the default configuration.
type Client struct {
	httpClient *http.Client
	userAgent  string
	timeout    time.Duration
	fetcher    Fetcher
	verify     bool
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
func (c *Client) ListPackages(ctx context.Context, repoURL, name string) ([]RpmPackage, error) {
	return c.FindPackages(ctx, repoURL, Query{Name: name})
}

// FindPackages reads the repository at repoURL and returns all packages matching q.
func (c *Client) FindPackages(ctx context.Context, repoURL string, q Query) ([]RpmPackage, error) {
	repo, err := c.Open(ctx, repoURL)
	if err != nil {
		return nil, err
	}
	return repo.FindPackages(ctx, q)
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
//	pkgs, err := rpmrepo.ListPackages(ctx, "https://download.docker.com/linux/centos/7/x86_64/stable", "docker-ce")
func ListPackages(ctx context.Context, repoURL, name string, opts ...Option) ([]RpmPackage, error) {
	return New(opts...).ListPackages(ctx, repoURL, name)
}

// FindPackages reads the repository at repoURL and returns the packages matching q.
func FindPackages(ctx context.Context, repoURL string, q Query, opts ...Option) ([]RpmPackage, error) {
	return New(opts...).FindPackages(ctx, repoURL, q)
}

// FindPackage returns the newest matching package in the repository, or ErrPackageNotFound when
// there is none.
func FindPackage(ctx context.Context, repoURL, name string, opts ...Option) (*RpmPackage, error) {
	pkgs, err := New(opts...).FindPackages(ctx, repoURL, Query{Name: name, Latest: true, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, ErrPackageNotFound
	}
	return &pkgs[0], nil
}
