// Package rpmrepo parses RPM (YUM/DNF) software repositories; it is the RPM repository SDK of
// pkgrepo.
//
// The most common entry point: given a repository URL and a software name, it returns the list of
// packages (download link, size, checksum, dependencies, and so on).
//
//	pkgs, err := rpmrepo.ListPackages(ctx,
//		"https://download.docker.com/linux/centos/7/x86_64/stable", "docker-ce")
//	for _, pkg := range pkgs {
//		fmt.Println(pkg.NEVRA(), pkg.Size.Package, pkg.DownloadURL)
//	}
//
// Workflow:
//
//  1. Read <repository URL>/repodata/repomd.xml to obtain the location, size, and checksum of the
//     primary metadata.
//  2. Stream and parse primary.xml (gzip, bzip2, xz, and zstd compression are detected
//     automatically, no need to decompress to disk).
//  3. Filter by package name and return the result; memory usage does not depend on the number of
//     packages in the repository.
//
// Repository addresses support the http, https, and file schemes, or a local directory path
// (such as ./testdata/repo).
package rpmrepo
