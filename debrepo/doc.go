// Package debrepo 解析 Debian/Ubuntu（apt）软件仓库，是 pkgrepo 的 deb 仓库 SDK。
//
// 最常用的入口：给定仓库地址与软件名称，返回软件包列表（下载链接、大小、指纹、依赖等）。
//
//	pkgs, err := debrepo.ListPackages(ctx, "https://deb.debian.org/debian", "nginx",
//		debrepo.WithSuite("bookworm"), debrepo.WithArchitecture("amd64"))
//	for _, pkg := range pkgs {
//		fmt.Println(pkg.ID(), pkg.Size.File, pkg.DownloadURL)
//	}
//
// 工作流程：
//
//  1. 读取 <仓库地址>/dists/<suite>/InRelease（其次 Release），得到组件、架构与
//     全部索引文件的地址、大小、校验和；
//  2. 按组件与架构定位 Packages（二进制包）与 Sources（源码包）索引，优先选择
//     xz、zst 等体积最小的压缩格式，流式下载并解析，无需解压落盘；
//  3. 按包名过滤并返回结果，内存占用与仓库中的包数量无关；
//  4. 可选地边解析边校验索引指纹（WithChecksumVerification），并在需要时回退到
//     apt 的 by-hash 地址。
//
// 仓库地址支持 http、https、file 协议，也可以直接传本地目录路径；地址可以指向
// 仓库根目录、dists/<suite>、组件目录，甚至某个具体的 Packages/Sources 文件。
package debrepo
