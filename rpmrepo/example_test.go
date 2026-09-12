package rpmrepo_test

import (
	"context"
	"fmt"
	"log"

	"github.com/xuxiaowei-com-cn/pkgrepo/rpmrepo"
)

// ExampleListPackages 演示按仓库地址与软件名称返回软件包列表。
// 该示例需要访问网络，因此没有 Output 注释，不会被 go test 执行。
func ExampleListPackages() {
	ctx := context.Background()
	const repoURL = "https://download.docker.com/linux/centos/7/x86_64/stable"

	pkgs, err := rpmrepo.ListPackages(ctx, repoURL, "docker-ce")
	if err != nil {
		log.Fatal(err)
	}
	for _, pkg := range pkgs {
		fmt.Printf("%s\t%d 字节\t%s\t%s\n",
			pkg.NEVRA(), pkg.Size.Package, pkg.Checksum, pkg.DownloadURL)
	}

	// 只取最新版本，并查看依赖关系。
	latest, err := rpmrepo.FindPackage(ctx, repoURL, "docker-ce")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("最新版本:", latest.NEVRA())
	for _, req := range latest.Format.Requires {
		fmt.Println("依赖:", req)
	}

	// 需要更精细的条件时使用 Query。
	amd64, err := rpmrepo.FindPackages(ctx, repoURL, rpmrepo.Query{
		Name:   "docker-ce",
		Arch:   "x86_64",
		Latest: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("x86_64 最新包数量:", len(amd64))
}

// ExampleOpen 演示复用仓库对象遍历大量软件包。
func ExampleOpen() {
	ctx := context.Background()
	repo, err := rpmrepo.Open(ctx, "https://download.docker.com/linux/centos/7/x86_64/stable")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("仓库标识:", repo.ID, "revision:", repo.Revision)

	// 流式遍历，内存占用与包数量无关。
	count := 0
	if err := repo.Scan(ctx, func(pkg *rpmrepo.Package) error {
		count++
		return nil
	}); err != nil {
		log.Fatal(err)
	}
	fmt.Println("软件包总数:", count)
}
