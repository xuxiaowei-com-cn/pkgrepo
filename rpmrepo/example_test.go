package rpmrepo_test

import (
	"context"
	"fmt"
	"log"

	"github.com/xuxiaowei-com-cn/pkgrepo/rpmrepo"
)

// ExampleListPackages demonstrates returning the package list for a repository address and software
// name. This example needs network access, so it has no Output comment and is not executed by
// go test.
func ExampleListPackages() {
	ctx := context.Background()
	const repoURL = "https://download.docker.com/linux/centos/7/x86_64/stable"

	pkgs, err := rpmrepo.ListPackages(ctx, repoURL, "docker-ce")
	if err != nil {
		log.Fatal(err)
	}
	for _, pkg := range pkgs {
		fmt.Printf("%s\t%d bytes\t%s\t%s\n",
			pkg.NEVRA(), pkg.Size.Package, pkg.Checksum, pkg.DownloadURL)
	}

	// Take only the newest version and inspect the dependencies.
	latest, err := rpmrepo.FindPackage(ctx, repoURL, "docker-ce")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("latest version:", latest.NEVRA())
	for _, req := range latest.Format.Requires {
		fmt.Println("requires:", req)
	}

	// Use Query when finer-grained conditions are needed.
	amd64, err := rpmrepo.FindPackages(ctx, repoURL, rpmrepo.Query{
		Name:   "docker-ce",
		Arch:   "x86_64",
		Latest: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("number of newest x86_64 packages:", len(amd64))
}

// ExampleOpen demonstrates reusing a repository object to iterate over many packages.
func ExampleOpen() {
	ctx := context.Background()
	repo, err := rpmrepo.Open(ctx, "https://download.docker.com/linux/centos/7/x86_64/stable")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("repository ID:", repo.ID, "revision:", repo.Revision)

	// Stream over the packages; memory usage does not depend on the number of packages.
	count := 0
	if err := repo.Scan(ctx, func(pkg *rpmrepo.Package) error {
		count++
		return nil
	}); err != nil {
		log.Fatal(err)
	}
	fmt.Println("total number of packages:", count)
}
