package rpmrepo

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
)

// Repository is an RPM repository whose metadata has been parsed.
type Repository struct {
	// URL is the repository address passed in by the caller.
	URL string
	// BaseURL is the normalized repository root address (ending with /).
	BaseURL *url.URL
	// ID is the identifier derived from the repository address, for example
	// download.docker.com/linux/centos/7/x86_64/stable.
	ID string
	// RepoMD is the parse result of repodata/repomd.xml.
	RepoMD *RepoMD
	// Revision is the revision field of repomd.xml; it changes when the metadata changes.
	Revision string
	// Primary is the primary metadata entry in repomd.xml.
	Primary *RepoMDData

	client  *Client
	fetcher Fetcher
}

// Open reads and parses the repository metadata. repoURL can be the repository root address, the
// address of repomd.xml, a local directory (./repo, /data/repo), or a file:// address.
//
// Open reads a single repository; when further addresses are configured with WithRepositories it
// returns ErrMultipleRepositories, because a *Repository describes one repository. Use ListPackages
// or FindPackages to query several repositories at once.
func (c *Client) Open(ctx context.Context, repoURL string) (*Repository, error) {
	if len(c.repositories) > 0 {
		return nil, ErrMultipleRepositories
	}
	return c.open(ctx, repoURL)
}

// open reads and parses a single repository; it is the implementation shared by Open and the
// multi-repository queries, which must not go through the single-repository guard of Open.
func (c *Client) open(ctx context.Context, repoURL string) (*Repository, error) {
	base, err := normalizeRepoURL(repoURL)
	if err != nil {
		return nil, err
	}
	fetcher := c.fetcherInstance()
	repomdURL := base.JoinPath("repodata", "repomd.xml").String()
	body, err := fetcher.Open(ctx, repomdURL)
	if err != nil {
		return nil, fmt.Errorf("%w (%s): %w", ErrNotRepository, repomdURL, err)
	}
	defer body.Close()
	repomd, err := ParseRepoMD(body)
	if err != nil {
		return nil, fmt.Errorf("%w (%s): %w", ErrNotRepository, repomdURL, err)
	}
	primary, err := repomd.Primary()
	if err != nil {
		return nil, fmt.Errorf("%w (%s)", err, repomdURL)
	}
	return &Repository{
		URL:      repoURL,
		BaseURL:  base,
		ID:       repoID(base),
		RepoMD:   repomd,
		Revision: repomd.Revision,
		Primary:  primary,
		client:   c,
		fetcher:  fetcher,
	}, nil
}

// PrimaryURL returns the absolute address of the primary metadata.
func (r *Repository) PrimaryURL() (string, error) {
	if r.Primary == nil {
		return "", ErrPrimaryNotFound
	}
	return resolveLocation(r.BaseURL, r.Primary.Location)
}

// Scan streams over every package in the repository, calling fn once per package (constant memory
// usage).
//
// The returned Package has DownloadURL, RepoURL, and RepoID populated.
func (r *Repository) Scan(ctx context.Context, fn func(*Package) error) error {
	if fn == nil {
		return nil
	}
	body, err := r.openPrimary(ctx)
	if err != nil {
		return err
	}
	defer body.Close()
	return ParsePrimary(body, func(pkg *Package) error {
		pkg.RepoID = r.ID
		pkg.RepoURL = r.BaseURL.String()
		downloadURL, err := resolveLocation(r.BaseURL, pkg.Location)
		if err != nil {
			return err
		}
		pkg.DownloadURL = downloadURL
		return fn(pkg)
	})
}

// Packages returns every package in the repository (note that large repositories consume a lot of
// memory).
func (r *Repository) Packages(ctx context.Context) ([]Package, error) {
	return r.FindPackages(ctx, Query{})
}

// FindPackages returns all packages matching q.
func (r *Repository) FindPackages(ctx context.Context, q Query) ([]Package, error) {
	pkgs := make([]Package, 0, 16)
	err := r.Scan(ctx, func(pkg *Package) error {
		if q.Match(pkg) {
			pkgs = append(pkgs, *pkg)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if q.Latest {
		pkgs = retainLatest(pkgs)
	}
	q.sort(pkgs)
	if q.Limit > 0 && len(pkgs) > q.Limit {
		pkgs = pkgs[:q.Limit]
	}
	return pkgs, nil
}

// FindPackage returns the newest matching package, or ErrPackageNotFound when there is none.
func (r *Repository) FindPackage(ctx context.Context, q Query) (*Package, error) {
	q.Latest = true
	q.Limit = 1
	pkgs, err := r.FindPackages(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(pkgs) == 0 {
		return nil, ErrPackageNotFound
	}
	return &pkgs[0], nil
}

// openPrimary opens the primary metadata stream, verifying it against the checksum recorded in
// repomd.xml when necessary.
func (r *Repository) openPrimary(ctx context.Context) (io.ReadCloser, error) {
	if r.Primary == nil {
		return nil, ErrPrimaryNotFound
	}
	primaryURL, err := resolveLocation(r.BaseURL, r.Primary.Location)
	if err != nil {
		return nil, err
	}
	raw, err := r.fetcher.Open(ctx, primaryURL)
	if err != nil {
		return nil, fmt.Errorf("rpmrepo: reading primary metadata failed: %w", err)
	}
	body, compressed, err := decompress(raw)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("rpmrepo: reading primary metadata failed (%s): %w", primaryURL, err)
	}
	if !r.client.verify {
		return body, nil
	}
	return withVerification(body, r.Primary, compressed)
}

// withVerification adds verification to the metadata stream when needed.
// Compressed data uses open-checksum (the digest of the decompressed data), and uncompressed data
// uses checksum.
func withVerification(body io.ReadCloser, data *RepoMDData, compressed bool) (io.ReadCloser, error) {
	expected := data.OpenChecksum
	if !compressed && expected.Value == "" {
		expected = data.Checksum
	}
	if expected.Value == "" {
		return body, nil
	}
	hasher, err := newHash(expected.Type)
	if err != nil {
		body.Close()
		return nil, err
	}
	return &verifyCloser{
		Reader: &verifyReader{
			r:        body,
			hash:     hasher,
			algo:     expected.Type,
			expected: expected.Value,
		},
		closer: body,
	}, nil
}

type verifyCloser struct {
	io.Reader
	closer io.Closer
}

func (c *verifyCloser) Close() error { return c.closer.Close() }

// normalizeRepoURL normalizes a user-supplied repository address into a URL ending with /.
// It supports http(s)://, file://, local directory paths, and an address pointing directly at
// repomd.xml.
func normalizeRepoURL(rawURL string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, ErrNoRepository
	}
	// Without a scheme prefix, treat the input as a local path.
	if !strings.Contains(rawURL, "://") {
		abs, err := filepath.Abs(rawURL)
		if err != nil {
			return nil, fmt.Errorf("rpmrepo: resolving local repository path %q failed: %w", rawURL, err)
		}
		return withTrailingSlash(&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}), nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("rpmrepo: invalid repository address %q: %w", rawURL, err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "file":
	default:
		return nil, fmt.Errorf("rpmrepo: unsupported repository address scheme %q", parsed.Scheme)
	}
	// Accept an address pointing directly at the standard repomd.xml.
	// The href values inside the metadata are relative to the repository root, so step back up to
	// the repository root here.
	parsed.Path = strings.TrimSuffix(parsed.Path, "/repodata/repomd.xml")
	parsed.Path = strings.TrimSuffix(parsed.Path, "/repomd.xml")
	return withTrailingSlash(parsed), nil
}

func withTrailingSlash(u *url.URL) *url.URL {
	clone := *u
	if !strings.HasSuffix(clone.Path, "/") {
		clone.Path += "/"
	}
	return &clone
}

// repoID derives a stable identifier from the repository address.
func repoID(u *url.URL) string {
	id := u.Host + strings.TrimSuffix(u.Path, "/")
	if u.Scheme == "file" {
		id = strings.TrimSuffix(u.Path, "/")
	}
	return strings.Trim(id, "/")
}

// resolveLocation resolves the relative address in the metadata into an absolute one.
func resolveLocation(base *url.URL, location Location) (string, error) {
	reference := base
	if location.Base != "" {
		parsed, err := resolveURLRef(base, location.Base)
		if err != nil {
			return "", err
		}
		reference = parsed
	}
	return resolveURL(reference, location.Href)
}

func resolveURL(base *url.URL, reference string) (string, error) {
	resolved, err := resolveURLRef(base, reference)
	if err != nil {
		return "", err
	}
	return resolved.String(), nil
}

func resolveURLRef(base *url.URL, reference string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(reference))
	if err != nil {
		return nil, fmt.Errorf("rpmrepo: invalid relative address %q: %w", reference, err)
	}
	if parsed.IsAbs() {
		return parsed, nil
	}
	return base.ResolveReference(parsed), nil
}
