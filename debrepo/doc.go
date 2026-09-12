// Package debrepo parses Debian/Ubuntu (apt) software repositories; it is the deb repository SDK of
// pkgrepo.
//
// The most common entry point: given a repository URL and a software name, it returns the list of
// packages (download link, size, checksum, dependencies, and so on).
//
//	pkgs, err := debrepo.ListPackages(ctx, "https://deb.debian.org/debian", "nginx",
//		debrepo.WithSuite("bookworm"), debrepo.WithArchitecture("amd64"))
//	for _, pkg := range pkgs {
//		fmt.Println(pkg.ID(), pkg.Size.File, pkg.DownloadURL)
//	}
//
// Workflow:
//
//  1. Read <repository URL>/dists/<suite>/InRelease (or Release as a fallback) to obtain the
//     components, architectures, and the address, size, and checksum of every index file.
//  2. Locate the Packages (binary packages) and Sources (source packages) indexes by component and
//     architecture, preferring the smallest compression format such as xz or zst, and stream and
//     parse them without decompressing to disk.
//  3. Filter by package name and return the result; memory usage does not depend on the number of
//     packages in the repository.
//  4. Optionally verify the index checksums while parsing (WithChecksumVerification) and fall back
//     to apt's by-hash addresses when needed.
//
// Repository addresses support the http, https, and file schemes, or a local directory path; an
// address can point at the repository root, dists/<suite>, a component directory, or even a specific
// Packages/Sources file.
package debrepo
