package rpmrepo

import (
	"context"
	"net/http"
	"time"
)

// defaultTimeout 是默认的 HTTP 超时，避免长时间卡在无响应的镜像上。
const defaultTimeout = 2 * time.Minute

// Client 是 RPM 仓库客户端，可复用于多个仓库与多次查询。
// 也可以使用包级函数 Open/ListPackages/FindPackages，它们使用默认配置的客户端。
type Client struct {
	httpClient *http.Client
	userAgent  string
	timeout    time.Duration
	fetcher    Fetcher
	verify     bool
}

// Option 用于配置 Client。
type Option func(*Client)

// New 创建一个仓库客户端。
func New(opts ...Option) *Client {
	client := &Client{}
	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	return client
}

// WithHTTPClient 使用自定义的 HTTP 客户端（例如带代理、重试、超时控制）。
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) { c.httpClient = httpClient }
}

// WithUserAgent 设置请求头中的 User-Agent。
func WithUserAgent(userAgent string) Option {
	return func(c *Client) { c.userAgent = userAgent }
}

// WithTimeout 设置 HTTP 请求超时，未设置时默认 2 分钟。
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) { c.timeout = timeout }
}

// WithFetcher 替换默认的下载实现，可用于本地缓存、离线数据源或测试桩。
func WithFetcher(fetcher Fetcher) Option {
	return func(c *Client) { c.fetcher = fetcher }
}

// WithChecksumVerification 控制是否校验元数据校验和（默认关闭）。
//
// 开启后，每份 primary 元数据都会边解析边计算摘要，并与 repomd.xml 中记录的
// open-checksum 比对，用于发现下载不完整或被篡改的元数据。
func WithChecksumVerification(enable bool) Option {
	return func(c *Client) { c.verify = enable }
}

// fetcherInstance 返回实际使用的下载实现。
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

// ListPackages 读取仓库 repoURL 并返回名称匹配 name 的所有软件包。
// name 支持通配符，例如 "nginx*"。
func (c *Client) ListPackages(ctx context.Context, repoURL, name string) ([]Package, error) {
	return c.FindPackages(ctx, repoURL, Query{Name: name})
}

// FindPackages 读取仓库 repoURL 并返回满足 q 的所有软件包。
func (c *Client) FindPackages(ctx context.Context, repoURL string, q Query) ([]Package, error) {
	repo, err := c.Open(ctx, repoURL)
	if err != nil {
		return nil, err
	}
	return repo.FindPackages(ctx, q)
}

// Open 读取并解析仓库元数据（repodata/repomd.xml）。
// 同一个仓库需要多次查询时，先 Open 再复用返回的 Repository 可以少读一次 repomd.xml。
func Open(ctx context.Context, repoURL string, opts ...Option) (*Repository, error) {
	return New(opts...).Open(ctx, repoURL)
}

// ListPackages 读取仓库 repoURL 并返回名称匹配 name 的软件包列表，
// 包含下载链接、大小、指纹（校验和）与依赖等元数据。
//
//	pkgs, err := rpmrepo.ListPackages(ctx, "https://download.docker.com/linux/centos/7/x86_64/stable", "docker-ce")
func ListPackages(ctx context.Context, repoURL, name string, opts ...Option) ([]Package, error) {
	return New(opts...).ListPackages(ctx, repoURL, name)
}

// FindPackages 读取仓库 repoURL 并返回满足 q 的软件包列表。
func FindPackages(ctx context.Context, repoURL string, q Query, opts ...Option) ([]Package, error) {
	return New(opts...).FindPackages(ctx, repoURL, q)
}

// FindPackage 返回仓库中版本最新的匹配包，找不到时返回 ErrPackageNotFound。
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
