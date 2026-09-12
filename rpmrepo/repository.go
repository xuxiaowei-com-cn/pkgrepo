package rpmrepo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
)

// Repository 是一个已解析元数据的 RPM 仓库。
type Repository struct {
	// URL 是调用方传入的仓库地址。
	URL string
	// BaseURL 是规范化后的仓库根地址（以 / 结尾）。
	BaseURL *url.URL
	// ID 是由仓库地址推导出的标识，例如 download.docker.com/linux/centos/7/x86_64/stable。
	ID string
	// RepoMD 是 repodata/repomd.xml 的解析结果。
	RepoMD *RepoMD
	// Revision 是 repomd.xml 中的 revision 字段，元数据变化时会更新。
	Revision string
	// Primary 是 primary 元数据在 repomd.xml 中的条目。
	Primary *RepoMDData

	client  *Client
	fetcher Fetcher
}

// Open 读取并解析仓库元数据。repoURL 可以是仓库根地址、repomd.xml 的地址，
// 也支持本地目录（./repo、/data/repo）与 file:// 地址。
func (c *Client) Open(ctx context.Context, repoURL string) (*Repository, error) {
	base, err := normalizeRepoURL(repoURL)
	if err != nil {
		return nil, err
	}
	fetcher := c.fetcherInstance()
	repomdURL := base.JoinPath("repodata", "repomd.xml").String()
	body, err := fetcher.Open(ctx, repomdURL)
	if err != nil {
		return nil, fmt.Errorf("%w（%s）: %w", ErrNotRepository, repomdURL, err)
	}
	defer body.Close()
	repomd, err := ParseRepoMD(body)
	if err != nil {
		return nil, fmt.Errorf("%w（%s）: %w", ErrNotRepository, repomdURL, err)
	}
	primary, err := repomd.Primary()
	if err != nil {
		return nil, fmt.Errorf("%w（%s）", err, repomdURL)
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

// PrimaryURL 返回 primary 元数据的绝对地址。
func (r *Repository) PrimaryURL() (string, error) {
	if r.Primary == nil {
		return "", ErrPrimaryNotFound
	}
	return resolveLocation(r.BaseURL, r.Primary.Location)
}

// Scan 流式遍历仓库中的所有软件包，每读到一个包就调用一次 fn（内存占用恒定）。
//
// 返回的 Package 已填充 DownloadURL、RepoURL、RepoID。
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

// Packages 返回仓库中的所有软件包（注意：大型仓库会占用较多内存）。
func (r *Repository) Packages(ctx context.Context) ([]Package, error) {
	return r.FindPackages(ctx, Query{})
}

// FindPackages 返回满足 q 的所有软件包。
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

// openPrimary 打开 primary 元数据流，必要时按 repomd.xml 记录的校验和做校验。
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
		return nil, fmt.Errorf("rpmrepo: 读取 primary 元数据失败: %w", err)
	}
	body, compressed, err := decompress(raw)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("rpmrepo: 读取 primary 元数据失败（%s）: %w", primaryURL, err)
	}
	if !r.client.verify {
		return body, nil
	}
	return withVerification(body, r.Primary, compressed)
}

// withVerification 在需要时给元数据流加上校验。
// 压缩数据用 open-checksum（解压后的摘要），未压缩数据用 checksum。
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

// normalizeRepoURL 把用户输入的仓库地址规范化为以 / 结尾的 URL。
// 支持 http(s)://、file://、本地目录路径，以及直接粘贴 repomd.xml 的地址。
func normalizeRepoURL(rawURL string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("rpmrepo: 仓库地址不能为空")
	}
	// 没有协议前缀时按本地路径处理。
	if !strings.Contains(rawURL, "://") {
		abs, err := filepath.Abs(rawURL)
		if err != nil {
			return nil, fmt.Errorf("rpmrepo: 解析本地仓库路径 %q 失败: %w", rawURL, err)
		}
		return withTrailingSlash(&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}), nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("rpmrepo: 非法的仓库地址 %q: %w", rawURL, err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "file":
	default:
		return nil, fmt.Errorf("rpmrepo: 不支持的仓库地址协议 %q", parsed.Scheme)
	}
	// 允许直接传标准 repomd.xml 的地址。
	// 元数据里的 href 是相对仓库根目录的，因此这里要退回仓库根目录。
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

// repoID 由仓库地址生成一个稳定的标识。
func repoID(u *url.URL) string {
	id := u.Host + strings.TrimSuffix(u.Path, "/")
	if u.Scheme == "file" {
		id = strings.TrimSuffix(u.Path, "/")
	}
	return strings.Trim(id, "/")
}

// resolveLocation 把元数据中的相对地址解析为绝对地址。
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
		return nil, fmt.Errorf("rpmrepo: 非法的相对地址 %q: %w", reference, err)
	}
	if parsed.IsAbs() {
		return parsed, nil
	}
	return base.ResolveReference(parsed), nil
}
