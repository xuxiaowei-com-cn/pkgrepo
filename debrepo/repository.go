package debrepo

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// IndexKind is the index type.
type IndexKind string

const (
	// IndexPackages is the binary package index (Packages, Packages.gz, ...).
	IndexPackages IndexKind = "Packages"
	// IndexSources is the source package index (Sources, Sources.gz, ...).
	IndexSources IndexKind = "Sources"
)

// IndexFile is one candidate of an index file (a given index in a given compression format).
type IndexFile struct {
	// Path is the path relative to the repository root.
	Path string `json:"path"`
	// URL is the absolute address.
	URL string `json:"url"`
	// Size is the file size in bytes, taken from the Release file; it is 0 when unknown.
	Size int64 `json:"size,omitempty"`
	// Checksum is the file checksum, taken from the Release file; it is empty when unknown.
	Checksum Checksum `json:"checksum,omitempty"`
	// ByHash indicates that this candidate comes from a by-hash directory.
	ByHash bool `json:"by_hash,omitempty"`
}

// Index is a logical index (component + architecture + type) together with its candidate files.
// The candidates are ordered by compression format preference: xz, zst, gz, bz2, lz4, uncompressed;
// each candidate may be followed by its corresponding by-hash address.
type Index struct {
	// Kind is the index type.
	Kind IndexKind `json:"kind"`
	// Suite, Component, and Architecture are the distribution, component, and architecture this
	// index belongs to.
	Suite        string `json:"suite"`
	Component    string `json:"component"`
	Architecture string `json:"architecture,omitempty"`
	// Path, URL, Size, and Checksum describe the preferred candidate (the same as Candidates[0]).
	Path     string   `json:"path"`
	URL      string   `json:"url"`
	Size     int64    `json:"size,omitempty"`
	Checksum Checksum `json:"checksum,omitempty"`
	// Candidates holds every available candidate file.
	Candidates []IndexFile `json:"candidates,omitempty"`
	// FromRelease indicates that the index is recorded in the Release file (that is, the repository
	// really provides this index).
	FromRelease bool `json:"from_release"`
}

// Repository is a deb repository whose Release metadata has been parsed.
type Repository struct {
	// URL is the repository address passed in by the caller.
	URL string
	// BaseURL is the normalized repository root address (ending with /), below which pool/ and
	// dists/ live.
	BaseURL *url.URL
	// ID is the identifier derived from the repository address, for example deb.debian.org/debian.
	ID string
	// Suite is the distribution suite/codename; Codename is the codename from the Release file (which
	// may be empty).
	Suite    string
	Codename string
	// Components and Architectures are the components and architectures actually scanned.
	// Architectures may contain "all" so that the binary-all indexes introduced in Debian 13 are
	// covered.
	Components    []string
	Architectures []string
	// Release is the parse result of Release/InRelease; it is nil when the address points directly at
	// an index file and no Release file can be read.
	Release *Release
	// ReleaseURL is the Release/InRelease address actually used (which may be empty).
	ReleaseURL string
	// InRelease indicates that the metadata came from a signed InRelease file (this SDK does not
	// verify signatures).
	InRelease bool

	binaryIndexes []Index
	sourceIndexes []Index
	client        *Client
	fetcher       Fetcher
}

// Open reads and parses the repository metadata (dists/<suite>/Release or InRelease).
//
// repoURL can be the repository root address (https://deb.debian.org/debian), a distribution address
// (.../dists/bookworm), a component/architecture directory
// (.../dists/bookworm/main/binary-amd64), a specific index address
// (.../main/binary-amd64/Packages.gz), or a local directory.
//
// Open reads a single repository; when further addresses are configured with WithRepositories it
// returns ErrMultipleRepositories, because a *Repository describes one repository. Use
// ListPackages, FindPackages, ListSources, or FindSources to query several repositories at once.
func (c *Client) Open(ctx context.Context, repoURL string) (*Repository, error) {
	if len(c.repositories) > 0 {
		return nil, ErrMultipleRepositories
	}
	return c.open(ctx, repoURL)
}

// open reads and parses a single repository; it is the implementation shared by Open and the
// multi-repository queries, which must not go through the single-repository guard of Open.
func (c *Client) open(ctx context.Context, repoURL string) (*Repository, error) {
	location, err := parseRepoURL(repoURL)
	if err != nil {
		return nil, err
	}
	fetcher := c.fetcherInstance()
	suite := firstNonEmpty(c.suite, location.suite)
	repo := &Repository{
		URL:     repoURL,
		BaseURL: location.base,
		ID:      repoID(location.base),
		Suite:   suite,
		client:  c,
		fetcher: fetcher,
	}
	if suite == "" {
		return nil, ErrSuiteRequired
	}

	release, releaseURL, inRelease, err := c.openRelease(ctx, fetcher, repo.BaseURL, suite)
	switch {
	case err == nil:
		repo.Release = release
		repo.ReleaseURL = releaseURL
		repo.InRelease = inRelease
		if release.Codename != "" {
			repo.Codename = release.Codename
		}
		if location.kind != "" {
			// When the address points directly at an index, the component and architecture from the
			// address take precedence (the Release file is only used for verification and
			// discovery).
			repo.Components = []string{firstNonEmpty(location.component, defaultComponent(release))}
			repo.Architectures = []string{firstNonEmpty(location.architecture, HostArchitecture())}
		}
	case location.indexURL == "":
		return nil, err
	default:
		// The address points directly at an index file and the repository has no Release file (for
		// example a small self-hosted repository), so use the address given by the caller as is.
		repo.Components = []string{firstNonEmpty(location.component, "main")}
		repo.Architectures = []string{firstNonEmpty(location.architecture, HostArchitecture())}
		kind := location.kind
		if kind == "" {
			kind = IndexPackages
		}
		index := Index{
			Kind:         kind,
			Suite:        suite,
			Component:    repo.Components[0],
			Architecture: repo.Architectures[0],
			Path:         location.indexPath,
			URL:          location.indexURL,
			Candidates: []IndexFile{{
				Path: location.indexPath,
				URL:  location.indexURL,
			}},
		}
		if kind == IndexSources {
			repo.sourceIndexes = append(repo.sourceIndexes, index)
		} else {
			repo.binaryIndexes = append(repo.binaryIndexes, index)
		}
		return repo, nil
	}

	if len(repo.Components) == 0 {
		repo.Components = c.resolveComponents(location, repo.Release)
	}
	if len(repo.Architectures) == 0 {
		repo.Architectures = c.resolveArchitectures(location, repo.Release)
	}

	repo.binaryIndexes = repo.buildIndexes(IndexPackages, repo.Release, repo.Components, repo.Architectures)
	repo.sourceIndexes = repo.buildIndexes(IndexSources, repo.Release, repo.Components, repo.sourceArchitectures())
	if architectures := indexArchitectures(repo.binaryIndexes); len(architectures) > 0 {
		repo.Architectures = architectures
	}
	return repo, nil
}

// openRelease tries InRelease and then Release, returning the parse result and the address actually
// used.
func (c *Client) openRelease(ctx context.Context, fetcher Fetcher, base *url.URL, suite string) (*Release, string, bool, error) {
	candidates := []struct {
		name      string
		inRelease bool
	}{
		{"InRelease", true},
		{"Release", false},
	}
	var lastErr error
	for _, candidate := range candidates {
		releaseURL, err := resolveURL(base, path.Join("dists", suite, candidate.name))
		if err != nil {
			return nil, "", false, err
		}
		body, err := fetcher.Open(ctx, releaseURL)
		if err != nil {
			lastErr = err
			continue
		}
		release, err := ParseRelease(body)
		body.Close()
		if err != nil {
			lastErr = fmt.Errorf("%w (%s): %w", ErrNotRepository, releaseURL, err)
			continue
		}
		return release, releaseURL, candidate.inRelease, nil
	}
	if lastErr == nil {
		lastErr = ErrNotRepository
	}
	// base always ends with "/" (the dists directory lives right below the repository root).
	return nil, "", false, fmt.Errorf("%w (%sdists/%s/): %w", ErrNotRepository, base, suite, lastErr)
}

// resolveComponents decides the final list of components to scan.
//
// Precedence: an explicit WithComponent > what can be inferred from the address > main from the
// Release file > every component in the Release file.
// WithComponent("*") and WithComponent("any") mean scanning every component listed in the Release
// file.
func (c *Client) resolveComponents(location *repoLocation, release *Release) []string {
	components := c.components
	if len(components) == 0 && location.component != "" {
		components = []string{location.component}
	}
	if len(components) == 1 && (components[0] == "*" || strings.EqualFold(components[0], "any")) {
		if release != nil && len(release.Components) > 0 {
			return append([]string(nil), release.Components...)
		}
		components = nil
	}
	if len(components) > 0 {
		return components
	}
	if release != nil && len(release.Components) > 0 {
		return []string{defaultComponent(release)}
	}
	return []string{"main"}
}

// defaultComponent returns the default component of the repository: main if present, otherwise the
// first component in the Release file.
func defaultComponent(release *Release) string {
	if release == nil || len(release.Components) == 0 {
		return "main"
	}
	for _, component := range release.Components {
		if component == "main" {
			return "main"
		}
	}
	return release.Components[0]
}

// resolveArchitectures decides the final list of architectures to scan.
//
// Precedence: an explicit WithArchitecture > what can be inferred from the address > the host
// architecture.
// WithArchitecture("any") and WithArchitecture("*") mean scanning every architecture listed in the
// Release file.
func (c *Client) resolveArchitectures(location *repoLocation, release *Release) []string {
	architectures := c.architectures
	if len(architectures) == 0 && location.architecture != "" {
		architectures = []string{location.architecture}
	}
	if len(architectures) == 1 && (architectures[0] == "*" || architectures[0] == "any") {
		if release != nil && len(release.Architectures) > 0 {
			return append([]string(nil), release.Architectures...)
		}
		architectures = nil
	}
	if len(architectures) > 0 {
		return architectures
	}
	return []string{HostArchitecture()}
}

// sourceArchitectures returns the architecture list used by source package indexes (source indexes
// are architecture-independent).
func (r *Repository) sourceArchitectures() []string { return []string{""} }

// buildIndexes builds an index and its candidate files for every "component + architecture"
// combination. When release is non-nil only the indexes recorded in the Release file are used; when
// it is nil the compression formats are guessed by convention.
func (r *Repository) buildIndexes(kind IndexKind, release *Release, components, architectures []string) []Index {
	var indexes []Index
	for _, component := range components {
		if component == "" {
			continue
		}
		arches := architectures
		// Starting with Debian 13 (trixie) architecture-independent packages only appear in the
		// binary-all index, so every component must additionally scan binary-all (when the
		// repository provides it).
		if kind == IndexPackages && release != nil && !containsString(architectures, "all") &&
			releaseHasIndex(release, component, "binary-all", "Packages") {
			arches = append(append([]string(nil), architectures...), "all")
		}
		for _, architecture := range arches {
			index := r.buildIndex(kind, release, component, architecture)
			if len(index.Candidates) == 0 {
				continue
			}
			indexes = append(indexes, index)
		}
	}
	return indexes
}

// releaseHasIndex reports whether the Release file records an index in a component directory (in any
// compression format).
func releaseHasIndex(release *Release, component, archDirectory, name string) bool {
	if release == nil {
		return false
	}
	for _, extension := range IndexCompressions {
		if release.Has(path.Join(component, archDirectory, name+extension)) {
			return true
		}
	}
	return false
}

// indexArchitectures collects the architectures that actually occur in the indexes, preserving their
// order.
func indexArchitectures(indexes []Index) []string {
	seen := make(map[string]bool, len(indexes))
	var architectures []string
	for _, index := range indexes {
		if index.Architecture == "" || seen[index.Architecture] {
			continue
		}
		seen[index.Architecture] = true
		architectures = append(architectures, index.Architecture)
	}
	return architectures
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

func (r *Repository) buildIndex(kind IndexKind, release *Release, component, architecture string) Index {
	directory, name := "source", "Sources"
	if kind == IndexPackages {
		directory, name = "binary-"+architecture, "Packages"
	}
	index := Index{
		Kind:         kind,
		Suite:        r.Suite,
		Component:    component,
		Architecture: architecture,
	}
	if kind == IndexSources {
		index.Architecture = ""
	}
	// Paths in the Release file are relative to dists/<suite>/; paths in the repository must include
	// dists/<suite>/.
	releaseBase := path.Join(component, directory)
	base := path.Join("dists", r.Suite, releaseBase)
	for _, extension := range IndexCompressions {
		releasePath := releaseBase + "/" + name + extension
		relative := base + "/" + name + extension
		candidate := IndexFile{Path: relative}
		if release != nil {
			entry, ok := release.Lookup(releasePath)
			if !ok {
				continue
			}
			candidate.Size = entry.Size
			candidate.Checksum = entry.Checksum
			index.FromRelease = true
		}
		resolved, err := resolveURL(r.BaseURL, relative)
		if err != nil {
			continue
		}
		candidate.URL = resolved
		if release != nil && release.AcquireByHash && !candidate.Checksum.IsZero() {
			if byHash, err := byHashURL(resolved, candidate.Checksum); err == nil {
				byHashCandidate := IndexFile{
					Path:     relative,
					URL:      byHash,
					Size:     candidate.Size,
					Checksum: candidate.Checksum,
					ByHash:   true,
				}
				if r.client != nil && r.client.byHashFirst {
					index.Candidates = append(index.Candidates, byHashCandidate, candidate)
					continue
				}
				index.Candidates = append(index.Candidates, candidate, byHashCandidate)
				continue
			}
		}
		index.Candidates = append(index.Candidates, candidate)
		// Without a Release file there is no way to know which compression formats the repository
		// provides, so keep every candidate and try them in turn.
	}
	if len(index.Candidates) > 0 {
		index.Path = index.Candidates[0].Path
		index.URL = index.Candidates[0].URL
		index.Size = index.Candidates[0].Size
		index.Checksum = index.Candidates[0].Checksum
	}
	return index
}

// byHashURL converts an index address into apt's by-hash address.
// For example main/binary-amd64/Packages.xz -> main/binary-amd64/by-hash/SHA256/<sha256>.
func byHashURL(fileURL string, checksum Checksum) (string, error) {
	algorithm := byHashAlgorithm(checksum.Type)
	if algorithm == "" {
		return "", fmt.Errorf("%w: %q", ErrUnsupportedChecksum, checksum.Type)
	}
	parsed, err := url.Parse(fileURL)
	if err != nil {
		return "", err
	}
	parsed.Path = path.Join(path.Dir(parsed.Path), "by-hash", algorithm, checksum.Value)
	return parsed.String(), nil
}

// byHashAlgorithm returns the algorithm name used by the by-hash directory.
func byHashAlgorithm(algo string) string {
	switch strings.ToLower(strings.TrimSpace(algo)) {
	case "md5":
		return "MD5Sum"
	case "sha1":
		return "SHA1"
	case "sha256":
		return "SHA256"
	case "sha512":
		return "SHA512"
	default:
		return ""
	}
}

// Indexes returns the list of binary package indexes.
func (r *Repository) Indexes() []Index {
	return append([]Index(nil), r.binaryIndexes...)
}

// SourceIndexes returns the list of source package indexes.
func (r *Repository) SourceIndexes() []Index {
	return append([]Index(nil), r.sourceIndexes...)
}

// FileURL resolves a path inside the repository into an absolute address.
func (r *Repository) FileURL(relativePath string) (string, error) {
	relativePath = strings.TrimPrefix(strings.TrimSpace(relativePath), "/")
	return resolveURL(r.BaseURL, relativePath)
}

// Scan streams over every binary package in the repository, calling fn once per package (constant
// memory usage).
//
// The returned Package has DownloadURL, RepoURL, RepoID, Suite, and Component populated.
func (r *Repository) Scan(ctx context.Context, fn func(*Package) error) error {
	if fn == nil {
		return nil
	}
	if len(r.binaryIndexes) == 0 {
		return fmt.Errorf("%w: repository %s has no usable Packages index", ErrIndexNotFound, r.ID)
	}
	for _, index := range r.binaryIndexes {
		if err := r.scanBinaryIndex(ctx, index, fn); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) scanBinaryIndex(ctx context.Context, index Index, fn func(*Package) error) error {
	body, err := r.openIndex(ctx, index)
	if err != nil {
		return err
	}
	defer body.Close()
	return ParsePackages(body, func(pkg *Package) error {
		pkg.Suite = r.Suite
		pkg.Component = index.Component
		pkg.RepoID = r.ID
		pkg.RepoURL = r.BaseURL.String()
		downloadURL, err := r.FileURL(pkg.Filename)
		if err != nil {
			return err
		}
		pkg.DownloadURL = downloadURL
		return fn(pkg)
	})
}

// ScanSources streams over every source package in the repository.
func (r *Repository) ScanSources(ctx context.Context, fn func(*Source) error) error {
	if fn == nil {
		return nil
	}
	if len(r.sourceIndexes) == 0 {
		return fmt.Errorf("%w: repository %s has no usable Sources index", ErrIndexNotFound, r.ID)
	}
	for _, index := range r.sourceIndexes {
		if err := r.scanSourceIndex(ctx, index, fn); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) scanSourceIndex(ctx context.Context, index Index, fn func(*Source) error) error {
	body, err := r.openIndex(ctx, index)
	if err != nil {
		return err
	}
	defer body.Close()
	return ParseSources(body, func(source *Source) error {
		source.Suite = r.Suite
		source.Component = index.Component
		source.RepoID = r.ID
		source.RepoURL = r.BaseURL.String()
		return fn(source)
	})
}

// Packages returns every binary package in the repository (note that large repositories consume a
// lot of memory).
func (r *Repository) Packages(ctx context.Context) ([]Package, error) {
	return r.FindPackages(ctx, Query{})
}

// FindPackages returns every binary package matching q.
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

// Sources returns every source package in the repository (note that large repositories consume a
// lot of memory).
func (r *Repository) Sources(ctx context.Context) ([]Source, error) {
	return r.FindSources(ctx, SourceQuery{})
}

// FindSources returns every source package matching q.
func (r *Repository) FindSources(ctx context.Context, q SourceQuery) ([]Source, error) {
	sources := make([]Source, 0, 16)
	err := r.ScanSources(ctx, func(source *Source) error {
		if q.Match(source) {
			sources = append(sources, *source)
		}
		return nil
	})
	if err != nil {
		return nil, err
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

// FindSource returns the newest matching source package, or ErrPackageNotFound when there is none.
func (r *Repository) FindSource(ctx context.Context, q SourceQuery) (*Source, error) {
	q.Latest = true
	q.Limit = 1
	sources, err := r.FindSources(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(sources) == 0 {
		return nil, ErrPackageNotFound
	}
	return &sources[0], nil
}

// openIndex tries every candidate of the index in turn (xz, zst, gz, bz2, lz4, uncompressed, and the
// by-hash addresses) and returns the decompressed data stream.
func (r *Repository) openIndex(ctx context.Context, index Index) (io.ReadCloser, error) {
	var firstErr, lastErr error
	for _, candidate := range index.Candidates {
		body, err := r.openIndexFile(ctx, candidate)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			lastErr = err
			continue
		}
		return body, nil
	}
	if firstErr != nil {
		lastErr = firstErr
	}
	if lastErr == nil {
		lastErr = ErrIndexNotFound
	}
	return nil, fmt.Errorf("debrepo: reading the %s index failed (%s): %w", index.Kind, index.Path, lastErr)
}

// openIndexFile opens one index candidate: download, verify when needed (the verification covers the
// compressed file itself), and decompress.
func (r *Repository) openIndexFile(ctx context.Context, candidate IndexFile) (io.ReadCloser, error) {
	raw, err := r.fetcher.Open(ctx, candidate.URL)
	if err != nil {
		return nil, err
	}
	var verifier *verifyReader
	var stream io.Reader = raw
	if r.client != nil && r.client.verify && !candidate.Checksum.IsZero() {
		hasher, err := newHash(candidate.Checksum.Type)
		if err != nil {
			raw.Close()
			return nil, err
		}
		verifier = &verifyReader{
			r:        raw,
			hash:     hasher,
			algo:     candidate.Checksum.Type,
			expected: candidate.Checksum.Value,
		}
		stream = &readCloser{Reader: verifier, closer: raw}
	}
	body, _, err := decompress(stream)
	if err != nil {
		raw.Close()
		return nil, err
	}
	if verifier == nil {
		return body, nil
	}
	// The checksum of compressed data covers the compressed file itself, and the decompressor may
	// stop reading at the end of the compressed stream, so the underlying data must be drained after
	// the decompressed stream is exhausted in order to trigger verification.
	return &verifyDrainReader{ReadCloser: body, verifier: verifier}, nil
}

// verifyDrainReader drains the underlying data once the decompressed stream reaches the end, which
// completes the verification.
type verifyDrainReader struct {
	io.ReadCloser
	verifier *verifyReader
}

func (v *verifyDrainReader) Read(p []byte) (int, error) {
	n, err := v.ReadCloser.Read(p)
	if err == io.EOF {
		if v.verifier.err == nil {
			_, _ = io.Copy(io.Discard, v.verifier)
		}
		if v.verifier.err != nil {
			return n, v.verifier.err
		}
	}
	return n, err
}

// HostArchitecture returns the Debian architecture name corresponding to the host.
func HostArchitecture() string { return dpkgArchitecture(runtime.GOARCH) }

// dpkgArchitecture maps Go's GOARCH to a Debian architecture name.
func dpkgArchitecture(goarch string) string {
	switch goarch {
	case "amd64":
		return "amd64"
	case "386":
		return "i386"
	case "arm":
		return "armhf"
	case "arm64":
		return "arm64"
	case "ppc64le":
		return "ppc64el"
	case "ppc64":
		return "ppc64"
	case "s390x":
		return "s390x"
	case "riscv64":
		return "riscv64"
	case "mips":
		return "mips"
	case "mipsle":
		return "mipsel"
	case "mips64":
		return "mips64"
	case "mips64le":
		return "mips64el"
	case "loong64":
		return "loong64"
	default:
		return goarch
	}
}

// repoLocation holds the information parsed out of a repository address.
type repoLocation struct {
	base         *url.URL
	suite        string
	component    string
	architecture string
	kind         IndexKind
	indexFile    string
	indexPath    string
	indexURL     string
}

// parseRepoURL parses a repository address; it supports the repository root address, a distribution
// address, a component directory, a specific index file address, and a local directory.
func parseRepoURL(rawURL string) (*repoLocation, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, ErrNoRepository
	}
	if !strings.Contains(rawURL, "://") {
		absolute, err := filepath.Abs(rawURL)
		if err != nil {
			return nil, fmt.Errorf("debrepo: resolving local repository path %q failed: %w", rawURL, err)
		}
		rawURL = (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}).String()
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("debrepo: invalid repository address %q: %w", rawURL, err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "file":
	default:
		return nil, fmt.Errorf("debrepo: unsupported repository address scheme %q", parsed.Scheme)
	}

	location := &repoLocation{}
	segments := splitPath(parsed.Path)
	// When the address points directly at a specific file, extract the file name first.
	var indexFile string
	if len(segments) > 0 && isRepoFileName(segments[len(segments)-1]) {
		indexFile = segments[len(segments)-1]
		segments = segments[:len(segments)-1]
		if kind := indexKindOf(indexFile); kind != "" {
			location.kind = kind
			// Only a Packages/Sources index may be used directly when no Release file is available;
			// passing a Release/InRelease address still reads the metadata by suite.
			location.indexURL = parsed.String()
		} else {
			indexFile = ""
		}
	}
	distsIdx := -1
	for i, segment := range segments {
		if segment == "dists" {
			distsIdx = i
			break
		}
	}
	if distsIdx < 0 {
		location.base = withTrailingSlash(parsed)
		location.indexFile = indexFile
		return location, nil
	}
	basePath := "/" + strings.Join(segments[:distsIdx], "/")
	location.base = withTrailingSlash(&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: basePath})
	rest := segments[distsIdx+1:]
	if len(rest) > 0 {
		location.suite = rest[0]
		rest = rest[1:]
	}
	for _, segment := range rest {
		switch {
		case segment == "by-hash":
			// by-hash/<algorithm>/<digest>: the index file name cannot be inferred from the address,
			// so leave discovery to the Release file.
			goto done
		case segment == "source":
			location.kind = IndexSources
		case strings.HasPrefix(segment, "binary-"):
			location.kind = IndexPackages
			location.architecture = strings.TrimPrefix(segment, "binary-")
		default:
			if location.component == "" {
				location.component = segment
			}
		}
	}
done:
	location.indexFile = indexFile
	if indexFile != "" {
		switch {
		case location.kind == IndexSources && location.suite != "" && location.component != "":
			location.indexPath = path.Join("dists", location.suite, location.component, "source", indexFile)
		case location.kind == IndexPackages && location.suite != "" && location.component != "" && location.architecture != "":
			location.indexPath = path.Join("dists", location.suite, location.component,
				"binary-"+location.architecture, indexFile)
		case location.suite != "":
			location.indexPath = path.Join("dists", location.suite, indexFile)
		default:
			location.indexPath = indexFile
		}
	}
	return location, nil
}

// splitPath splits a URL path on "/" into non-empty segments.
func splitPath(p string) []string {
	var segments []string
	for _, segment := range strings.Split(p, "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

// isRepoFileName reports whether the last segment of a path is a file name this SDK reads directly.
func isRepoFileName(name string) bool {
	switch {
	case name == "InRelease", name == "Release", name == "Release.gpg":
		return true
	default:
		return indexKindOf(name) != ""
	}
}

// indexKindOf determines the index type from an index file name.
func indexKindOf(name string) IndexKind {
	switch {
	case strings.HasPrefix(name, "Packages"):
		return IndexPackages
	case strings.HasPrefix(name, "Sources"):
		return IndexSources
	default:
		return ""
	}
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

// resolveURL resolves a relative address inside the repository into an absolute one.
func resolveURL(base *url.URL, reference string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(reference))
	if err != nil {
		return "", fmt.Errorf("debrepo: invalid relative address %q: %w", reference, err)
	}
	if parsed.IsAbs() {
		return parsed.String(), nil
	}
	return base.ResolveReference(parsed).String(), nil
}
