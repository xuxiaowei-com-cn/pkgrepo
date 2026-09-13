package debrepo

import (
	"context"
	"net/http"
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
func (c *Client) ListPackages(ctx context.Context, repoURL, name string) ([]DebPackage, error) {
	return c.FindPackages(ctx, repoURL, Query{Name: name})
}

// FindPackages reads the repository at repoURL and returns every binary package matching q.
func (c *Client) FindPackages(ctx context.Context, repoURL string, q Query) ([]DebPackage, error) {
	repo, err := c.Open(ctx, repoURL)
	if err != nil {
		return nil, err
	}
	return repo.FindPackages(ctx, q)
}

// ListSources reads the repository at repoURL and returns every source package whose name matches
// name.
func (c *Client) ListSources(ctx context.Context, repoURL, name string) ([]Source, error) {
	return c.FindSources(ctx, repoURL, SourceQuery{Name: name})
}

// FindSources reads the repository at repoURL and returns every source package matching q.
func (c *Client) FindSources(ctx context.Context, repoURL string, q SourceQuery) ([]Source, error) {
	repo, err := c.Open(ctx, repoURL)
	if err != nil {
		return nil, err
	}
	return repo.FindSources(ctx, q)
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
//	pkgs, err := debrepo.ListPackages(ctx, "https://deb.debian.org/debian", "nginx",
//		debrepo.WithSuite("bookworm"), debrepo.WithArchitecture("amd64"))
func ListPackages(ctx context.Context, repoURL, name string, opts ...Option) ([]DebPackage, error) {
	return New(opts...).ListPackages(ctx, repoURL, name)
}

// FindPackages reads the repository at repoURL and returns the packages matching q.
func FindPackages(ctx context.Context, repoURL string, q Query, opts ...Option) ([]DebPackage, error) {
	return New(opts...).FindPackages(ctx, repoURL, q)
}

// FindPackage returns the newest matching package in the repository, or ErrPackageNotFound when there
// is none.
func FindPackage(ctx context.Context, repoURL, name string, opts ...Option) (*DebPackage, error) {
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
func ListSources(ctx context.Context, repoURL, name string, opts ...Option) ([]Source, error) {
	return New(opts...).ListSources(ctx, repoURL, name)
}

// FindSources reads the repository at repoURL and returns every source package matching q.
func FindSources(ctx context.Context, repoURL string, q SourceQuery, opts ...Option) ([]Source, error) {
	return New(opts...).FindSources(ctx, repoURL, q)
}
