# pkgrepo

**English** | [简体中文](README-zh.md)

A Go SDK for parsing rpm and deb package repositories.

Available today: **RPM (YUM/DNF) repositories** — given a repository URL and a package name,
it returns the matching packages with their download URLs, sizes, fingerprints (checksums),
dependencies and other metadata.

```go
pkgs, err := rpmrepo.ListPackages(ctx,
    "https://download.docker.com/linux/centos/7/x86_64/stable", "docker-ce")
for _, pkg := range pkgs {
    fmt.Println(pkg.NEVRA(), pkg.Size.Package, pkg.Checksum, pkg.DownloadURL)
}
```

## Quick start

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

	// 1. List every package by name (the name accepts * ? [abc] wildcards).
	pkgs, err := rpmrepo.ListPackages(ctx, repoURL, "docker-ce")
	if err != nil {
		log.Fatal(err)
	}
	for _, pkg := range pkgs {
		fmt.Printf("%s\t%d\t%s\n", pkg.NEVRA(), pkg.Size.Package, pkg.DownloadURL)
	}

	// 2. Take only the newest version.
	latest, err := rpmrepo.FindPackage(ctx, repoURL, "docker-ce")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("latest:", latest.NEVRA(), latest.Checksum)

	// 3. Narrower conditions: architecture, version, capability (Provides), custom filters.
	x86, err := rpmrepo.FindPackages(ctx, repoURL, rpmrepo.Query{
		Name:   "docker-ce",
		Arch:   "x86_64",
		Latest: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("newest x86_64 package:", x86[0].DownloadURL)
}
```

## API

| Function / method | Description |
| --- | --- |
| `rpmrepo.ListPackages(ctx, repoURL, name, opts...)` | Lists packages by name (wildcards supported), returns `[]Package` |
| `rpmrepo.FindPackages(ctx, repoURL, Query, opts...)` | Queries by `Query` conditions |
| `rpmrepo.FindPackage(ctx, repoURL, name, opts...)` | Returns the newest version, or `ErrPackageNotFound` |
| `rpmrepo.Open(ctx, repoURL, opts...)` | Parses repository metadata and returns a `*Repository` reusable across queries |
| `(*Repository).Scan(ctx, fn)` | Streams every package; memory usage is independent of the package count |
| `(*Repository).Packages(ctx)` | Returns every package in the repository |
| `(*Repository).FindPackages(ctx, Query)` | Queries an already-opened repository |
| `rpmrepo.CompareVersions(a, b)` | RPM version comparison (rpmvercmp), consistent with rpm/dnf |

`Query` supports `Name` (wildcards), `Arch` (`src` also matches `nosrc`), `Provides` (lookup by
capability), `Epoch`/`Version`/`Release` (exact pinning), `Latest` (keep only the newest per
name+arch), `Limit`, `IgnoreCase`, `Sort` and `Filter` (custom predicate).

## Returned metadata

`Package` comes from primary.xml; the main fields are:

| Field | Description |
| --- | --- |
| `Name`, `Arch`, `Version{Epoch,Version,Release}` | Package name, architecture, version (`NEVRA()` returns the full identifier) |
| `Checksum{Type,Value,PkgID}` | Fingerprint (sha256 by default); `PkgID` marks it usable as the package's unique identity |
| `Location.Href`, `DownloadURL` | Path relative to the repository, and the resolved absolute download URL |
| `Size{Package,Installed,Archive}` | Package file / installed / archive size in bytes |
| `Time{File,Build}` | Repository entry time and build time (`UnixTime.Time()` converts to `time.Time`) |
| `Summary`, `Description`, `URL`, `Packager` | Descriptive information |
| `Format.License/Vendor/Group/BuildHost/SourceRPM/HeaderRange` | Extended rpm header information |
| `Format.Provides/Requires/Conflicts/Obsoletes/Recommends/Suggests/Supplements/Enhances` | Dependencies; `Dependency.String()` renders e.g. `nginx >= 1.24.0-1.el9` |

## Supported repository layouts

- Metadata: `repodata/repomd.xml` + `primary.xml` (you can also pass the `repomd.xml` URL directly)
- Compression: gzip, bzip2, xz, zstd, detected from file magic (no reliance on the extension, no need to decompress to disk)
- URLs: `http://`, `https://`, `file://`, and local directory paths
- Verification: `rpmrepo.WithChecksumVerification(true)` verifies metadata checksums while parsing
- Memory: `Repository.Scan` parses as a stream, so a repository with hundreds of thousands of packages still uses constant memory

```go
client := rpmrepo.New(
	rpmrepo.WithTimeout(30*time.Second),               // HTTP timeout
	rpmrepo.WithHTTPClient(myProxyClient),             // proxy / retries / custom Transport
	rpmrepo.WithChecksumVerification(true),            // verify metadata
	rpmrepo.WithUserAgent("my-agent/1.0"),
	rpmrepo.WithFetcher(myFetcher),                    // custom data source or local cache
)
repo, err := client.Open(ctx, repoURL)
```

## Command-line tool

```bash
go run ./cmd/rpmrepo packages https://download.docker.com/linux/centos/7/x86_64/stable docker-ce
go run ./cmd/rpmrepo packages -arch x86_64 -latest -verify <repo-url> docker-ce
go run ./cmd/rpmrepo packages -json <repo-url> docker-ce | jq '.[0].format.requires'
go run ./cmd/rpmrepo repomd <repo-url>
```

Sample output against a real repository:

```text
共 1 个软件包（仓库 download.docker.com/linux/centos/7/x86_64/stable，revision 1717607461）
NEVRA                            大小       构建时间              指纹             下载地址
docker-ce-3:26.1.4-1.el7.x86_64  27.3 MB  2024-06-05 11:31  79fad206a296…  https://download.docker.com/linux/centos/7/x86_64/stable/Packages/docker-ce-26.1.4-1.el7.x86_64.rpm
```

> Note: the CLI currently prints its output in Chinese.

## Testing

```bash
go test ./...

# Fully download the real rpm and verify its fingerprint (skipped by default)
PKGREPO_FULL_DOWNLOAD=1 go test ./rpmrepo/ -run TestRealReposPackageChecksum -v

# On a restricted network or from mainland China, point at a mirror serving the same repository data
PKGREPO_DOCKER_BASE=https://mirrors.aliyun.com/docker-ce go test ./...
```

The end-to-end tests use **no hand-crafted sample data**: they parse the 4 real repositories listed
above. Each repository downloads `repomd.xml` and `primary.xml` only once per test run, and the
remaining cases reuse those same real bytes. When the network is unavailable, the online cases are
skipped with the reason printed; unit tests (the official rpm `rpmvercmp` cases, decompression,
queries, URL resolution, and so on) need no network.

Coverage: version comparison (every case from `rpmvercmp.at`), all four compression formats,
real gzip and zstd metadata, streaming parsing, download URL reachability, fingerprints and sizes,
dependency parsing, metadata checksums (including tampered scenarios), and error branches such as
a missing repository, a missing primary, and unsupported formats.

### Verified against real repositories

The table below is a snapshot from a run on 2026-09-12 (repository contents change over time):

| Repository URL | primary compression | docker-ce package count | Newest version |
| --- | --- | --- | --- |
| `https://download.docker.com/linux/centos/7/x86_64/stable/` | gzip | 99 | 3:26.1.4-1.el7 |
| `https://download.docker.com/linux/centos/8/x86_64/stable/` | gzip | 58 | 3:26.1.3-1.el8 |
| `https://download.docker.com/linux/centos/9/x86_64/stable/` | zstd | 103 | 3:29.8.0-1.el9 |
| `https://download.docker.com/linux/centos/10/x86_64/stable/` | zstd | 53 | 3:29.8.0-1.el10 |

Verification covers metadata checksum validation, picking the newest version by Epoch ordering,
download URL construction, sizes and fingerprints, and parsing of real dependencies — including
capability names such as `(iptables-nft or iptables)` and `libc.so.6(GLIBC_2.34)(64bit)`.

## Roadmap

- filelists metadata (file lists inside packages) and updateinfo (security advisories)
- GPG signature verification (repomd.xml.asc)
- deb repositories (`Packages.gz`/`Packages.xz`) and apt source parsing
- metadata caching, mirror lists (mirrorlist) and multi-mirror fallback
