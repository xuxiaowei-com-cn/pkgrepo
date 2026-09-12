package rpmrepo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Fetcher reads files from a repository (repomd.xml, primary.xml, and so on).
// The default implementation supports the http, https, and file schemes; a custom implementation
// (for example one that adds a local cache) can be supplied instead.
type Fetcher interface {
	// Open opens the file addressed by rawURL; the caller is responsible for closing the returned
	// io.ReadCloser.
	Open(ctx context.Context, rawURL string) (io.ReadCloser, error)
}

// defaultUserAgent is the default User-Agent.
const defaultUserAgent = "pkgrepo/0.1 (+https://github.com/xuxiaowei-com-cn/pkgrepo)"

// HTTPFetcher reads repository files over HTTP(S).
type HTTPFetcher struct {
	// Client is the underlying HTTP client; when nil, http.DefaultClient is used.
	Client *http.Client
	// UserAgent is the User-Agent request header; when empty, the default value is used.
	UserAgent string
}

// Open implements the Fetcher interface.
func (f *HTTPFetcher) Open(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("rpmrepo: building request failed: %w", err)
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
		return nil, fmt.Errorf("rpmrepo: request %s failed: %w", rawURL, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		resp.Body.Close()
		return nil, &HTTPError{URL: rawURL, StatusCode: resp.StatusCode, Status: resp.Status}
	}
	return resp.Body, nil
}

// FileFetcher reads repository files from the local file system, which is convenient for offline use
// and unit tests.
type FileFetcher struct{}

// Open implements the Fetcher interface.
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

// filePath converts a file:// URL or a scheme-less local path into a file system path.
func filePath(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("rpmrepo: invalid address %q: %w", rawURL, err)
	}
	if u.Scheme == "" {
		return rawURL, nil
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("rpmrepo: unsupported scheme %q", u.Scheme)
	}
	if u.Host != "" && u.Host != "localhost" {
		return "", fmt.Errorf("rpmrepo: unsupported host %q in file address", u.Host)
	}
	if u.Path == "" {
		return "", fmt.Errorf("rpmrepo: file address is missing a path: %q", rawURL)
	}
	return u.Path, nil
}

// schemeFetcher dispatches to the HTTP or local file implementation based on the address scheme.
type schemeFetcher struct {
	http Fetcher
	file Fetcher
}

func (f *schemeFetcher) Open(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("rpmrepo: invalid address %q: %w", rawURL, err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return f.http.Open(ctx, rawURL)
	case "file", "":
		return f.file.Open(ctx, rawURL)
	default:
		return nil, fmt.Errorf("rpmrepo: unsupported scheme %q", u.Scheme)
	}
}
