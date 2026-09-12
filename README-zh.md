# pkgrepo

[English](README.md) | **简体中文**

解析 rpm、deb 软件仓库的 Go SDK。

当前已完成：**RPM（YUM/DNF）仓库**——给定仓库地址与软件名称，返回软件包列表，
包含下载链接、大小、指纹（校验和）、依赖关系等元数据。

```go
pkgs, err := rpmrepo.ListPackages(ctx,
    "https://download.docker.com/linux/centos/7/x86_64/stable", "docker-ce")
for _, pkg := range pkgs {
    fmt.Println(pkg.NEVRA(), pkg.Size.Package, pkg.Checksum, pkg.DownloadURL)
}
```

## 快速开始

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

## API

| 函数 / 方法 | 说明 |
| --- | --- |
| `rpmrepo.ListPackages(ctx, repoURL, name, opts...)` | 按名称（支持通配符）列出软件包，返回 `[]Package` |
| `rpmrepo.FindPackages(ctx, repoURL, Query, opts...)` | 按 `Query` 条件查询 |
| `rpmrepo.FindPackage(ctx, repoURL, name, opts...)` | 返回最新版本，找不到返回 `ErrPackageNotFound` |
| `rpmrepo.Open(ctx, repoURL, opts...)` | 解析仓库元数据，返回 `*Repository`，可复用做多次查询 |
| `(*Repository).Scan(ctx, fn)` | 流式遍历全部软件包，内存占用与包数量无关 |
| `(*Repository).Packages(ctx)` | 返回仓库全部软件包 |
| `(*Repository).FindPackages(ctx, Query)` | 在已打开的仓库上查询 |
| `rpmrepo.CompareVersions(a, b)` | RPM 版本比较（rpmvercmp），与 rpm/dnf 结果一致 |

`Query` 支持：`Name`（通配符）、`Arch`（`src` 同时匹配 `nosrc`）、`Provides`（按能力查找）、
`Epoch`/`Version`/`Release`（精确锁定）、`Latest`（每个 名称+架构 只保留最新）、
`Limit`、`IgnoreCase`、`Sort`、`Filter`（自定义过滤函数）。

## 返回的元数据

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

## 支持的仓库形态

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

## 命令行工具

```bash
go run ./cmd/rpmrepo packages https://download.docker.com/linux/centos/7/x86_64/stable docker-ce
go run ./cmd/rpmrepo packages -arch x86_64 -latest -verify <仓库地址> docker-ce
go run ./cmd/rpmrepo packages -json <仓库地址> docker-ce | jq '.[0].format.requires'
go run ./cmd/rpmrepo repomd <仓库地址>
```

真实仓库输出示例：

```text
共 1 个软件包（仓库 download.docker.com/linux/centos/7/x86_64/stable，revision 1717607461）
NEVRA                            大小       构建时间              指纹             下载地址
docker-ce-3:26.1.4-1.el7.x86_64  27.3 MB  2024-06-05 11:31  79fad206a296…  https://download.docker.com/linux/centos/7/x86_64/stable/Packages/docker-ce-26.1.4-1.el7.x86_64.rpm
```

## 测试

```bash
go test ./...

# 完整下载真实 rpm 并核对指纹（默认跳过）
PKGREPO_FULL_DOWNLOAD=1 go test ./rpmrepo/ -run TestRealReposPackageChecksum -v

# 网络受限或位于国内时，可指向同一份仓库数据的镜像
PKGREPO_DOCKER_BASE=https://mirrors.aliyun.com/docker-ce go test ./...
```

端到端测试**不使用自造的样例数据**，而是直接解析上面 4 个真实仓库：每个仓库在单次测试运行中
只下载一次 `repomd.xml` 与 `primary.xml`，其余用例复用同一份真实字节。网络不可用时会跳过联网
用例并打印原因，单元测试（rpm 官方 `rpmvercmp` 用例、解压、查询、地址解析等）不需要网络。

测试覆盖：版本比较（`rpmvercmp.at` 全部用例）、四种压缩格式、gzip 与 zstd 的真实元数据、
流式解析、下载地址可用性、指纹与大小、依赖解析、元数据校验和（含被篡改的场景），
以及仓库不存在 / 缺少 primary / 不支持的格式等错误分支。

### 已在真实仓库上验证

下表是 2026-09-12 一次运行的快照（仓库内容会随时间变化）：

| 仓库地址 | primary 压缩 | docker-ce 包数量 | 最新版本 |
| --- | --- | --- | --- |
| `https://download.docker.com/linux/centos/7/x86_64/stable/` | gzip | 99 | 3:26.1.4-1.el7 |
| `https://download.docker.com/linux/centos/8/x86_64/stable/` | gzip | 58 | 3:26.1.3-1.el8 |
| `https://download.docker.com/linux/centos/9/x86_64/stable/` | zstd | 103 | 3:29.8.0-1.el9 |
| `https://download.docker.com/linux/centos/10/x86_64/stable/` | zstd | 53 | 3:29.8.0-1.el10 |

验证内容包括：元数据校验和校验、Epoch 排序取最新版本、下载地址拼接、大小与指纹、
以及真实依赖（含 `(iptables-nft or iptables)`、`libc.so.6(GLIBC_2.34)(64bit)` 这类能力名）的解析。

## 后续计划

- filelists 元数据（包内文件列表）与 updateinfo（安全公告）
- GPG 签名校验（repomd.xml.asc）
- deb 仓库（`Packages.gz`/`Packages.xz`）与 apt 源解析
- 元数据缓存、镜像列表（mirrorlist）与多镜像回退
