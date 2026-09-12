package rpmrepo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// realRepo 描述一个用于端到端验证的真实 rpm 仓库。
// 这些地址来自 Docker 官方 CentOS 源，覆盖 gzip 与 zstd 两种 primary 元数据压缩格式。
type realRepo struct {
	name               string
	url                string
	distTag            string
	primaryCompression string
	minPackages        int
	wantRequires       []string
}

// dockerRepoBase 是 Docker 官方源地址。
//
// 网络受限或位于国内时，可以用环境变量指向同一份仓库数据的镜像，例如：
//
//	PKGREPO_DOCKER_BASE=https://mirrors.aliyun.com/docker-ce go test ./...
func dockerRepoBase() string {
	if base := strings.TrimSpace(os.Getenv("PKGREPO_DOCKER_BASE")); base != "" {
		return strings.TrimSuffix(base, "/")
	}
	return "https://download.docker.com"
}

// realRepos 返回用于端到端验证的真实仓库。
func realRepos() []realRepo {
	base := dockerRepoBase()
	repo := func(name, dist, compression string, minPackages int, requires ...string) realRepo {
		return realRepo{
			name:               name,
			url:                base + "/linux/centos/" + strings.TrimPrefix(name, "centos") + "/x86_64/stable/",
			distTag:            dist,
			primaryCompression: compression,
			minPackages:        minPackages,
			wantRequires:       requires,
		}
	}
	return []realRepo{
		repo("centos7", "el7", "gzip", 50, "containerd.io", "systemd", "libc.so.6(GLIBC_2.14)(64bit)"),
		repo("centos8", "el8", "gzip", 30, "containerd.io", "systemd", "libc.so.6(GLIBC_2.14)(64bit)"),
		repo("centos9", "el9", "zstd", 50,
			"containerd.io", "nftables", "(iptables-nft or iptables)", "libc.so.6(GLIBC_2.34)(64bit)"),
		repo("centos10", "el10", "zstd", 30,
			"containerd.io", "nftables", "(iptables-nft or iptables)", "libc.so.6(GLIBC_2.34)(64bit)"),
	}
}

// realRepoPackage 是测试使用的软件名称。
const realRepoPackage = "docker-ce"

// 网络工具
//
// networkUnavailable 记录本次测试运行是否已确认连不上外网，
// 避免离线环境下每个子测试都反复等待 DNS 超时。
var networkUnavailable atomic.Bool

// realRepoHTTPClient 返回测试用的 HTTP 客户端。
//
// 这里固定使用 IPv4：部分网络环境（例如本机）到镜像站点的 IPv6 链路会随机重置连接，
// 造成与 SDK 无关的失败。SDK 本身不限制地址族，测试只是让链路更稳定。
func realRepoHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				return dialer.DialContext(ctx, "tcp4", addr)
			},
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          20,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   15 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
	}
}

// requireNetwork 在本次运行已确认网络不可用时直接跳过。
func requireNetwork(t *testing.T, repoURL string) {
	t.Helper()
	if networkUnavailable.Load() {
		t.Skipf("跳过：本次运行已确认网络不可用，无法访问 %s", repoURL)
	}
}

// isNetworkError 判断错误是否来自网络层（离线、DNS 失败、连接被重置等）。
func isNetworkError(err error) bool {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return false
	}
	var netErr net.Error
	return errors.As(urlErr.Err, &netErr)
}

// isOfflineError 判断错误是否表示"当前环境连不上外网"（DNS 解析失败、网络不可达等）。
// 连接被重置这类抖动不算离线，交给重试处理。
func isOfflineError(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		message := urlErr.Err.Error()
		for _, marker := range []string{"no such host", "network is unreachable", "name resolution"} {
			if strings.Contains(message, marker) {
				return true
			}
		}
	}
	return false
}

// callWithRetry 在网络抖动时重试（最多 6 次，逐步加大退避），返回最后一次的结果。
// 仓库真实返回 404 之类的响应不会重试，直接返回。
func callWithRetry[T any](t *testing.T, repoURL string, action func() (T, error)) (T, error) {
	t.Helper()
	var (
		value T
		err   error
	)
	attempts := 6
	if networkUnavailable.Load() {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		value, err = action()
		if err == nil || !isNetworkError(err) {
			return value, err
		}
		if attempt < attempts {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
	}
	t.Logf("访问 %s 连续 %d 次网络失败: %v", repoURL, attempts, err)
	return value, err
}

// skipIfNetworkUnavailable 只在"连不上"时跳过测试（离线环境、DNS 不可用、链路抖动等）；
// 仓库真实返回 404 等响应仍然算失败，避免掩盖回归。
func skipIfNetworkUnavailable(t *testing.T, repoURL string, err error) {
	t.Helper()
	if isNetworkError(err) {
		if isOfflineError(err) {
			networkUnavailable.Store(true)
		}
		t.Skipf("跳过：网络不可用，无法访问真实仓库 %s（%v）", repoURL, err)
	}
}

// 真实元数据缓存
//
// realRepoData 保存从真实仓库下载的元数据。每次测试运行对每个仓库只下载一次，
// 之后的测试都复用同一份字节，既保证数据来自真实仓库，又避免重复请求触发镜像站限流。
type realRepoData struct {
	repo       realRepo
	repomdURL  string
	primaryURL string
	// repomd 是 repodata/repomd.xml 的原始字节。
	repomd []byte
	// primary 是 primary 元数据的原始字节（仍处于压缩状态）。
	primary []byte
	// fetchErr 记录下载元数据时的错误（成功时为 nil），避免每个测试重复重试。
	fetchErr error
}

var (
	realRepoDataMu  sync.Mutex
	realRepoDataSet = make(map[string]*realRepoData)
)

// loadRealRepoData 返回真实仓库的元数据，同一个仓库在本次运行中只下载一次。
func loadRealRepoData(t *testing.T, repo realRepo) *realRepoData {
	t.Helper()
	realRepoDataMu.Lock()
	defer realRepoDataMu.Unlock()
	if data, ok := realRepoDataSet[repo.url]; ok {
		if data.fetchErr != nil {
			skipIfNetworkUnavailable(t, repo.url, data.fetchErr)
			t.Fatalf("读取真实仓库 %s 失败: %v", repo.url, data.fetchErr)
		}
		return data
	}
	requireNetwork(t, repo.url)

	ctx := context.Background()
	httpClient := realRepoHTTPClient(2 * time.Minute)
	base := strings.TrimSuffix(repo.url, "/")
	data := &realRepoData{
		repo:      repo,
		repomdURL: base + "/repodata/repomd.xml",
	}

	repomd, err := callWithRetry(t, data.repomdURL, func() ([]byte, error) {
		return httpGetBytes(ctx, httpClient, data.repomdURL)
	})
	if err != nil {
		data.fetchErr = err
		realRepoDataSet[repo.url] = data
		skipIfNetworkUnavailable(t, data.repomdURL, err)
		t.Fatalf("读取 %s 失败: %v", data.repomdURL, err)
	}
	data.repomd = repomd

	parsed, err := ParseRepoMD(bytes.NewReader(repomd))
	if err != nil {
		data.fetchErr = err
		realRepoDataSet[repo.url] = data
		t.Fatalf("解析 %s 失败: %v", data.repomdURL, err)
	}
	primary, err := parsed.Primary()
	if err != nil {
		data.fetchErr = err
		realRepoDataSet[repo.url] = data
		t.Fatalf("%s 缺少 primary 元数据: %v", data.repomdURL, err)
	}
	data.primaryURL = base + "/" + primary.Location.Href

	payload, err := callWithRetry(t, data.primaryURL, func() ([]byte, error) {
		return httpGetBytes(ctx, httpClient, data.primaryURL)
	})
	if err != nil {
		data.fetchErr = err
		realRepoDataSet[repo.url] = data
		skipIfNetworkUnavailable(t, data.primaryURL, err)
		t.Fatalf("读取 %s 失败: %v", data.primaryURL, err)
	}
	if int64(len(payload)) != primary.Size {
		t.Errorf("%s 实际大小 %d 与 repomd.xml 记录的 %d 不一致",
			data.primaryURL, len(payload), primary.Size)
	}
	data.primary = payload

	t.Logf("%s: 已下载真实元数据 repomd.xml=%d 字节 primary=%d 字节（%s）",
		repo.name, len(data.repomd), len(data.primary), CompressionOf(primary.Location.Href))
	realRepoDataSet[repo.url] = data
	return data
}

func httpGetBytes(ctx context.Context, httpClient *http.Client, rawURL string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rpmrepo: 请求 %s 返回 %s", rawURL, response.Status)
	}
	return io.ReadAll(response.Body)
}

// fixtureFetcher 用已下载的真实元数据响应 SDK 的请求，避免测试重复访问网络。
type fixtureFetcher struct {
	data *realRepoData
	// tamper 用于改写 repomd.xml，验证错误分支。
	tamper func([]byte) []byte
}

func (f *fixtureFetcher) Open(_ context.Context, rawURL string) (io.ReadCloser, error) {
	switch rawURL {
	case f.data.repomdURL:
		body := f.data.repomd
		if f.tamper != nil {
			body = f.tamper(body)
		}
		return io.NopCloser(bytes.NewReader(body)), nil
	case f.data.primaryURL:
		return io.NopCloser(bytes.NewReader(f.data.primary)), nil
	default:
		return nil, fmt.Errorf("测试桩：未缓存的地址 %s", rawURL)
	}
}

// openRealRepo 用真实元数据打开仓库（开启校验和校验）。
func openRealRepo(t *testing.T, repo realRepo) *Repository {
	t.Helper()
	data := loadRealRepoData(t, repo)
	repository, err := New(WithFetcher(&fixtureFetcher{data: data}), WithChecksumVerification(true)).
		Open(context.Background(), repo.url)
	if err != nil {
		t.Fatalf("打开真实仓库 %s 失败: %v", repo.url, err)
	}
	return repository
}

// findRealPackages 在真实元数据上查询软件包。
func findRealPackages(t *testing.T, repository *Repository, query Query) []Package {
	t.Helper()
	pkgs, err := repository.FindPackages(context.Background(), query)
	if err != nil {
		t.Fatalf("查询 %s 失败: %v", repository.URL, err)
	}
	return pkgs
}

// TestRealReposListPackages 用真实仓库验证核心功能：仓库地址 + 软件名称 → 软件包列表。
func TestRealReposListPackages(t *testing.T) {
	for _, repo := range realRepos() {
		t.Run(repo.name, func(t *testing.T) {
			repository := openRealRepo(t, repo)
			pkgs := findRealPackages(t, repository, Query{Name: realRepoPackage, Arch: "x86_64"})
			if len(pkgs) < repo.minPackages {
				t.Fatalf("docker-ce 包数量为 %d，少于预期的 %d，仓库元数据可能异常", len(pkgs), repo.minPackages)
			}

			// 默认排序为版本降序，第一个即最新版本。
			latest := pkgs[0]
			for i := range pkgs {
				pkg := &pkgs[i]
				if pkg.Version.Compare(latest.Version) > 0 {
					t.Fatalf("排序结果有误：%s 比 %s 更新", pkg.NEVRA(), latest.NEVRA())
				}
				if pkg.Name != realRepoPackage || pkg.Arch != "x86_64" {
					t.Fatalf("查询结果混入了其他包: %s", pkg.NEVRA())
				}
				if pkg.Checksum.Type != "sha256" || len(pkg.Checksum.Value) != sha256.Size*2 {
					t.Fatalf("包 %s 的指纹异常: %+v", pkg.NEVRA(), pkg.Checksum)
				}
				if pkg.Size.Package <= 0 || pkg.Size.Installed <= 0 {
					t.Fatalf("包 %s 的大小异常: %+v", pkg.NEVRA(), pkg.Size)
				}
				if !strings.HasPrefix(pkg.DownloadURL, repo.url) ||
					!strings.HasSuffix(pkg.DownloadURL, pkg.Filename()) {
					t.Fatalf("包 %s 的下载地址异常: %q", pkg.NEVRA(), pkg.DownloadURL)
				}
				if want := pkg.Name + "-" + pkg.Version.String() + "." + pkg.Arch; pkg.NEVRA() != want {
					t.Fatalf("NEVRA 为 %q，期望 %q", pkg.NEVRA(), want)
				}
				if pkg.RepoID == "" || pkg.RepoURL == "" {
					t.Fatalf("包 %s 缺少仓库信息", pkg.NEVRA())
				}
				if len(pkg.Format.Requires) == 0 {
					t.Fatalf("包 %s 没有解析出任何依赖", pkg.NEVRA())
				}
			}

			if !strings.HasSuffix(latest.Version.Release, repo.distTag) {
				t.Errorf("最新版本 %s 的 release 不含 %s", latest.NEVRA(), repo.distTag)
			}
			for _, want := range repo.wantRequires {
				if !latest.Requires(want) {
					t.Errorf("%s 的依赖中缺少 %s", latest.NEVRA(), want)
				}
			}
			if latest.Time.Build.IsZero() || latest.Time.File.IsZero() {
				t.Errorf("%s 缺少构建/入库时间", latest.NEVRA())
			}
			if latest.Format.HeaderRange.End == 0 || latest.Format.SourceRPM == "" {
				t.Errorf("%s 缺少 rpm 头部信息: %+v", latest.NEVRA(), latest.Format)
			}
			if !latest.Provides(realRepoPackage) {
				t.Errorf("%s 未提供 %s 能力", latest.NEVRA(), realRepoPackage)
			}
			t.Logf("%s: 共 %d 个 docker-ce 包，最新 %s，大小 %d 字节，指纹 %s，依赖 %d 条，下载地址 %s",
				repo.name, len(pkgs), latest.NEVRA(), latest.Size.Package, latest.Checksum,
				len(latest.Format.Requires), latest.DownloadURL)
		})
	}
}

// TestRealReposRepoMD 用真实 repomd.xml 验证仓库索引解析。
func TestRealReposRepoMD(t *testing.T) {
	for _, repo := range realRepos() {
		t.Run(repo.name, func(t *testing.T) {
			repository := openRealRepo(t, repo)
			repomd := repository.RepoMD
			if repomd.Revision == "" {
				t.Fatal("repomd.xml 缺少 revision")
			}
			if _, err := strconv.ParseInt(repomd.Revision, 10, 64); err != nil {
				t.Errorf("revision %q 不是 Unix 时间戳: %v", repomd.Revision, err)
			}
			if _, ok := repomd.DataByType(DataTypePrimary); !ok {
				t.Fatalf("repomd.xml 中缺少 primary 元数据，类型为 %v", repomd.Types())
			}

			primary := repository.Primary
			if primary.Location.Href == "" || primary.Checksum.Value == "" || primary.OpenChecksum.Value == "" {
				t.Fatalf("primary 条目不完整: %+v", primary)
			}
			if got := CompressionOf(primary.Location.Href); got != repo.primaryCompression {
				t.Errorf("primary 压缩格式为 %q，期望 %q", got, repo.primaryCompression)
			}
			if primary.OpenSize <= primary.Size {
				t.Errorf("primary 解压后大小 %d 不应小于压缩大小 %d", primary.OpenSize, primary.Size)
			}
			if primary.Timestamp.IsZero() {
				t.Error("primary 缺少生成时间")
			}

			primaryURL, err := repository.PrimaryURL()
			if err != nil {
				t.Fatalf("PrimaryURL 失败: %v", err)
			}
			if !strings.HasPrefix(primaryURL, strings.TrimSuffix(repo.url, "/")+"/") {
				t.Errorf("primary 地址 %q 不在仓库地址下", primaryURL)
			}
			t.Logf("%s: revision=%s 元数据类型=%v primary=%s（%s）size=%d open-size=%d",
				repo.name, repomd.Revision, repomd.Types(), primary.Location.Href,
				CompressionOf(primary.Location.Href), primary.Size, primary.OpenSize)
		})
	}
}

// TestRealReposParsePrimaryStream 直接流式解析真实 primary.xml，覆盖完整元数据遍历。
func TestRealReposParsePrimaryStream(t *testing.T) {
	for _, repo := range realRepos() {
		t.Run(repo.name, func(t *testing.T) {
			data := loadRealRepoData(t, repo)
			reader, compressed, err := decompress(bytes.NewReader(data.primary))
			if err != nil {
				t.Fatalf("解压 primary 元数据失败: %v", err)
			}
			defer reader.Close()
			if !compressed {
				t.Errorf("%s 应当是压缩数据", data.primaryURL)
			}

			count, dependencies := 0, 0
			var sample Package
			err = ParsePrimary(reader, func(pkg *Package) error {
				count++
				if count == 1 {
					sample = *pkg
					dependencies = len(pkg.Format.Requires)
				}
				if pkg.Name == "" || pkg.Arch == "" || pkg.Version.Version == "" || pkg.Checksum.Value == "" {
					t.Fatalf("解析出的软件包字段不完整: %+v", pkg)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("ParsePrimary 失败: %v", err)
			}
			if count < repo.minPackages {
				t.Fatalf("流式解析出 %d 个软件包，少于预期的 %d 个", count, repo.minPackages)
			}
			if dependencies == 0 {
				t.Errorf("第一个软件包 %s 没有依赖", sample.NEVRA())
			}
			t.Logf("%s: 流式解析 %d 个软件包，第一个 %s（%d 条依赖）",
				repo.name, count, sample.NEVRA(), dependencies)
		})
	}
}

// TestRealReposDownloadURLIsReachable 验证元数据给出的下载地址真实可用。
func TestRealReposDownloadURLIsReachable(t *testing.T) {
	requireNetwork(t, realRepos()[0].url)
	httpClient := realRepoHTTPClient(2 * time.Minute)
	for _, repo := range realRepos() {
		t.Run(repo.name, func(t *testing.T) {
			ctx := context.Background()
			repository := openRealRepo(t, repo)
			latest, err := repository.FindPackage(ctx, Query{Name: realRepoPackage, Arch: "x86_64"})
			if err != nil {
				t.Fatalf("FindPackage 失败: %v", err)
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, latest.DownloadURL, nil)
			if err != nil {
				t.Fatalf("构造请求失败: %v", err)
			}
			// 只取前两个字节，确认地址有效且不需要下载整个 rpm。
			request.Header.Set("Range", "bytes=0-1")
			response, err := callWithRetry(t, latest.DownloadURL, func() (*http.Response, error) {
				return httpClient.Do(request)
			})
			if err != nil {
				skipIfNetworkUnavailable(t, latest.DownloadURL, err)
				t.Fatalf("下载 %s 失败: %v", latest.DownloadURL, err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
				t.Fatalf("下载地址 %s 返回 %s", latest.DownloadURL, response.Status)
			}
			t.Logf("%s: %s 可用（%s）", repo.name, latest.NEVRA(), response.Status)
		})
	}
}

// TestRealReposPackageChecksum 下载真实 rpm 包并核对元数据中的指纹，默认跳过。
// 执行方式：PKGREPO_FULL_DOWNLOAD=1 go test ./rpmrepo/ -run TestRealReposPackageChecksum -v
func TestRealReposPackageChecksum(t *testing.T) {
	if os.Getenv("PKGREPO_FULL_DOWNLOAD") != "1" {
		t.Skip("默认跳过完整下载，设置 PKGREPO_FULL_DOWNLOAD=1 开启")
	}
	requireNetwork(t, realRepos()[0].url)
	httpClient := realRepoHTTPClient(10 * time.Minute)
	for _, repo := range realRepos() {
		t.Run(repo.name, func(t *testing.T) {
			ctx := context.Background()
			repository := openRealRepo(t, repo)
			latest, err := repository.FindPackage(ctx, Query{Name: realRepoPackage, Arch: "x86_64"})
			if err != nil {
				t.Fatalf("FindPackage 失败: %v", err)
			}

			type downloadResult struct {
				written int64
				sha256  string
			}
			result, err := callWithRetry(t, latest.DownloadURL, func() (downloadResult, error) {
				response, err := httpClient.Get(latest.DownloadURL)
				if err != nil {
					return downloadResult{}, err
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusOK {
					return downloadResult{}, fmt.Errorf("rpmrepo: 请求 %s 返回 %s", latest.DownloadURL, response.Status)
				}
				hasher := sha256.New()
				written, err := io.Copy(hasher, response.Body)
				if err != nil {
					return downloadResult{}, err
				}
				return downloadResult{written: written, sha256: hex.EncodeToString(hasher.Sum(nil))}, nil
			})
			if err != nil {
				skipIfNetworkUnavailable(t, latest.DownloadURL, err)
				t.Fatalf("下载 %s 失败: %v", latest.DownloadURL, err)
			}
			if result.written != latest.Size.Package {
				t.Errorf("下载大小 %d 与元数据记录的 %d 不一致", result.written, latest.Size.Package)
			}
			if !strings.EqualFold(result.sha256, latest.Checksum.Value) {
				t.Errorf("指纹不一致：实际 %s，元数据 %s", result.sha256, latest.Checksum.Value)
			}
			t.Logf("%s: %s 下载 %d 字节，SHA256 %s 与元数据一致",
				repo.name, latest.NEVRA(), result.written, result.sha256)
		})
	}
}

// TestRealReposChecksumVerification 在真实数据上验证元数据校验和：正常通过、被篡改时报错。
func TestRealReposChecksumVerification(t *testing.T) {
	repo := realRepos()[0]
	repository := openRealRepo(t, repo) // 已开启 WithChecksumVerification(true)
	if len(findRealPackages(t, repository, Query{Name: realRepoPackage})) == 0 {
		t.Fatal("校验通过但查询不到软件包")
	}

	data := loadRealRepoData(t, repo)
	tampered := New(WithFetcher(&fixtureFetcher{data: data, tamper: zeroOpenChecksum}), WithChecksumVerification(true))
	_, err := tampered.ListPackages(context.Background(), repo.url, realRepoPackage)
	if err == nil {
		t.Fatal("open-checksum 被篡改后仍然校验通过")
	}
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("错误为 %v，期望 ErrChecksumMismatch", err)
	}
	t.Logf("%s: 篡改 open-checksum 后按预期返回错误: %v", repo.name, err)
}

// TestRealReposMissingPrimary 在真实 repomd.xml 上删除 primary 条目，验证错误分支。
func TestRealReposMissingPrimary(t *testing.T) {
	repo := realRepos()[0]
	data := loadRealRepoData(t, repo)
	client := New(WithFetcher(&fixtureFetcher{data: data, tamper: dropPrimaryData}))
	if _, err := client.Open(context.Background(), repo.url); !errors.Is(err, ErrPrimaryNotFound) {
		t.Fatalf("错误为 %v，期望 ErrPrimaryNotFound", err)
	}
}

// TestRealReposNotFound 验证不存在的仓库地址会返回清晰的错误。
func TestRealReposNotFound(t *testing.T) {
	missingURL := dockerRepoBase() + "/linux/centos/7/x86_64/does-not-exist/"
	requireNetwork(t, missingURL)
	_, err := callWithRetry(t, missingURL, func() (*Repository, error) {
		return Open(context.Background(), missingURL, WithHTTPClient(realRepoHTTPClient(2*time.Minute)))
	})
	if err == nil {
		t.Fatal("期望返回错误")
	}
	skipIfNetworkUnavailable(t, missingURL, err)
	if !errors.Is(err, ErrNotRepository) {
		t.Errorf("错误为 %v，期望包装 ErrNotRepository", err)
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound {
		t.Errorf("错误为 %v，期望包含 404 的 HTTPError", err)
	}
}

// zeroOpenChecksum 把 repomd.xml 中记录的 open-checksum 全部改成 0。
func zeroOpenChecksum(body []byte) []byte {
	pattern := regexp.MustCompile(`(<open-checksum type="[a-zA-Z0-9]+">)[0-9a-fA-F]+(</open-checksum>)`)
	return pattern.ReplaceAll(body, []byte("${1}"+strings.Repeat("0", 64)+"${2}"))
}

// dropPrimaryData 从 repomd.xml 中删除 primary 元数据条目。
func dropPrimaryData(body []byte) []byte {
	pattern := regexp.MustCompile(`(?s)\s*<data type="primary">.*?</data>`)
	return pattern.ReplaceAll(body, nil)
}
