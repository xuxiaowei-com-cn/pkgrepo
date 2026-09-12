// Package rpmrepo 解析 RPM（YUM/DNF）软件仓库，是 pkgrepo 的 RPM 仓库 SDK。
//
// 最常用的入口：给定仓库地址与软件名称，返回软件包列表（下载链接、大小、指纹、依赖等）。
//
//	pkgs, err := rpmrepo.ListPackages(ctx,
//		"https://download.docker.com/linux/centos/7/x86_64/stable", "docker-ce")
//	for _, pkg := range pkgs {
//		fmt.Println(pkg.NEVRA(), pkg.Size.Package, pkg.DownloadURL)
//	}
//
// 工作流程：
//
//  1. 读取 <仓库地址>/repodata/repomd.xml，得到 primary 元数据的地址、大小、校验和；
//  2. 流式下载并解析 primary.xml（自动识别 gzip、bzip2、xz、zstd 压缩，无需解压落盘）；
//  3. 按包名过滤并返回结果，内存占用与仓库中的包数量无关。
//
// 仓库地址支持 http、https、file 协议，也可以直接传本地目录路径（如 ./testdata/repo）。
package rpmrepo
