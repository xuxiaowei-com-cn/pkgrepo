package rpmrepo

import (
	"context"
	"strings"
	"testing"
	"time"
)

// containerdPackage 是测试使用的软件名称。
//
// Docker 官方源为 el7/el8/el9/el10 都提供了 containerd.io，
// 适合用来验证"仓库地址 + 软件名称 → 软件包列表"在不同版本 CentOS 上的表现。
const containerdPackage = "containerd.io"

// TestContainerd 在 CentOS 7/8/9/10 四个版本的 Docker 官方源上查询 containerd.io：
// 既验证"仓库地址 + 软件名称 → 软件包列表"这条主链路，也验证复用同一份元数据的查询方式。
//
// 国内或网络受限时，可以用环境变量指向同一份仓库数据的镜像：
//
//	PKGREPO_DOCKER_BASE=https://mirrors.aliyun.com/docker-ce go test ./rpmrepo -run TestContainerd -v
func TestContainerd(t *testing.T) {
	ctx := context.Background()

	for _, repo := range realRepos() {
		t.Run(repo.name, func(t *testing.T) {
			// 1. 按名称列出全部软件包（名称支持 * ? [abc] 通配符）
			pkgs := containerdPackages(t, ctx, repo)
			if len(pkgs) == 0 {
				t.Fatalf("%s 仓库中找不到 %s", repo.name, containerdPackage)
			}
			for i := range pkgs {
				pkg := &pkgs[i]
				t.Logf("%s\t%d\t%s", pkg.NEVRA(), pkg.Size.Package, pkg.DownloadURL)
				if pkg.Name != containerdPackage {
					t.Fatalf("查询结果混入了其他包: %s", pkg.NEVRA())
				}
			}

			// 2. 打开仓库后复用同一份元数据：只取最新版本
			repository := containerdRepository(t, ctx, repo)
			latest := findLatestContainerd(t, ctx, repository)
			t.Logf("最新版本: %s %s", latest.NEVRA(), latest.Checksum)
			assertContainerdPackage(t, repo, latest)

			// 3. 复用同一份元数据做更精细的查询：限定架构，按版本降序取最新
			x86 := findContainerd(t, ctx, repository, Query{
				Name: containerdPackage,
				Arch: "x86_64",
			})
			t.Logf("x86_64 最新包: %s", x86[0].DownloadURL)
			if x86[0].NEVRA() != latest.NEVRA() {
				t.Errorf("按架构查询的最新包为 %s，与 Latest 查询的 %s 不一致", x86[0].NEVRA(), latest.NEVRA())
			}
			for i := range x86 {
				if x86[i].Version.Compare(latest.Version) > 0 {
					t.Errorf("排序结果有误：%s 比 %s 更新", x86[i].NEVRA(), latest.NEVRA())
				}
			}

			// 4. 不带版本条件时，列表里也应能找到最新版本
			if !containsPackage(pkgs, latest.NEVRA()) {
				t.Errorf("列表查询中没有 %s", latest.NEVRA())
			}
		})
	}
}

// containerdPackages 按名称列出仓库中的全部 containerd.io 包。
func containerdPackages(t *testing.T, ctx context.Context, repo realRepo) []Package {
	t.Helper()
	requireNetwork(t, repo.url)
	pkgs, err := callWithRetry(t, repo.url, func() ([]Package, error) {
		return ListPackages(ctx, repo.url, containerdPackage, WithHTTPClient(realRepoHTTPClient(2*time.Minute)))
	})
	if err != nil {
		skipIfNetworkUnavailable(t, repo.url, err)
		t.Fatalf("查询 %s 的 %s 失败: %v", repo.name, containerdPackage, err)
	}
	return pkgs
}

// containerdRepository 打开仓库，后续多个查询复用同一份元数据。
func containerdRepository(t *testing.T, ctx context.Context, repo realRepo) *Repository {
	t.Helper()
	requireNetwork(t, repo.url)
	repository, err := callWithRetry(t, repo.url, func() (*Repository, error) {
		return Open(ctx, repo.url, WithHTTPClient(realRepoHTTPClient(2*time.Minute)))
	})
	if err != nil {
		skipIfNetworkUnavailable(t, repo.url, err)
		t.Fatalf("打开 %s 仓库失败: %v", repo.name, err)
	}
	return repository
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
		Arch:   "x86_64",
		Latest: true,
	})
	if err != nil {
		t.Fatalf("查询 %s 的最新版本失败: %v", repository.URL, err)
	}
	return pkg
}

// assertContainerdPackage 校验真实仓库返回的包信息是否自洽。
func assertContainerdPackage(t *testing.T, repo realRepo, pkg *Package) {
	t.Helper()
	distro := strings.TrimPrefix(repo.name, "centos")

	if pkg.Name != containerdPackage || pkg.Arch != "x86_64" {
		t.Fatalf("查询结果混入了其他包: %s", pkg.NEVRA())
	}
	if pkg.Checksum.Type != "sha256" || pkg.Checksum.Value == "" {
		t.Fatalf("包 %s 的指纹异常: %+v", pkg.NEVRA(), pkg.Checksum)
	}
	if pkg.Size.Package <= 0 || pkg.Size.Installed <= 0 {
		t.Fatalf("包 %s 的大小异常: %+v", pkg.NEVRA(), pkg.Size)
	}
	if !strings.Contains(pkg.DownloadURL, "/centos/"+distro+"/") {
		t.Errorf("包 %s 的下载地址不属于 CentOS %s: %q", pkg.NEVRA(), distro, pkg.DownloadURL)
	}
	if !strings.HasSuffix(pkg.DownloadURL, pkg.Filename()) {
		t.Errorf("包 %s 的下载地址异常: %q", pkg.NEVRA(), pkg.DownloadURL)
	}
	if want := pkg.Name + "-" + pkg.Version.String() + "." + pkg.Arch; pkg.NEVRA() != want {
		t.Errorf("NEVRA 为 %q，期望 %q", pkg.NEVRA(), want)
	}
	if len(pkg.Format.Requires) == 0 {
		t.Errorf("包 %s 没有解析出任何依赖", pkg.NEVRA())
	}
	// 版本号里应带上发行版标记（el7、el8、el9、el10）。
	if !strings.HasSuffix(pkg.Version.Release, repo.distTag) {
		t.Errorf("包 %s 的 release 不含 %s", pkg.NEVRA(), repo.distTag)
	}
	if pkg.Time.Build.IsZero() || pkg.Time.File.IsZero() {
		t.Errorf("包 %s 缺少构建/入库时间", pkg.NEVRA())
	}
}

// containsPackage 判断列表中是否存在指定 NEVRA 的包。
func containsPackage(pkgs []Package, nevra string) bool {
	for i := range pkgs {
		if pkgs[i].NEVRA() == nevra {
			return true
		}
	}
	return false
}
