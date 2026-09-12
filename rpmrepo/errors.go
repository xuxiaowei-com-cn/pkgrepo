package rpmrepo

import (
	"errors"
	"fmt"
)

var (
	// ErrNotRepository 表示给定的地址不是有效的 RPM 仓库（读不到 repodata/repomd.xml）。
	ErrNotRepository = errors.New("rpmrepo: 不是有效的 rpm 仓库，无法读取 repodata/repomd.xml")

	// ErrPrimaryNotFound 表示 repomd.xml 中缺少 primary 元数据条目。
	ErrPrimaryNotFound = errors.New("rpmrepo: repomd.xml 中缺少 primary 元数据")

	// ErrUnsupportedCompression 表示元数据使用了不支持的压缩格式。
	ErrUnsupportedCompression = errors.New("rpmrepo: 不支持的元数据压缩格式")

	// ErrUnsupportedChecksum 表示元数据使用了不支持的校验算法。
	ErrUnsupportedChecksum = errors.New("rpmrepo: 不支持的校验算法")

	// ErrChecksumMismatch 表示元数据校验和不匹配（可能下载不完整或被篡改）。
	ErrChecksumMismatch = errors.New("rpmrepo: 元数据校验和不匹配")

	// ErrPackageNotFound 表示仓库中没有匹配的软件包，由 FindPackage 返回。
	ErrPackageNotFound = errors.New("rpmrepo: 未找到匹配的软件包")
)

// HTTPError 表示 HTTP 请求返回了非 2xx 状态码。
type HTTPError struct {
	// URL 是请求地址。
	URL string
	// StatusCode 是 HTTP 状态码。
	StatusCode int
	// Status 是状态行，例如 "404 Not Found"。
	Status string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("rpmrepo: 请求 %s 失败: %s", e.URL, e.Status)
}
