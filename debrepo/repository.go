package debrepo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// IndexKind 是索引类型。
type IndexKind string

const (
	// IndexPackages 是二进制包索引（Packages、Packages.gz…）。
	IndexPackages IndexKind = "Packages"
	// IndexSources 是源码包索引（Sources、Sources.gz…）。
	IndexSources IndexKind = "Sources"
)

// IndexFile 是索引文件的一个候选（同一种索引的某种压缩格式）。
type IndexFile struct {
	// Path 是相对仓库根的路径。
	Path string `json:"path"`
	// URL 是绝对地址。
	URL string `json:"url"`
	// Size 是文件大小（字节），来自 Release，未知时为 0。
	Size int64 `json:"size,omitempty"`
	// Checksum 是文件指纹，来自 Release，未知时为空。
	Checksum Checksum `json:"checksum,omitempty"`
	// ByHash 表示该候选来自 by-hash 目录。
	ByHash bool `json:"by_hash,omitempty"`
}

// Index 是一个逻辑索引（组件 + 架构 + 类型）及其候选文件。
// 候选按压缩格式的优先级排序：xz、zst、gz、bz2、lz4、未压缩；
// 每个候选后面可能跟着对应的 by-hash 地址。
type Index struct {
	// Kind 是索引类型。
	Kind IndexKind `json:"kind"`
	// Suite、Component、Architecture 是该索引对应的发行版、组件与架构。
	Suite        string `json:"suite"`
	Component    string `json:"component"`
	Architecture string `json:"architecture,omitempty"`
	// Path、URL、Size、Checksum 是首选候选（与 Candidates[0] 相同）。
	Path     string   `json:"path"`
	URL      string   `json:"url"`
	Size     int64    `json:"size,omitempty"`
	Checksum Checksum `json:"checksum,omitempty"`
	// Candidates 是所有可用的候选文件。
	Candidates []IndexFile `json:"candidates,omitempty"`
	// FromRelease 表示该索引在 Release 文件中有记录（即仓库确实提供该索引）。
	FromRelease bool `json:"from_release"`
}

// Repository 是一个已解析 Release 元数据的 deb 仓库。
type Repository struct {
	// URL 是调用方传入的仓库地址。
	URL string
	// BaseURL 是规范化后的仓库根地址（以 / 结尾），pool/ 与 dists/ 都在其下。
	BaseURL *url.URL
	// ID 是由仓库地址推导出的标识，例如 deb.debian.org/debian。
	ID string
	// Suite 是发行版套件/代号，Codename 是 Release 中的代号（可能为空）。
	Suite    string
	Codename string
	// Components 与 Architectures 是实际扫描的组件与架构列表。
	// 为了兼容 Debian 13 起的 binary-all 索引，Architectures 可能包含 "all"。
	Components    []string
	Architectures []string
	// Release 是 Release/InRelease 的解析结果；当地址直接指向索引文件且读不到
	// Release 时为 nil。
	Release *Release
	// ReleaseURL 是实际使用的 Release/InRelease 地址（可能为空）。
	ReleaseURL string
	// InRelease 表示元数据来自签名的 InRelease 文件（本 SDK 不做签名校验）。
	InRelease bool

	binaryIndexes []Index
	sourceIndexes []Index
	client        *Client
	fetcher       Fetcher
}

// Open 读取并解析仓库元数据（dists/<suite>/Release 或 InRelease）。
//
// repoURL 可以是仓库根地址（https://deb.debian.org/debian）、发行版地址
// （…/dists/bookworm）、组件/架构目录（…/dists/bookworm/main/binary-amd64），
// 也可以是具体的索引地址（…/main/binary-amd64/Packages.gz）或本地目录。
func (c *Client) Open(ctx context.Context, repoURL string) (*Repository, error) {
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
			// 地址直接指向索引时，以地址中的组件/架构为准（Release 只用来校验与发现）。
			repo.Components = []string{firstNonEmpty(location.component, defaultComponent(release))}
			repo.Architectures = []string{firstNonEmpty(location.architecture, HostArchitecture())}
		}
	case location.indexURL == "":
		return nil, err
	default:
		// 地址直接指向索引文件，且仓库没有 Release（例如自建的小仓库），
		// 直接用调用方给出的地址。
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

// openRelease 依次尝试 InRelease 与 Release，返回解析结果与实际使用的地址。
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
			lastErr = fmt.Errorf("%w（%s）: %w", ErrNotRepository, releaseURL, err)
			continue
		}
		return release, releaseURL, candidate.inRelease, nil
	}
	if lastErr == nil {
		lastErr = ErrNotRepository
	}
	// base 一定以 "/" 结尾（dists 目录就在仓库根下）。
	return nil, "", false, fmt.Errorf("%w（%sdists/%s/）: %w", ErrNotRepository, base, suite, lastErr)
}

// resolveComponents 决定最终扫描的组件列表。
//
// 优先级：WithComponent 显式指定 > 地址中推断 > Release 中的 main > Release 中的全部组件。
// WithComponent("*")、WithComponent("any") 表示扫描 Release 中列出的全部组件。
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

// defaultComponent 返回仓库的默认组件：优先 main，其次 Release 中的第一个组件。
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

// resolveArchitectures 决定最终扫描的架构列表。
//
// 优先级：WithArchitecture 显式指定 > 地址中推断 > 宿主机架构。
// WithArchitecture("any")、WithArchitecture("*") 表示扫描 Release 中列出的全部架构。
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

// sourceArchitectures 返回源码包索引使用的架构列表（源码索引与架构无关）。
func (r *Repository) sourceArchitectures() []string { return []string{""} }

// buildIndexes 为每个"组件 + 架构"组合生成索引与候选文件。
// release 非空时只使用 Release 中记录的索引；为空时按惯例猜测各压缩格式。
func (r *Repository) buildIndexes(kind IndexKind, release *Release, components, architectures []string) []Index {
	var indexes []Index
	for _, component := range components {
		if component == "" {
			continue
		}
		arches := architectures
		// Debian 13（trixie）起架构无关包只出现在 binary-all 索引中，
		// 因此每个组件都要额外扫描 binary-all（如果仓库提供的话）。
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

// releaseHasIndex 判断 Release 中是否记录了某个组件目录下的索引（任意压缩格式）。
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

// indexArchitectures 汇总索引中实际出现的架构，保持顺序。
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
	// Release 中的路径是相对 dists/<suite>/ 的；仓库中的路径则要带上 dists/<suite>/。
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
		// 没有 Release 时无法知道仓库提供哪种压缩格式，保留全部候选依次尝试。
	}
	if len(index.Candidates) > 0 {
		index.Path = index.Candidates[0].Path
		index.URL = index.Candidates[0].URL
		index.Size = index.Candidates[0].Size
		index.Checksum = index.Candidates[0].Checksum
	}
	return index
}

// byHashURL 把索引地址转换为 apt 的 by-hash 地址。
// 例如 main/binary-amd64/Packages.xz → main/binary-amd64/by-hash/SHA256/<sha256>。
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

// byHashAlgorithm 返回 by-hash 目录使用的算法名。
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

// Indexes 返回二进制包索引列表。
func (r *Repository) Indexes() []Index {
	return append([]Index(nil), r.binaryIndexes...)
}

// SourceIndexes 返回源码包索引列表。
func (r *Repository) SourceIndexes() []Index {
	return append([]Index(nil), r.sourceIndexes...)
}

// FileURL 把仓库内的相对路径解析为绝对地址。
func (r *Repository) FileURL(relativePath string) (string, error) {
	relativePath = strings.TrimPrefix(strings.TrimSpace(relativePath), "/")
	return resolveURL(r.BaseURL, relativePath)
}

// Scan 流式遍历仓库中的所有二进制软件包，每读到一个包就调用一次 fn（内存占用恒定）。
//
// 返回的 Package 已填充 DownloadURL、RepoURL、RepoID、Suite、Component。
func (r *Repository) Scan(ctx context.Context, fn func(*Package) error) error {
	if fn == nil {
		return nil
	}
	if len(r.binaryIndexes) == 0 {
		return fmt.Errorf("%w: 仓库 %s 中没有可用的 Packages 索引", ErrIndexNotFound, r.ID)
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

// ScanSources 流式遍历仓库中的所有源码包。
func (r *Repository) ScanSources(ctx context.Context, fn func(*Source) error) error {
	if fn == nil {
		return nil
	}
	if len(r.sourceIndexes) == 0 {
		return fmt.Errorf("%w: 仓库 %s 中没有可用的 Sources 索引", ErrIndexNotFound, r.ID)
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

// Packages 返回仓库中的所有二进制软件包（注意：大型仓库会占用较多内存）。
func (r *Repository) Packages(ctx context.Context) ([]Package, error) {
	return r.FindPackages(ctx, Query{})
}

// FindPackages 返回满足 q 的所有二进制软件包。
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

// FindPackage 返回版本最新的匹配包，找不到时返回 ErrPackageNotFound。
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

// Sources 返回仓库中的所有源码包（注意：大型仓库会占用较多内存）。
func (r *Repository) Sources(ctx context.Context) ([]Source, error) {
	return r.FindSources(ctx, SourceQuery{})
}

// FindSources 返回满足 q 的所有源码包。
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

// FindSource 返回版本最新的匹配源码包，找不到时返回 ErrPackageNotFound。
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

// openIndex 依次尝试索引的所有候选（xz、zst、gz、bz2、lz4、未压缩，以及
// by-hash 地址），返回解压后的数据流。
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
	return nil, fmt.Errorf("debrepo: 读取 %s 索引失败（%s）: %w", index.Kind, index.Path, lastErr)
}

// openIndexFile 打开一个索引候选：下载、按需校验（校验的是压缩后的文件本身）、解压。
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
	// 压缩数据的校验值针对的是压缩后的文件本身，而解压器可能在压缩流结束处
	// 就停止读取，因此读完解压流后要把底层数据读完以触发校验。
	return &verifyDrainReader{ReadCloser: body, verifier: verifier}, nil
}

// verifyDrainReader 在解压流读到结尾时把底层数据读完，从而完成校验。
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

// HostArchitecture 返回宿主机对应的 Debian 架构名。
func HostArchitecture() string { return dpkgArchitecture(runtime.GOARCH) }

// dpkgArchitecture 把 Go 的 GOARCH 映射为 Debian 的架构名。
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

// repoLocation 是从仓库地址中解析出的信息。
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

// parseRepoURL 解析仓库地址，支持仓库根地址、发行版地址、组件目录、
// 具体索引文件地址以及本地目录。
func parseRepoURL(rawURL string) (*repoLocation, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("debrepo: 仓库地址不能为空")
	}
	if !strings.Contains(rawURL, "://") {
		absolute, err := filepath.Abs(rawURL)
		if err != nil {
			return nil, fmt.Errorf("debrepo: 解析本地仓库路径 %q 失败: %w", rawURL, err)
		}
		rawURL = (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}).String()
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("debrepo: 非法的仓库地址 %q: %w", rawURL, err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "file":
	default:
		return nil, fmt.Errorf("debrepo: 不支持的仓库地址协议 %q", parsed.Scheme)
	}

	location := &repoLocation{}
	segments := splitPath(parsed.Path)
	// 地址直接指向具体文件时，先摘出文件名。
	var indexFile string
	if len(segments) > 0 && isRepoFileName(segments[len(segments)-1]) {
		indexFile = segments[len(segments)-1]
		segments = segments[:len(segments)-1]
		if kind := indexKindOf(indexFile); kind != "" {
			location.kind = kind
			// 只有 Packages/Sources 索引才允许在 Release 不可用时直接使用，
			// 传入 Release/InRelease 地址时仍然按 suite 去读取元数据。
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
			// by-hash/<算法>/<摘要>，无法从地址推断索引文件名，交给 Release 发现。
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

// splitPath 把 URL 路径按 "/" 切分成非空片段。
func splitPath(p string) []string {
	var segments []string
	for _, segment := range strings.Split(p, "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

// isRepoFileName 判断路径末段是否是本 SDK 会直接读取的文件名。
func isRepoFileName(name string) bool {
	switch {
	case name == "InRelease", name == "Release", name == "Release.gpg":
		return true
	default:
		return indexKindOf(name) != ""
	}
}

// indexKindOf 根据索引文件名判断类型。
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

// repoID 由仓库地址生成一个稳定的标识。
func repoID(u *url.URL) string {
	id := u.Host + strings.TrimSuffix(u.Path, "/")
	if u.Scheme == "file" {
		id = strings.TrimSuffix(u.Path, "/")
	}
	return strings.Trim(id, "/")
}

// resolveURL 把仓库内的相对地址解析为绝对地址。
func resolveURL(base *url.URL, reference string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(reference))
	if err != nil {
		return "", fmt.Errorf("debrepo: 非法的相对地址 %q: %w", reference, err)
	}
	if parsed.IsAbs() {
		return parsed.String(), nil
	}
	return base.ResolveReference(parsed).String(), nil
}
