package debrepo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Fetcher 负责读取仓库中的文件（Release、Packages、Sources 等）。
// 默认实现支持 http、https、file 协议，也可以替换为自己的实现（例如加本地缓存）。
type Fetcher interface {
	// Open 打开 rawURL 指向的文件，调用方负责关闭返回的 io.ReadCloser。
	Open(ctx context.Context, rawURL string) (io.ReadCloser, error)
}

// defaultUserAgent 是默认的 User-Agent。
const defaultUserAgent = "pkgrepo/0.1 (+https://github.com/xuxiaowei-com-cn/pkgrepo)"

// HTTPFetcher 通过 HTTP(S) 读取仓库文件。
type HTTPFetcher struct {
	// Client 是底层 HTTP 客户端，为空时使用 http.DefaultClient。
	Client *http.Client
	// UserAgent 是请求头中的 User-Agent，为空时使用默认值。
	UserAgent string
}

// Open 实现 Fetcher 接口。
func (f *HTTPFetcher) Open(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("debrepo: 构造请求失败: %w", err)
	}
	userAgent := f.UserAgent
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("debrepo: 请求 %s 失败: %w", rawURL, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		resp.Body.Close()
		return nil, &HTTPError{URL: rawURL, StatusCode: resp.StatusCode, Status: resp.Status}
	}
	return resp.Body, nil
}

// FileFetcher 通过本地文件系统读取仓库文件，便于离线使用与单元测试。
type FileFetcher struct{}

// Open 实现 Fetcher 接口。
func (f *FileFetcher) Open(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := filePath(rawURL)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// filePath 把 file:// URL 或无协议前缀的本地路径转换为文件系统路径。
func filePath(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("debrepo: 非法的地址 %q: %w", rawURL, err)
	}
	if u.Scheme == "" {
		return rawURL, nil
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("debrepo: 不支持的协议 %q", u.Scheme)
	}
	if u.Host != "" && u.Host != "localhost" {
		return "", fmt.Errorf("debrepo: 不支持的 file 地址主机 %q", u.Host)
	}
	if u.Path == "" {
		return "", fmt.Errorf("debrepo: file 地址缺少路径: %q", rawURL)
	}
	return u.Path, nil
}

// schemeFetcher 按地址协议分发到 HTTP 或本地文件实现。
type schemeFetcher struct {
	http Fetcher
	file Fetcher
}

func (f *schemeFetcher) Open(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("debrepo: 非法的地址 %q: %w", rawURL, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return f.http.Open(ctx, rawURL)
	case "file", "":
		return f.file.Open(ctx, rawURL)
	default:
		return nil, fmt.Errorf("debrepo: 不支持的协议 %q", u.Scheme)
	}
}
