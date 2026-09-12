package debrepo

import (
	"errors"
	"fmt"
)

var (
	// ErrNotRepository 表示给定的地址不是有效的 deb 仓库（读不到 Release/InRelease）。
	ErrNotRepository = errors.New("debrepo: 不是有效的 deb 仓库，无法读取 Release/InRelease")

	// ErrSuiteRequired 表示没有指定发行版 suite。
	// suite 可以是套件名（stable、testing），也可以是代号（bookworm、jammy）。
	ErrSuiteRequired = errors.New("debrepo: 未指定发行版 suite，请使用 debrepo.WithSuite 指定（例如 bookworm、jammy、stable）")

	// ErrIndexNotFound 表示仓库中找不到可用的 Packages 或 Sources 索引。
	ErrIndexNotFound = errors.New("debrepo: 仓库中找不到 Packages/Sources 索引")

	// ErrUnsupportedCompression 表示索引使用了不支持的压缩格式。
	ErrUnsupportedCompression = errors.New("debrepo: 不支持的索引压缩格式")

	// ErrUnsupportedChecksum 表示索引使用了不支持的校验算法。
	ErrUnsupportedChecksum = errors.New("debrepo: 不支持的校验算法")

	// ErrChecksumMismatch 表示索引校验和不匹配（可能下载不完整或被篡改）。
	ErrChecksumMismatch = errors.New("debrepo: 索引校验和不匹配")

	// ErrPackageNotFound 表示仓库中没有匹配的软件包，由 FindPackage 返回。
	ErrPackageNotFound = errors.New("debrepo: 未找到匹配的软件包")

	// ErrInvalidVersion 表示 Debian 版本号非法。
	ErrInvalidVersion = errors.New("debrepo: 非法的 Debian 版本号")

	// ErrInvalidControl 表示 deb822（control）数据非法。
	ErrInvalidControl = errors.New("debrepo: 非法的 deb822 控制文件")

	// ErrInvalidRelation 表示依赖关系表达式非法。
	ErrInvalidRelation = errors.New("debrepo: 非法的依赖关系")

	// ErrInvalidPackage 表示 Packages/Sources 条目缺少必须字段。
	ErrInvalidPackage = errors.New("debrepo: 非法的软件包条目")
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
	return fmt.Sprintf("debrepo: 请求 %s 失败: %s", e.URL, e.Status)
}

// NotFound 判断错误是否为 HTTP 404（用于 by-hash 回退）。
func (e *HTTPError) NotFound() bool { return e != nil && e.StatusCode == 404 }
