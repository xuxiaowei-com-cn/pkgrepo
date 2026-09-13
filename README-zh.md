# pkgrepo

[English](README.md) | **简体中文**

解析 rpm、deb 软件仓库的 Go SDK。

当前已完成：

- **RPM（YUM/DNF）仓库**：`rpmrepo` 读取 `repodata/repomd.xml` + `primary.xml`，
  给定仓库地址与软件名称即可返回软件包列表，包含下载链接、大小、指纹（校验和）、依赖等元数据。
- **Debian/Ubuntu（apt）仓库**：`debrepo` 读取 `dists/<suite>/InRelease`（或 `Release`）
  以及 `Packages`/`Sources` 索引，返回同样的元数据，并支持 `by-hash` 回退、索引校验和校验、
  Debian 版本比较等能力。

```go
// rpm
pkgs, err := rpmrepo.ListPackages(ctx,
    "https://download.docker.com/linux/centos/7/x86_64/stable", "docker-ce")
fmt.Println(pkgs[0].NEVRA(), pkgs[0].Size.Package, pkgs[0].Checksum)

// deb
debs, err := debrepo.ListPackages(ctx,
    "https://download.docker.com/linux/debian", "docker-ce",
    debrepo.WithSuite("bookworm"), debrepo.WithArchitecture("amd64"))
fmt.Println(debs[0].ID(), debs[0].Size.File, debs[0].DownloadURL)
```

## RPM 仓库（rpmrepo）

给定仓库地址与软件名称，返回软件包列表，包含下载链接、大小、指纹（校验和）、依赖关系等元数据。

```go
pkgs, err := rpmrepo.ListPackages(ctx,
    "https://download.docker.com/linux/centos/7/x86_64/stable", "docker-ce")
for _, pkg := range pkgs {
    fmt.Println(pkg.NEVRA(), pkg.Size.Package, pkg.Checksum, pkg.DownloadURL)
}
```

### 快速开始

```bash
go get github.com/xuxiaowei-com-cn/pkgrepo/rpmrepo
```

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/xuxiaowei-com-cn/pkgrepo/rpmrepo"
)

func main() {
	ctx := context.Background()
	const repoURL = "https://download.docker.com/linux/centos/7/x86_64/stable"

	// 1. 按名称列出全部软件包（名称支持 * ? [abc] 通配符）
	pkgs, err := rpmrepo.ListPackages(ctx, repoURL, "docker-ce")
	if err != nil {
		log.Fatal(err)
	}
	for _, pkg := range pkgs {
		fmt.Printf("%s\t%d\t%s\n", pkg.NEVRA(), pkg.Size.Package, pkg.DownloadURL)
	}

	// 2. 只取最新版本
	latest, err := rpmrepo.FindPackage(ctx, repoURL, "docker-ce")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("最新版本:", latest.NEVRA(), latest.Checksum)

	// 3. 更精细的条件：架构、版本、能力（Provides）、自定义过滤
	x86, err := rpmrepo.FindPackages(ctx, repoURL, rpmrepo.Query{
		Name:   "docker-ce",
		Arch:   "x86_64",
		Latest: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("x86_64 最新包:", x86[0].DownloadURL)
}
```

### API

| 函数 / 方法 | 说明 |
| --- | --- |
| `rpmrepo.ListPackages(ctx, repoURL, name, opts...)` | 按名称（支持通配符）列出软件包，返回 `[]Package` |
| `rpmrepo.FindPackages(ctx, repoURL, Query, opts...)` | 按 `Query` 条件查询 |
| `rpmrepo.FindPackage(ctx, repoURL, name, opts...)` | 返回最新版本，找不到返回 `ErrPackageNotFound` |
| `rpmrepo.Open(ctx, repoURL, opts...)` | 解析仓库元数据，返回 `*Repository`，可复用做多次查询 |
| `rpmrepo.WithRepositories(urls...)` | 一次查询多个仓库并合并结果 |
| `(*Repository).Scan(ctx, fn)` | 流式遍历全部软件包，内存占用与包数量无关 |
| `(*Repository).Packages(ctx)` | 返回仓库全部软件包 |
| `(*Repository).FindPackages(ctx, Query)` | 在已打开的仓库上查询 |
| `rpmrepo.CompareVersions(a, b)` | RPM 版本比较（rpmvercmp），与 rpm/dnf 结果一致 |

`Query` 支持：`Name`（通配符）、`Arch`（`src` 同时匹配 `nosrc`）、`Provides`（按能力查找）、
`Epoch`/`Version`/`Release`（精确锁定）、`Latest`（每个 名称+架构 只保留最新）、
`Limit`、`IgnoreCase`、`Sort`、`Filter`（自定义过滤函数）。

### 一次查询多个仓库（rpmrepo）

`WithRepositories` 可以为 `ListPackages`、`FindPackages`、`FindPackage` 追加更多仓库地址：
所有地址使用相同设置独立读取，结果合并为一个列表，因此排序、`Latest`、`Limit` 作用于合并后的
结果，每个包仍保留来源仓库的 `RepoURL` 与 `RepoID`。

```go
pkgs, err := rpmrepo.ListPackages(ctx,
	"https://repo.almalinux.org/almalinux/9/BaseOS/x86_64/os", "nginx",
	rpmrepo.WithRepositories("https://repo.almalinux.org/almalinux/9/AppStream/x86_64/os"))
```

调用参数中的地址可以为空（由 `WithRepositories` 提供全部地址）；重复的地址只读取一次；任一仓库
读取失败都会让整次查询失败。`Open` 依旧只读取单个仓库，配置了更多地址时返回
`ErrMultipleRepositories`。

### 返回的元数据

`Package` 来自 primary.xml，主要字段：

| 字段 | 说明 |
| --- | --- |
| `Name`、`Arch`、`Version{Epoch,Version,Release}` | 包名、架构、版本（`NEVRA()` 返回完整标识） |
| `Checksum{Type,Value,PkgID}` | 指纹（默认 sha256），`PkgID` 表示可作为包唯一标识 |
| `Location.Href`、`DownloadURL` | 仓库内相对路径、解析后的绝对下载地址 |
| `Size{Package,Installed,Archive}` | 包文件 / 安装后 / 归档的大小（字节） |
| `Time{File,Build}` | 入库时间、构建时间（`UnixTime.Time()` 转 `time.Time`） |
| `Summary`、`Description`、`URL`、`Packager` | 描述信息 |
| `Format.License/Vendor/Group/BuildHost/SourceRPM/HeaderRange` | rpm 头部扩展信息 |
| `Format.Provides/Requires/Conflicts/Obsoletes/Recommends/Suggests/Supplements/Enhances` | 依赖关系，`Dependency.String()` 输出如 `nginx >= 1.24.0-1.el9` |

### 支持的仓库形态

- 元数据：`repodata/repomd.xml` + `primary.xml`（也支持直接粘贴 `repomd.xml` 的地址）
- 压缩：gzip、bzip2、xz、zstd，按文件 magic 自动识别（不依赖后缀，无需解压落盘）
- 地址：`http://`、`https://`、`file://`，以及本地目录路径
- 校验：`rpmrepo.WithChecksumVerification(true)` 会边解析边校验元数据校验和
- 内存：`Repository.Scan` 为流式解析，几十万包的仓库也只占用常量内存

```go
client := rpmrepo.New(
	rpmrepo.WithTimeout(30*time.Second),               // HTTP 超时
	rpmrepo.WithHTTPClient(myProxyClient),             // 代理 / 重试 / 自定义 Transport
	rpmrepo.WithChecksumVerification(true),            // 校验元数据
	rpmrepo.WithUserAgent("my-agent/1.0"),
	rpmrepo.WithFetcher(myFetcher),                    // 自定义数据源或本地缓存
)
repo, err := client.Open(ctx, repoURL)
```

## deb 仓库（debrepo）

`debrepo` 按 apt 的方式读取 Debian/Ubuntu 仓库：先用 `dists/<suite>/InRelease`（或 `Release`）
确认有哪些索引以及它们的校验值，再流式解析指定组件与架构的 `Packages`（二进制包）与
`Sources`（源码包）索引。

```go
pkgs, err := debrepo.ListPackages(ctx, "https://deb.debian.org/debian", "nginx",
    debrepo.WithSuite("bookworm"), debrepo.WithArchitecture("amd64"))
for _, pkg := range pkgs {
    fmt.Println(pkg.ID(), pkg.Size.File, pkg.Checksums.SHA256, pkg.DownloadURL)
}
```

### 快速开始

```bash
go get github.com/xuxiaowei-com-cn/pkgrepo/debrepo
```

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/xuxiaowei-com-cn/pkgrepo/debrepo"
)

func main() {
	ctx := context.Background()
	const repoURL = "https://download.docker.com/linux/debian"

	// 1. 按名称列出全部版本（名称支持 * ? [abc] 通配符）
	pkgs, err := debrepo.ListPackages(ctx, repoURL, "docker-ce",
		debrepo.WithSuite("bookworm"), debrepo.WithArchitecture("amd64"))
	if err != nil {
		log.Fatal(err)
	}
	for _, pkg := range pkgs {
		fmt.Printf("%s\t%d\t%s\n", pkg.ID(), pkg.Size.File, pkg.DownloadURL)
	}

	// 2. 只取最新版本
	latest, err := debrepo.FindPackage(ctx, repoURL, "docker-ce",
		debrepo.WithSuite("bookworm"), debrepo.WithArchitecture("amd64"))
	if err != nil {
		log.Fatal(err)
	}
	checksum, _ := latest.Checksum()
	fmt.Println("最新版本:", latest.ID(), checksum)

	// 3. 打开仓库后复用同一份元数据做多次查询
	repo, err := debrepo.Open(ctx, repoURL,
		debrepo.WithSuite("bookworm"), debrepo.WithArchitecture("amd64"),
		debrepo.WithChecksumVerification(true))
	if err != nil {
		log.Fatal(err)
	}
	deps, err := repo.FindPackages(ctx, debrepo.Query{
		Name:     "docker-ce",
		Provides: "docker-engine",
		Latest:   true,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("按虚拟包查询:", deps[0].ID())

	// 4. 源码包来自 Sources 索引
	source, err := repo.FindSource(ctx, debrepo.SourceQuery{Name: "docker-ce"})
	if err != nil {
		log.Fatal(err)
	}
	if dsc, ok := source.DSC(); ok {
		url, _ := source.FileURL(dsc.Path)
		fmt.Println("源码包:", url)
	}
}
```

### API

| 函数 / 方法 | 说明 |
| --- | --- |
| `debrepo.ListPackages(ctx, repoURL, name, opts...)` | 按名称（支持通配符）列出二进制包 |
| `debrepo.FindPackages(ctx, repoURL, Query, opts...)` | 按 `Query` 条件查询二进制包 |
| `debrepo.FindPackage(ctx, repoURL, name, opts...)` | 返回最新版本，找不到返回 `ErrPackageNotFound` |
| `debrepo.ListSources(ctx, repoURL, name, opts...)` | 从 `Sources` 索引按名称列出源码包 |
| `debrepo.FindSources(ctx, repoURL, SourceQuery, opts...)` | 按 `SourceQuery` 条件查询源码包 |
| `debrepo.Open(ctx, repoURL, opts...)` | 解析 `Release`/`InRelease`，返回可复用的 `*Repository` |
| `debrepo.WithRepositories(urls...)` | 一次查询多个仓库并合并结果 |
| `(*Repository).Indexes()` / `SourceIndexes()` | 实际发现的索引文件（组件、架构、压缩格式、大小、校验值） |
| `(*Repository).Scan(ctx, fn)` / `ScanSources(ctx, fn)` | 流式遍历全部软件包，内存占用与包数量无关 |
| `(*Repository).Packages(ctx)` / `Sources(ctx)` | 返回仓库全部二进制包 / 源码包 |
| `(*Repository).FindPackages` / `FindSources` / `FindPackage` / `FindSource` | 在已打开的仓库上查询 |
| `debrepo.CompareVersions(a, b)` | Debian 版本比较，与 `dpkg --compare-versions` 一致 |
| `debrepo.ParseRelease` / `ParsePackages` / `ParseSources` / `ParseStanzas` / `ParseDependencies` | 独立解析函数（也可用于 `.deb` 的 control 文件与 `.dsc` 文件） |

仓库通过选项选择：`WithSuite("bookworm")`、`WithComponent("main", "contrib")`
（`"*"` 表示 `Release` 中列出的全部组件）、`WithArchitecture("amd64", "arm64")`
（`"any"` 表示全部架构；默认使用宿主机架构，并自动扫描 `binary-all` 索引）；
此外还有 `WithTimeout`、`WithHTTPClient`、`WithUserAgent`、`WithFetcher`、
`WithChecksumVerification`、`WithByHashFirst`。

`Query` 支持：`Name`（通配符）、`Arch`、`Component`、`Version`（精确匹配）、`Provides`
（按虚拟包查找）、`Section`、`Priority`、`Essential`、`Latest`（每个 名称+架构+组件 只保留最新）、
`Limit`、`IgnoreCase`、`Sort`、`Filter`；源码包使用对应的 `SourceQuery`。

### 一次查询多个仓库（debrepo）

`WithRepositories` 可以为 `ListPackages`、`FindPackages`、`FindPackage`、`ListSources`、
`FindSources` 追加更多仓库地址：所有地址独立读取，结果合并为一个列表，排序、`Latest`、`Limit`
作用于合并后的结果，每个包仍保留来源仓库的 `RepoURL` 与 `RepoID`。

`WithSuite`、`WithComponent`、`WithArchitecture` 会作用于每一个地址。当各仓库的 suite 不同时
（例如 Debian 安全仓库是 `bookworm-security`），请把 suite 写在地址里，而不要使用 `WithSuite`：

```go
pkgs, err := debrepo.ListPackages(ctx,
	"https://deb.debian.org/debian/dists/bookworm", "nginx",
	debrepo.WithArchitecture("amd64"),
	debrepo.WithRepositories("https://security.debian.org/debian-security/dists/bookworm-security"))
```

调用参数中的地址可以为空（由 `WithRepositories` 提供全部地址）；重复的地址只读取一次；任一仓库
读取失败都会让整次查询失败。`Open` 依旧只读取单个仓库，配置了更多地址时返回
`ErrMultipleRepositories`。

### 返回的元数据

`Package` 来自 `Packages` 索引，主要字段：

| 字段 | 说明 |
| --- | --- |
| `Name`、`Architecture`、`Version{Epoch,Upstream,Revision}` | 包名、架构、Debian 版本（`Version.String()` 输出如 `1:1.22.1-9~deb12u1`） |
| `Source`、`SourceVersion` | 源码包名称，以及 `Source: foo (1.2-3)` 中括号里的版本 |
| `Checksums{MD5,SHA1,SHA256,SHA512}` | `.deb` 的指纹；`Checksum()` 返回可用的最强算法 |
| `Filename`、`DownloadURL` | 仓库内相对路径、解析后的绝对下载地址 |
| `Size{File,Installed}` | `.deb` 文件大小（字节）与安装后占用空间（KiB） |
| `Description{Synopsis,Long}` | 摘要与完整描述（` .` 行会还原为空行） |
| `Maintainer`、`OriginalMaintainer`、`Homepage`、`Section`、`Priority`、`Essential`、`MultiArch` | 描述信息 |
| `Depends`、`PreDepends`、`Recommends`、`Suggests`、`Breaks`、`Conflicts`、`Provides`、`Replaces`、`Enhances`、`BuiltUsing` | 依赖关系（含替代方案与版本约束），可用 `Dependencies.Has`、`Package.DependsOn`、`Package.ProvidesPackage` 判断 |
| `Suite`、`Component`、`RepoURL`、`RepoID` | 包所属的仓库上下文 |
| `Fields` | 段落中的全部原始字段（`Field(name)`），例如 `Tag`、`Task`、`Bugs`、`Package-Type` |

源码包（`Source`）还包含 `Binary`、`Uploaders`、`BuildDepends`/`BuildDepends-Indep`/
`BuildDepends-Arch`（以及对应的 `Build-Conflicts-*`）、`Directory`、`Files`、`Checksums`、
`Vcs*`、`Testsuite`，并提供 `DSC()`、`FileURL()` 等方法来下载 `.dsc` / `.orig.tar.gz`。

### 支持的仓库形态

- 元数据：`dists/<suite>/InRelease`（自动剥离 PGP 签名包裹）或 `dists/<suite>/Release`
- 索引：按组件与架构定位 `Packages`、`Sources`，支持 xz、zstd、gzip、bzip2、lz4 与未压缩格式（按 magic 识别，优先顺序 `xz → zst → gz → bz2 → lz4 → 未压缩`）
- `binary-all`：自动扫描。Debian 13 起 `Architecture: all` 的包只出现在 `.../binary-all/Packages*`
- `by-hash`：`Release` 中声明 `Acquire-By-Hash: yes` 时启用（先常规路径、失败后回退 `by-hash/SHA256/<hash>`；`WithByHashFirst` 可改成优先 by-hash）
- 校验：`debrepo.WithChecksumVerification(true)` 会按 `Release` 中记录的 SHA256/SHA512 校验每个索引
- 地址：`http://`、`https://`、`file://` 与本地目录路径；地址可以指向仓库根、`dists/<suite>`、组件目录，
  甚至某个具体的 `Packages`/`Sources` 文件（这种情况可以不依赖 `Release`）
- 内存：`Scan`/`ScanSources` 为流式解析，几十万包的仓库也只占用常量内存

```go
client := debrepo.New(
	debrepo.WithTimeout(30*time.Second),
	debrepo.WithSuite("trixie"),
	debrepo.WithComponent("*"),              // Release 中列出的全部组件
	debrepo.WithArchitecture("amd64"),
	debrepo.WithChecksumVerification(true),  // 按 Release 校验索引
	debrepo.WithByHashFirst(true),           // 优先使用 by-hash 地址
)
repo, err := client.Open(ctx, "https://deb.debian.org/debian")
```

## 命令行工具

### rpmrepo

```bash
go run ./cmd/rpmrepo packages https://download.docker.com/linux/centos/7/x86_64/stable docker-ce
go run ./cmd/rpmrepo packages -arch x86_64 -latest -verify <仓库地址> docker-ce
go run ./cmd/rpmrepo packages <仓库地址> <仓库地址> nginx
go run ./cmd/rpmrepo packages -json <仓库地址> docker-ce | jq '.[0].format.requires'
go run ./cmd/rpmrepo repomd <仓库地址>
```

包名之前的每个参数都是仓库地址；可以给出多个地址，结果会合并展示（见上方
[一次查询多个仓库（rpmrepo）](#一次查询多个仓库rpmrepo)）。

真实仓库输出示例：

```text
共 1 个软件包（仓库 download.docker.com/linux/centos/7/x86_64/stable，revision 1717607461）
NEVRA                            大小       构建时间              指纹             下载地址
docker-ce-3:26.1.4-1.el7.x86_64  27.3 MB  2024-06-05 11:31  79fad206a296…  https://download.docker.com/linux/centos/7/x86_64/stable/Packages/docker-ce-26.1.4-1.el7.x86_64.rpm
```

### debrepo

```bash
go run ./cmd/debrepo packages -suite bookworm https://deb.debian.org/debian nginx
go run ./cmd/debrepo packages -suite jammy -arch amd64 -latest -json http://archive.ubuntu.com/ubuntu bash
go run ./cmd/debrepo packages -suite bookworm -component '*' https://deb.debian.org/debian docker-ce
go run ./cmd/debrepo packages -arch amd64 https://deb.debian.org/debian/dists/bookworm https://security.debian.org/debian-security/dists/bookworm-security nginx
go run ./cmd/debrepo sources  -suite bookworm https://deb.debian.org/debian nginx
go run ./cmd/debrepo release  -suite bookworm https://deb.debian.org/debian
go run ./cmd/debrepo indexes  -suite noble http://archive.ubuntu.com/ubuntu
```

包名之前的每个参数都是仓库地址；可以给出多个地址，结果会合并展示（见上方
[一次查询多个仓库（debrepo）](#一次查询多个仓库debrepo)）。

真实仓库输出示例：

```text
共 94 个软件包（仓库 mirrors.aliyun.com/docker-ce/linux/debian，suite bookworm）
包名        版本                            架构   组件    大小      指纹          下载地址
docker-ce   5:29.8.0-1~debian.12~bookworm  amd64  stable  23.2 MB  b78d5ee83ed1…  https://mirrors.aliyun.com/docker-ce/linux/debian/dists/bookworm/pool/stable/amd64/docker-ce_29.8.0-1~debian.12~bookworm_amd64.deb
```

## 测试

```bash
go test ./...

# 网络受限或位于国内时，把端到端测试指向同一份仓库数据的镜像
PKGREPO_DOCKER_BASE=https://mirrors.aliyun.com/docker-ce go test ./...

# 完整下载真实 rpm / deb 并核对指纹（默认跳过）
PKGREPO_FULL_DOWNLOAD=1 go test ./rpmrepo/ -run TestRealReposPackageChecksum -v
PKGREPO_FULL_DOWNLOAD=1 go test ./debrepo/ -run TestRealReposPackageChecksum -v

# 可选：校验 Debian 官方源（binary-all 与 Sources 索引）
PKGREPO_DEBIAN_BASE=https://deb.debian.org/debian go test ./debrepo/ -run TestUpstreamDebianRepository -v
```

端到端测试**不使用自造的样例数据**，而是直接解析真实仓库（Docker 的 CentOS、Debian、Ubuntu 仓库树）。
每个仓库在单次测试运行中只下载一次元数据，其余用例复用同一份真实字节。网络不可用时会跳过联网
用例并打印原因，单元测试（rpm 官方 `rpmvercmp` 用例、dpkg 版本比较用例、deb822 解析、解压、查询、
地址解析、索引发现与 `by-hash` 回退等）不需要网络。

测试覆盖：版本比较（`rpmvercmp.at` 全部用例，以及 `dpkg --compare-versions` 语义）、全部压缩格式、
真实 gzip/xz/zstd 元数据、流式解析、下载地址可用性、指纹与大小、依赖解析（含替代方案、架构限定符、
构建 profile）、元数据校验和（含被篡改的场景），以及仓库不存在 / 缺少 primary 或索引 /
不支持的格式等错误分支。

### 已在真实仓库上验证

下表是 2026-09-12 一次运行的快照（仓库内容会随时间变化）。

RPM（Docker 的 CentOS 源）：

| 仓库地址 | primary 压缩 | docker-ce 包数量 | 最新版本 |
| --- | --- | --- | --- |
| `https://download.docker.com/linux/centos/7/x86_64/stable/` | gzip | 99 | 3:26.1.4-1.el7 |
| `https://download.docker.com/linux/centos/8/x86_64/stable/` | gzip | 58 | 3:26.1.3-1.el8 |
| `https://download.docker.com/linux/centos/9/x86_64/stable/` | zstd | 103 | 3:29.8.0-1.el9 |
| `https://download.docker.com/linux/centos/10/x86_64/stable/` | zstd | 53 | 3:29.8.0-1.el10 |

deb（Docker 的 Debian/Ubuntu 源，`stable` 组件，amd64）：

| 仓库地址 | 索引压缩 | docker-ce 版本数 | 最新版本 |
| --- | --- | --- | --- |
| `https://download.docker.com/linux/debian`（bookworm） | gzip | 94 | 5:29.8.0-1~debian.12~bookworm |
| `https://download.docker.com/linux/debian`（bullseye） | gzip | 113 | 5:29.8.0-1~debian.11~bullseye |
| `https://download.docker.com/linux/ubuntu`（jammy） | gzip | 106 | 5:29.8.0-1~ubuntu.22.04~jammy |
| `https://download.docker.com/linux/ubuntu`（noble） | gzip | 71 | 5:29.8.0-1~ubuntu.24.04~noble |

验证内容包括：元数据校验和校验、按 Epoch 与 Debian 版本规则取最新版本、下载地址拼接、大小与指纹，
以及真实依赖的解析——rpm 侧如 `(iptables-nft or iptables)`、`libc.so.6(GLIBC_2.34)(64bit)`，
deb 侧如 `debconf (>= 0.5) | debconf-2.0`、`python3:any (>= 3.11)` 这类替代方案、架构限定符与构建 profile。

## 后续计划

- filelists 元数据（包内文件列表）与 updateinfo（安全公告）
- GPG 签名校验（`repomd.xml.asc`、`InRelease`/`Release.gpg`）
- deb 仓库的 Contents、Translation 索引
- 元数据缓存、镜像列表（mirrorlist、sources.list 的 `Signed-By`）与多镜像回退
