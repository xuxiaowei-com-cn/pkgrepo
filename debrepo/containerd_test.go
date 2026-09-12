package debrepo

import (
	"context"
	"strings"
	"testing"
	"time"
)

// containerdPackage 是测试使用的软件名称。
//
// Docker 的 deb 源为 Debian/Ubuntu 的多个 suite 都提供了 containerd.io，
// 适合用来验证"仓库地址 + 软件名称 → 软件包列表"这条主链路在 deb 仓库上的表现。
const containerdPackage = "containerd.io"

// TestContainerd 在 Docker 的 Debian/Ubuntu deb 源上查询 containerd.io：
// 既验证"仓库地址 + 软件名称 → 软件包列表"这条主链路，也验证复用同一份元数据的查询方式。
//
// 国内或网络受限时，可以用环境变量指向同一份仓库数据的镜像：
//
//	PKGREPO_DOCKER_BASE=https://mirrors.aliyun.com/docker-ce go test ./debrepo -run TestContainerd -v
func TestContainerd(t *testing.T) {
	ctx := context.Background()

	for _, repo := range realRepos() {
		repo := repo
		t.Run(repo.name, func(t *testing.T) {
			// 1. 按名称列出全部软件包（名称支持 * ? [abc] 通配符）
			pkgs := containerdPackages(t, ctx, repo)
			if len(pkgs) == 0 {
				t.Fatalf("%s 仓库中找不到 %s", repo.name, containerdPackage)
			}
			for i := range pkgs {
				pkg := &pkgs[i]
				checksum, _ := pkg.Checksum()
				t.Logf("%s\t%d\t%s\t%s", pkg.ID(), pkg.Size.File, checksum, pkg.DownloadURL)
				if pkg.Name != containerdPackage {
					t.Fatalf("查询结果混入了其他包: %s", pkg.ID())
				}
			}

			// 2. 打开仓库后复用同一份元数据：只取最新版本
			repository := openRealRepo(t, repo)
			latest := findLatestContainerd(t, ctx, repository)
			checksum, _ := latest.Checksum()
			t.Logf("最新版本: %s %s", latest.ID(), checksum)
			assertContainerdPackage(t, repo, latest)

			// 3. 复用同一份元数据做更精细的查询：限定架构，按版本降序取最新
			amd64 := findContainerd(t, ctx, repository, Query{
				Name: containerdPackage,
				Arch: repo.arch,
			})
			t.Logf("%s 最新包: %s", repo.arch, amd64[0].DownloadURL)
			if amd64[0].ID() != latest.ID() {
				t.Errorf("按架构查询的最新包为 %s，与 Latest 查询的 %s 不一致", amd64[0].ID(), latest.ID())
			}
			for i := range amd64 {
				if amd64[i].Version.Compare(latest.Version) > 0 {
					t.Errorf("排序结果有误：%s 比 %s 更新", amd64[i].ID(), latest.ID())
				}
			}

			// 4. 不带版本条件时，列表里也应能找到最新版本
			if !containsPackage(pkgs, latest.ID()) {
				t.Errorf("列表查询中没有 %s", latest.ID())
			}
		})
	}
}

// containerdPackages 按名称列出仓库中的全部 containerd.io 包。
func containerdPackages(t *testing.T, ctx context.Context, repo realRepo) []Package {
	t.Helper()
	requireNetwork(t, repo.url)
	pkgs, err := callWithRetry(t, repo.url, func() ([]Package, error) {
		return ListPackages(ctx, repo.url, containerdPackage,
			WithFetcher(newCachingFetcher(&HTTPFetcher{Client: realRepoClient(2 * time.Minute)})),
			WithSuite(repo.suite),
			WithComponent(repo.component),
			WithArchitecture(repo.arch),
		)
	})
	if err != nil {
		if isOfflineError(err) {
			networkUnavailable.Store(true)
			t.Skipf("跳过：网络不可用，无法访问 %s（%v）", repo.url, err)
		}
		t.Fatalf("查询 %s 的 %s 失败: %v", repo.name, containerdPackage, err)
	}
	return pkgs
}

// findContainerd 在已打开的仓库上查询，结果为空时直接失败。
func findContainerd(t *testing.T, ctx context.Context, repository *Repository, q Query) []Package {
	t.Helper()
	pkgs, err := repository.FindPackages(ctx, q)
	if err != nil {
		t.Fatalf("查询 %+v 失败: %v", q, err)
	}
	if len(pkgs) == 0 {
		t.Fatalf("查询 %+v 没有结果", q)
	}
	return pkgs
}

// findLatestContainerd 返回仓库中版本最新的 containerd.io 包。
func findLatestContainerd(t *testing.T, ctx context.Context, repository *Repository) *Package {
	t.Helper()
	pkg, err := repository.FindPackage(ctx, Query{
		Name:   containerdPackage,
		Arch:   repository.Architectures[0],
		Latest: true,
	})
	if err != nil {
		t.Fatalf("查询 %s 的最新版本失败: %v", repository.URL, err)
	}
	return pkg
}

// assertContainerdPackage 校验真实仓库返回的 deb 包信息是否自洽。
func assertContainerdPackage(t *testing.T, repo realRepo, pkg *Package) {
	t.Helper()
	if pkg.Name != containerdPackage || pkg.Architecture != repo.arch {
		t.Fatalf("查询结果混入了其他包: %s", pkg.ID())
	}
	if pkg.Checksums.SHA256 == "" {
		t.Fatalf("包 %s 缺少 sha256 指纹: %+v", pkg.ID(), pkg.Checksums)
	}
	if pkg.Size.File <= 0 || pkg.Size.Installed <= 0 {
		t.Fatalf("包 %s 的大小异常: %+v", pkg.ID(), pkg.Size)
	}
	// deb 包的下载地址由仓库根 + Filename 组成（Docker 的 Filename 带 dists/<suite> 前缀）。
	prefix := repo.url + "/dists/" + repo.suite + "/pool/"
	if !strings.HasPrefix(pkg.DownloadURL, prefix) {
		t.Errorf("包 %s 的下载地址不属于 %s %s: %q", pkg.ID(), repo.distro, repo.suite, pkg.DownloadURL)
	}
	if !strings.HasSuffix(pkg.DownloadURL, pkg.BaseFilename()) {
		t.Errorf("包 %s 的下载地址异常: %q", pkg.ID(), pkg.DownloadURL)
	}
	if want := pkg.Name + "_" + pkg.Version.String() + "_" + pkg.Architecture; pkg.ID() != want {
		t.Errorf("标识为 %q，期望 %q", pkg.ID(), want)
	}
	if pkg.Suite != repo.suite || pkg.Component != repo.component {
		t.Errorf("包 %s 的上下文为 %s/%s，期望 %s/%s",
			pkg.ID(), pkg.Suite, pkg.Component, repo.suite, repo.component)
	}
	if len(pkg.Depends) == 0 {
		t.Errorf("包 %s 没有解析出任何依赖", pkg.ID())
	}
	if !pkg.DependsOn("libc6") {
		t.Errorf("包 %s 的依赖中没有 libc6: %v", pkg.ID(), pkg.Depends)
	}
	if pkg.Section == "" || pkg.Priority == "" {
		t.Errorf("包 %s 缺少 Section/Priority: %+v", pkg.ID(), pkg)
	}
	if pkg.Description.Synopsis == "" {
		t.Errorf("包 %s 缺少描述", pkg.ID())
	}
}

// containsPackage 判断列表中是否存在指定标识的包。
func containsPackage(pkgs []Package, id string) bool {
	for i := range pkgs {
		if pkgs[i].ID() == id {
			return true
		}
	}
	return false
}
