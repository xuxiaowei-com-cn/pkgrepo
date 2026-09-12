package debrepo

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// dockerRepoBase 返回 Docker 的 deb 软件源地址。
//
// 地址可以用环境变量覆盖，国内本地开发可以指向镜像站：
//
//	PKGREPO_DOCKER_BASE=https://mirrors.aliyun.com/docker-ce go test ./...
func dockerRepoBase() string {
	if base := strings.TrimSpace(os.Getenv("PKGREPO_DOCKER_BASE")); base != "" {
		return strings.TrimSuffix(base, "/")
	}
	return "https://download.docker.com"
}

// realRepo 描述一个用于端到端验证的真实 deb 仓库。
type realRepo struct {
	name string
	// distro 是发行版目录名（debian、ubuntu）。
	distro string
	// url 是发行版根地址，例如 https://download.docker.com/linux/debian。
	url         string
	suite       string
	component   string
	arch        string
	distTag     string
	minPackages int
}

// realRepos 返回用于端到端验证的真实仓库：Docker 官方的 Debian 与 Ubuntu 源。
// 这些仓库覆盖 Debian 与 Ubuntu 两种发行版、四种 suite，并且都使用 5: 纪元版本号。
func realRepos() []realRepo {
	base := dockerRepoBase()
	repo := func(name, distro, suite, distTag string) realRepo {
		return realRepo{
			name:        name,
			distro:      distro,
			url:         base + "/linux/" + distro,
			suite:       suite,
			component:   "stable",
			arch:        "amd64",
			distTag:     distTag,
			minPackages: 100,
		}
	}
	return []realRepo{
		repo("debian-trixie", "debian", "trixie", "debian.13"),
		repo("debian-bookworm", "debian", "bookworm", "debian.12"),
		repo("debian-bullseye", "debian", "bullseye", "debian.11"),
		repo("ubuntu-resolute", "ubuntu", "resolute", "ubuntu.26.04"),
		repo("ubuntu-noble", "ubuntu", "noble", "ubuntu.24.04"),
		repo("ubuntu-jammy", "ubuntu", "jammy", "ubuntu.22.04"),
	}
}

// realRepoPackage 是端到端测试使用的软件名称。
const realRepoPackage = "docker-ce"

// networkUnavailable 记录本次测试运行是否已确认连不上外网，
// 避免离线环境下每个子测试都反复等待 DNS 超时。
var networkUnavailable atomic.Bool

// realRepoClient 返回测试用的 HTTP 客户端（固定使用 IPv4，链路更稳定）。
func realRepoClient(timeout time.Duration) *http.Client {
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

// isOfflineError 判断错误是否表示"当前环境连不上外网"（DNS 解析失败、网络不可达等）。
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
	var zero T
	var err error
	for attempt := 0; attempt < 6; attempt++ {
		zero, err = action()
		if err == nil {
			return zero, nil
		}
		if isOfflineError(err) {
			networkUnavailable.Store(true)
			t.Logf("访问 %s 连续网络失败: %v", repoURL, err)
			t.Skipf("跳过：网络不可用，无法访问真实仓库 %s（%v）", repoURL, err)
		}
		var httpErr *HTTPError
		if errors.As(err, &httpErr) {
			return zero, err
		}
		if attempt < 5 {
			time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
		}
	}
	return zero, err
}

// cachingFetcher 在内存中缓存已下载的文件，避免同一份索引被反复下载。
// 真实仓库的索引可能有几十 MB，测试中会多次扫描，缓存能把网络开销降到最低。
type cachingFetcher struct {
	base Fetcher

	mu    sync.Mutex
	files map[string][]byte
}

func newCachingFetcher(base Fetcher) *cachingFetcher {
	return &cachingFetcher{base: base, files: map[string][]byte{}}
}

func (f *cachingFetcher) Open(ctx context.Context, rawURL string) (io.ReadCloser, error) {
	f.mu.Lock()
	data, ok := f.files[rawURL]
	f.mu.Unlock()
	if ok {
		return io.NopCloser(bytes.NewReader(data)), nil
	}
	body, err := f.base.Open(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	data, err = io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.files[rawURL] = data
	f.mu.Unlock()
	return io.NopCloser(bytes.NewReader(data)), nil
}

// repoCache 缓存已打开的仓库，避免每个子测试重复下载同一份索引。
type repoCache struct {
	mu      sync.Mutex
	entries map[string]*Repository
}

var openRealRepos = &repoCache{entries: map[string]*Repository{}}

func (c *repoCache) load(key string, open func() (*Repository, error)) (*Repository, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if repo, ok := c.entries[key]; ok {
		return repo, nil
	}
	repo, err := open()
	if err != nil {
		return nil, err
	}
	c.entries[key] = repo
	return repo, nil
}

// openRealRepo 打开真实仓库（开启校验和校验），并在本次测试运行内复用。
func openRealRepo(t *testing.T, repo realRepo) *Repository {
	t.Helper()
	requireNetwork(t, repo.url)
	key := repo.name + "|" + repo.suite + "|" + repo.arch
	opened, err := openRealRepos.load(key, func() (*Repository, error) {
		fetcher := newCachingFetcher(&HTTPFetcher{Client: realRepoClient(4 * time.Minute)})
		client := New(
			WithFetcher(fetcher),
			WithSuite(repo.suite),
			WithComponent(repo.component),
			WithArchitecture(repo.arch),
			WithChecksumVerification(true),
		)
		return callWithRetry(t, repo.url, func() (*Repository, error) {
			return client.Open(context.Background(), repo.url)
		})
	})
	if err != nil {
		if isOfflineError(err) {
			networkUnavailable.Store(true)
			t.Skipf("跳过：网络不可用，无法访问真实仓库 %s（%v）", repo.url, err)
		}
		t.Fatalf("打开真实仓库 %s 失败: %v", repo.url, err)
	}
	return opened
}

func TestRealReposReleaseMetadata(t *testing.T) {
	for _, repo := range realRepos() {
		repo := repo
		t.Run(repo.name, func(t *testing.T) {
			opened := openRealRepo(t, repo)
			release := opened.Release
			if release == nil {
				t.Fatal("Release 为空")
			}
			if release.Origin == "" || release.Label == "" {
				t.Errorf("Origin/Label = %q/%q", release.Origin, release.Label)
			}
			if release.Suite != repo.suite {
				t.Errorf("Suite = %q，期望 %q", release.Suite, repo.suite)
			}
			if release.Date.IsZero() {
				t.Error("Date 应当被解析")
			}
			if !containsString(release.Components, repo.component) {
				t.Errorf("Components = %v，期望包含 %q", release.Components, repo.component)
			}
			if !containsString(release.Architectures, repo.arch) {
				t.Errorf("Architectures = %v，期望包含 %q", release.Architectures, repo.arch)
			}
			if !strings.HasPrefix(opened.ReleaseURL, repo.url+"/dists/"+repo.suite+"/") {
				t.Errorf("ReleaseURL = %q", opened.ReleaseURL)
			}
			t.Logf("%s: %s（InRelease: %t），Architectures=%v Components=%v",
				repo.name, opened.ReleaseURL, opened.InRelease, release.Architectures, release.Components)
		})
	}
}

func TestRealReposIndexes(t *testing.T) {
	for _, repo := range realRepos() {
		repo := repo
		t.Run(repo.name, func(t *testing.T) {
			opened := openRealRepo(t, repo)
			indexes := opened.Indexes()
			if len(indexes) == 0 {
				t.Fatal("没有发现任何 Packages 索引")
			}
			index := indexes[0]
			if index.Component != repo.component || index.Architecture != repo.arch {
				t.Errorf("索引 = %+v", index)
			}
			if !index.FromRelease || !strings.Contains(index.Path, "/Packages") {
				t.Errorf("索引路径 = %q", index.Path)
			}
			if index.Size <= 0 || index.Checksum.Value == "" {
				t.Errorf("索引缺少大小或校验值: %+v", index)
			}
			releasePath := strings.TrimPrefix(index.Path, "dists/"+opened.Suite+"/")
			entry, ok := opened.Release.Lookup(releasePath)
			if !ok {
				t.Fatalf("Release 中找不到索引 %q", releasePath)
			}
			if entry.Size != index.Size || !entry.Checksum.Equal(index.Checksum) {
				t.Errorf("Release 中的索引信息 = %+v，索引 = %+v", entry, index)
			}
			switch entry.Checksum.Type {
			case "sha512", "sha256":
			default:
				t.Errorf("索引校验算法 = %q", entry.Checksum.Type)
			}
			t.Logf("%s: %s（%s，%d 字节，候选 %d 个）", repo.name, index.Path,
				CompressionOf(index.Path), index.Size, len(index.Candidates))
		})
	}
}

func TestRealReposListPackages(t *testing.T) {
	ctx := context.Background()
	for _, repo := range realRepos() {
		repo := repo
		t.Run(repo.name, func(t *testing.T) {
			opened := openRealRepo(t, repo)
			pkgs, err := opened.FindPackages(ctx, Query{Name: realRepoPackage})
			if err != nil {
				t.Fatalf("查询 %s 失败: %v", realRepoPackage, err)
			}
			if len(pkgs) < 5 {
				t.Fatalf("%s 只找到 %d 个版本，期望至少 5 个", realRepoPackage, len(pkgs))
			}
			// 默认排序：名称升序、版本降序，因此最新版本在最前。
			for i := 1; i < len(pkgs); i++ {
				if CompareVersions(pkgs[i-1].Version.String(), pkgs[i].Version.String()) < 0 {
					t.Errorf("版本未按降序排列: %s < %s",
						pkgs[i-1].Version.String(), pkgs[i].Version.String())
				}
			}
			latest := pkgs[0]
			if latest.Version.Epoch != 5 {
				t.Errorf("最新版本 %s 的 epoch = %d，期望 5", latest.Version.String(), latest.Version.Epoch)
			}
			if !strings.Contains(latest.Version.String(), repo.distTag) {
				t.Errorf("最新版本 %s 不包含发行版标记 %q", latest.Version.String(), repo.distTag)
			}
			if latest.Component != repo.component || latest.Suite != repo.suite {
				t.Errorf("包的上下文 = %s/%s，期望 %s/%s",
					latest.Suite, latest.Component, repo.suite, repo.component)
			}
			if !strings.HasPrefix(latest.DownloadURL, repo.url+"/dists/"+repo.suite+"/pool/") ||
				!strings.HasSuffix(latest.DownloadURL, ".deb") {
				t.Errorf("DownloadURL = %q", latest.DownloadURL)
			}
			if latest.Size.File <= 0 || latest.Size.Installed <= 0 {
				t.Errorf("大小异常: %+v", latest.Size)
			}
			if checksum, ok := latest.Checksum(); !ok ||
				(checksum.Type != "sha256" && checksum.Type != "sha512") {
				t.Errorf("指纹 = %+v, %t", checksum, ok)
			}
			if latest.Section == "" || latest.Priority == "" || latest.Description.Synopsis == "" {
				t.Errorf("描述信息缺失: %+v", latest)
			}
			if !latest.DependsOn("containerd.io") || !latest.DependsOn("libc6") {
				t.Errorf("%s 的依赖解析异常: %v", realRepoPackage, latest.Depends)
			}
			found, err := opened.FindPackage(ctx, Query{Name: realRepoPackage})
			if err != nil {
				t.Fatalf("FindPackage 失败: %v", err)
			}
			if found.Version.String() != latest.Version.String() {
				t.Errorf("FindPackage = %s，期望 %s", found.Version.String(), latest.Version.String())
			}
			t.Logf("%s: %d 个版本，最新 %s（%d 字节）", repo.name, len(pkgs),
				latest.Version.String(), latest.Size.File)
		})
	}
}

func TestRealReposPackageCount(t *testing.T) {
	for _, repo := range realRepos() {
		repo := repo
		t.Run(repo.name, func(t *testing.T) {
			opened := openRealRepo(t, repo)
			index := opened.Indexes()[0]
			// 只读取首选候选，避免下载未使用的压缩格式。
			index.Candidates = index.Candidates[:1]
			body, err := opened.openIndex(context.Background(), index)
			if err != nil {
				t.Fatalf("读取索引失败: %v", err)
			}
			defer body.Close()
			total := 0
			if err := ParsePackages(body, func(*Package) error {
				total++
				return nil
			}); err != nil {
				t.Fatalf("解析索引失败: %v", err)
			}
			if total < repo.minPackages {
				t.Errorf("索引中的包数量 = %d，期望至少 %d", total, repo.minPackages)
			}
		})
	}
}

// TestRealReposDownloadReachable 检查索引中给出的下载地址真实可达。
func TestRealReposDownloadReachable(t *testing.T) {
	ctx := context.Background()
	for _, repo := range realRepos() {
		repo := repo
		t.Run(repo.name, func(t *testing.T) {
			opened := openRealRepo(t, repo)
			pkg, err := opened.FindPackage(ctx, Query{Name: realRepoPackage})
			if err != nil {
				t.Fatalf("查找 %s 失败: %v", realRepoPackage, err)
			}
			// 镜像站偶发连接重置，这里重试几次再判定。
			size, err := callWithRetry(t, pkg.DownloadURL, func() (int64, error) {
				client := realRepoClient(2 * time.Minute)
				req, err := http.NewRequestWithContext(ctx, http.MethodHead, pkg.DownloadURL, nil)
				if err != nil {
					return 0, err
				}
				req.Header.Set("User-Agent", defaultUserAgent)
				resp, err := client.Do(req)
				if err != nil {
					return 0, err
				}
				defer resp.Body.Close()
				if resp.StatusCode < 200 || resp.StatusCode > 299 {
					return 0, fmt.Errorf("HEAD %s = %s", pkg.DownloadURL, resp.Status)
				}
				return resp.ContentLength, nil
			})
			if err != nil {
				t.Fatalf("请求失败: %v", err)
			}
			if size > 0 && size != pkg.Size.File {
				t.Errorf("远端大小 %d 与索引中的 %d 不一致", size, pkg.Size.File)
			}
		})
	}
}

// TestRealReposPackageChecksum 完整下载一个 .deb 并核对指纹（默认跳过，耗时较长）。
func TestRealReposPackageChecksum(t *testing.T) {
	if os.Getenv("PKGREPO_FULL_DOWNLOAD") == "" {
		t.Skip("默认跳过完整下载测试，设置 PKGREPO_FULL_DOWNLOAD=1 后启用")
	}
	ctx := context.Background()
	for _, repo := range realRepos() {
		repo := repo
		t.Run(repo.name, func(t *testing.T) {
			opened := openRealRepo(t, repo)
			pkg, err := opened.FindPackage(ctx, Query{Name: realRepoPackage})
			if err != nil {
				t.Fatalf("查找 %s 失败: %v", realRepoPackage, err)
			}
			expected, ok := pkg.Checksums.Strongest()
			if !ok {
				t.Fatalf("索引中没有指纹: %+v", pkg.Checksums)
			}
			client := realRepoClient(10 * time.Minute)
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, pkg.DownloadURL, nil)
			if err != nil {
				t.Fatalf("构造请求失败: %v", err)
			}
			req.Header.Set("User-Agent", defaultUserAgent)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("下载失败: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("下载 %s = %s", pkg.DownloadURL, resp.Status)
			}
			hasher, err := newHash(expected.Type)
			if err != nil {
				t.Fatalf("不支持的算法 %q: %v", expected.Type, err)
			}
			size, err := io.Copy(hasher, resp.Body)
			if err != nil {
				t.Fatalf("读取响应失败: %v", err)
			}
			if size != pkg.Size.File {
				t.Errorf("下载大小 = %d，期望 %d", size, pkg.Size.File)
			}
			actual := hex.EncodeToString(hasher.Sum(nil))
			if !strings.EqualFold(actual, expected.Value) {
				t.Errorf("指纹不匹配: 期望 %s，实际 %s", expected.Value, actual)
			}
		})
	}
}

// TestUpstreamDebianRepository 验证 Debian 官方源特有的能力：
// binary-all 索引（Debian 13 起架构无关包只出现在该索引）与 Sources 源码索引。
//
// 该测试默认跳过（官方源在国内较慢），设置镜像地址后启用：
//
//	PKGREPO_DEBIAN_BASE=https://deb.debian.org/debian go test ./debrepo/ -run Upstream -v
func TestUpstreamDebianRepository(t *testing.T) {
	base := strings.TrimSuffix(strings.TrimSpace(os.Getenv("PKGREPO_DEBIAN_BASE")), "/")
	if base == "" {
		t.Skip("默认跳过官方源测试，设置 PKGREPO_DEBIAN_BASE 后启用（例如 https://deb.debian.org/debian）")
	}
	fetcher := newCachingFetcher(&HTTPFetcher{Client: realRepoClient(4 * time.Minute)})
	client := New(
		WithFetcher(fetcher),
		WithSuite("stable"),
		WithComponent("main"),
		WithArchitecture("amd64"),
		WithChecksumVerification(true),
	)
	ctx := context.Background()
	repo, err := callWithRetry(t, base, func() (*Repository, error) {
		return client.Open(ctx, base)
	})
	if err != nil {
		if isOfflineError(err) {
			networkUnavailable.Store(true)
			t.Skipf("跳过：网络不可用（%v）", err)
		}
		t.Fatalf("打开 Debian 源失败: %v", err)
	}
	if len(repo.SourceIndexes()) == 0 {
		t.Error("Debian 官方源应当提供 Sources 索引")
	}
	// binary-all：架构无关的包（Architecture: all）。
	pkg, err := repo.FindPackage(ctx, Query{Name: "ca-certificates"})
	if err != nil {
		t.Fatalf("查找 ca-certificates 失败: %v", err)
	}
	if pkg.Architecture != "all" {
		t.Errorf("Architecture = %q，期望 all", pkg.Architecture)
	}
	// Sources：源码包与 .dsc 文件。
	source, err := repo.FindSource(ctx, SourceQuery{Name: "nginx"})
	if err != nil {
		t.Fatalf("查找源码包失败: %v", err)
	}
	dsc, ok := source.DSC()
	if !ok {
		t.Fatal("源码包中没有 .dsc 文件")
	}
	dscURL, err := source.FileURL(dsc.Path)
	if err != nil {
		t.Fatalf("FileURL 失败: %v", err)
	}
	if !strings.HasPrefix(dscURL, base+"/pool/") || !strings.HasSuffix(dscURL, ".dsc") {
		t.Errorf("dsc 地址 = %q", dscURL)
	}
	t.Logf("Debian %s：ca-certificates %s（all），源码包 nginx %s",
		repo.Suite, pkg.Version.String(), source.Version.String())
}
