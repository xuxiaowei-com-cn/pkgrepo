# pkgrepo

**English** | [简体中文](README-zh.md)

A Go SDK for parsing rpm and deb package repositories.

Available today:

- **RPM (YUM/DNF) repositories** — `rpmrepo` reads `repodata/repomd.xml` + `primary.xml`
  and returns the matching packages with download URLs, sizes, fingerprints (checksums),
  dependencies and other metadata.
- **Debian/Ubuntu (apt) repositories** — `debrepo` reads `dists/<suite>/InRelease` (or
  `Release`) plus the `Packages`/`Sources` indexes and returns the same kind of metadata,
  with `by-hash` lookup, index checksum verification and Debian version comparison.

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

## RPM repositories (rpmrepo)

Given a repository URL and a package name, it returns the matching packages with their
download URLs, sizes, fingerprints (checksums), dependencies and other metadata.

```go
pkgs, err := rpmrepo.ListPackages(ctx,
    "https://download.docker.com/linux/centos/7/x86_64/stable", "docker-ce")
for _, pkg := range pkgs {
    fmt.Println(pkg.NEVRA(), pkg.Size.Package, pkg.Checksum, pkg.DownloadURL)
}
```

### Quick start

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
		Arch:   []string{"x86_64"},
		Latest: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("newest x86_64 package:", x86[0].DownloadURL)
}
```

### API

| Function / method | Description |
| --- | --- |
| `rpmrepo.ListPackages(ctx, repoURL, name, opts...)` | Lists packages by name (wildcards supported), returns `[]Package` |
| `rpmrepo.FindPackages(ctx, repoURL, Query, opts...)` | Queries by `Query` conditions |
| `rpmrepo.FindPackage(ctx, repoURL, name, opts...)` | Returns the newest version, or `ErrPackageNotFound` |
| `rpmrepo.Open(ctx, repoURL, opts...)` | Parses repository metadata and returns a `*Repository` reusable across queries |
| `rpmrepo.WithRepositories(urls...)` | Queries several repositories at once and merges the results |
| `(*Repository).Scan(ctx, fn)` | Streams every package; memory usage is independent of the package count |
| `(*Repository).Packages(ctx)` | Returns every package in the repository |
| `(*Repository).FindPackages(ctx, Query)` | Queries an already-opened repository |
| `rpmrepo.CompareVersions(a, b)` | RPM version comparison (rpmvercmp), consistent with rpm/dnf |

`Query` supports `Name` (wildcards), `Arch` (a list of architectures, wildcards supported; `src`
also matches `nosrc`), `Provides` (lookup by capability), `Epoch`/`Version`/`Release` (exact
pinning), `Latest` (keep only the newest per name+arch), `Limit`, `IgnoreCase`, `Sort` and `Filter`
(custom predicate).

### Querying several repositories

`WithRepositories` adds further repository addresses to `ListPackages`, `FindPackages`, and
`FindPackage`. Every address is read with the same settings and the results are merged into a single
list, so sorting, `Latest`, and `Limit` apply to the merged result, and every package keeps the
`RepoURL` and `RepoID` of the repository it came from:

```go
pkgs, err := rpmrepo.ListPackages(ctx,
	"https://repo.almalinux.org/almalinux/9/BaseOS/x86_64/os", "nginx",
	rpmrepo.WithRepositories("https://repo.almalinux.org/almalinux/9/AppStream/x86_64/os"))
```

The address given to the call may be empty when `WithRepositories` supplies every address, repeated
addresses are read once, and a repository that cannot be read fails the whole query. `Open` keeps
reading a single repository and returns `ErrMultipleRepositories` when further addresses are
configured.

### Returned metadata

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

### Supported repository layouts

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

## Debian repositories (debrepo)

`debrepo` reads a Debian/Ubuntu repository the same way apt does: `dists/<suite>/InRelease`
(or `Release`) tells it which indexes exist and what their checksums are, then it streams the
`Packages` (binary) and `Sources` (source) indexes of the requested components and architectures.

```go
pkgs, err := debrepo.ListPackages(ctx, "https://deb.debian.org/debian", "nginx",
    debrepo.WithSuite("bookworm"), debrepo.WithArchitecture("amd64"))
for _, pkg := range pkgs {
    fmt.Println(pkg.ID(), pkg.Size.File, pkg.Checksums.SHA256, pkg.DownloadURL)
}
```

### Quick start

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

	// 1. List every version of a package (the name accepts * ? [abc] wildcards).
	pkgs, err := debrepo.ListPackages(ctx, repoURL, "docker-ce",
		debrepo.WithSuite("bookworm"), debrepo.WithArchitecture("amd64"))
	if err != nil {
		log.Fatal(err)
	}
	for _, pkg := range pkgs {
		fmt.Printf("%s\t%d\t%s\n", pkg.ID(), pkg.Size.File, pkg.DownloadURL)
	}

	// 2. Take only the newest version.
	latest, err := debrepo.FindPackage(ctx, repoURL, "docker-ce",
		debrepo.WithSuite("bookworm"), debrepo.WithArchitecture("amd64"))
	if err != nil {
		log.Fatal(err)
	}
	checksum, _ := latest.Checksum()
	fmt.Println("latest:", latest.ID(), checksum)

	// 3. Open the repository once and reuse the metadata for several queries.
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
	fmt.Println("by virtual package:", deps[0].ID())

	// 4. Source packages come from the Sources index.
	source, err := repo.FindSource(ctx, debrepo.SourceQuery{Name: "docker-ce"})
	if err != nil {
		log.Fatal(err)
	}
	if dsc, ok := source.DSC(); ok {
		url, _ := source.FileURL(dsc.Path)
		fmt.Println("source:", url)
	}
}
```

### API

| Function / method | Description |
| --- | --- |
| `debrepo.ListPackages(ctx, repoURL, name, opts...)` | Lists binary packages by name (wildcards supported) |
| `debrepo.FindPackages(ctx, repoURL, Query, opts...)` | Queries binary packages by `Query` conditions |
| `debrepo.FindPackage(ctx, repoURL, name, opts...)` | Returns the newest version, or `ErrPackageNotFound` |
| `debrepo.ListSources(ctx, repoURL, name, opts...)` | Lists source packages from the `Sources` index |
| `debrepo.FindSources(ctx, repoURL, SourceQuery, opts...)` | Queries source packages |
| `debrepo.Open(ctx, repoURL, opts...)` | Parses `Release`/`InRelease` and returns a reusable `*Repository` |
| `debrepo.WithRepositories(urls...)` | Queries several repositories at once and merges the results |
| `(*Repository).Indexes()` / `SourceIndexes()` | The discovered index files (component, architecture, compression, size, checksum) |
| `(*Repository).Scan(ctx, fn)` / `ScanSources(ctx, fn)` | Streams every package; memory usage is independent of the package count |
| `(*Repository).Packages(ctx)` / `Sources(ctx)` | Returns every binary / source package |
| `(*Repository).FindPackages` / `FindSources` / `FindPackage` / `FindSource` | Queries an already-opened repository |
| `debrepo.CompareVersions(a, b)` | Debian version comparison, consistent with `dpkg --compare-versions` |
| `debrepo.ParseRelease` / `ParsePackages` / `ParseSources` / `ParseStanzas` / `ParseDependencies` | Standalone parsers (also handy for `.deb` control files and `.dsc` files) |

Repository selection is done with options: `WithSuite("bookworm")`, `WithComponent("main", "contrib")`
(`"*"` means every component listed in `Release`), `WithArchitecture("amd64", "arm64")`
(`"any"` means every architecture; by default the host architecture is used and the `binary-all`
index is scanned as well), plus `WithTimeout`, `WithHTTPClient`, `WithUserAgent`, `WithFetcher`,
`WithChecksumVerification` and `WithByHashFirst`.

`Query` supports `Name` (wildcards), `Arch` (a list of architectures, wildcards supported; `any`
means no restriction), `Component`, `Version` (exact), `Provides`, `Section`, `Priority`,
`Essential`, `Latest` (newest per name+architecture+component), `Limit`, `IgnoreCase`, `Sort` and
`Filter`. `SourceQuery` is the source-package equivalent.

### Querying several repositories

`WithRepositories` adds further repository addresses to `ListPackages`, `FindPackages`,
`FindPackage`, `ListSources`, and `FindSources`. The results are merged into a single list: sorting,
`Latest`, and `Limit` apply to the merged result, and every package keeps the `RepoURL` and `RepoID`
of the repository it came from.

`WithSuite`, `WithComponent`, and `WithArchitecture` apply to every address. When the repositories
use different suites (the Debian security archive is `bookworm-security`), give the suite in each
address instead of using `WithSuite`:

```go
pkgs, err := debrepo.ListPackages(ctx,
	"https://deb.debian.org/debian/dists/bookworm", "nginx",
	debrepo.WithArchitecture("amd64"),
	debrepo.WithRepositories("https://security.debian.org/debian-security/dists/bookworm-security"))
```

The address given to the call may be empty when `WithRepositories` supplies every address, repeated
addresses are read once, and a repository that cannot be read fails the whole query. `Open` keeps
reading a single repository and returns `ErrMultipleRepositories` when further addresses are
configured.

### Returned metadata

`Package` comes from the `Packages` index; the main fields are:

| Field | Description |
| --- | --- |
| `Name`, `Architecture`, `Version{Epoch,Upstream,Revision}` | Package name, architecture and Debian version (`Version.String()` renders `1:1.22.1-9~deb12u1`) |
| `Source`, `SourceVersion` | Source package name and the optional version in `Source: foo (1.2-3)` |
| `Checksums{MD5,SHA1,SHA256,SHA512}` | Fingerprints of the `.deb`; `Checksum()` returns the strongest available one |
| `Filename`, `DownloadURL` | Path relative to the repository root, and the resolved absolute download URL |
| `Size{File,Installed}` | `.deb` size in bytes and installed size in KiB |
| `Description{Synopsis,Long}` | Synopsis and long description (` .` lines become blank lines) |
| `Maintainer`, `OriginalMaintainer`, `Homepage`, `Section`, `Priority`, `Essential`, `MultiArch` | Descriptive information |
| `Depends`, `PreDepends`, `Recommends`, `Suggests`, `Breaks`, `Conflicts`, `Provides`, `Replaces`, `Enhances`, `BuiltUsing` | Dependencies with alternatives and version constraints (`Dependencies.Has`, `Package.DependsOn`, `Package.ProvidesPackage`) |
| `Suite`, `Component`, `RepoURL`, `RepoID` | Repository context |
| `Fields` | Every raw field of the stanza (`Field(name)`), e.g. `Tag`, `Task`, `Bugs`, `Package-Type` |

Source packages (`Source`) additionally carry `Binary`, `Uploaders`, `BuildDepends`/`BuildDepends-Indep`/
`BuildDepends-Arch` (and the `Build-Conflicts-*` counterparts), `Directory`, `Files`,
`Checksums`, `Vcs*`, `Testsuite`, and helpers such as `DSC()` and `FileURL()` for downloading
`.dsc` / `.orig.tar.gz` files.

### Supported repository layouts

- Metadata: `dists/<suite>/InRelease` (PGP armor stripped automatically) or `dists/<suite>/Release`
- Indexes: `Packages` and `Sources` per component and architecture, in xz, zstd, gzip, bzip2, lz4 or uncompressed form (magic-based detection, preference `xz → zst → gz → bz2 → lz4 → plain`)
- `binary-all`: scanned automatically, because Debian 13 and later keep `Architecture: all` packages only in `.../binary-all/Packages*`
- `by-hash`: used when `Release` advertises `Acquire-By-Hash: yes` (the canonical path is tried first and the `by-hash/SHA256/<hash>` address is the fallback; `WithByHashFirst` flips the order)
- Verification: `debrepo.WithChecksumVerification(true)` checks every index against the SHA256/SHA512 recorded in `Release`
- URLs: `http://`, `https://`, `file://` and local directory paths; the URL may point at the repository root, `dists/<suite>`, a component directory or a concrete `Packages`/`Sources` file (a readable `Release` is optional in that last case)
- Memory: `Scan`/`ScanSources` parse as a stream, so a repository with hundreds of thousands of packages still uses constant memory

```go
client := debrepo.New(
	debrepo.WithTimeout(30*time.Second),
	debrepo.WithSuite("trixie"),
	debrepo.WithComponent("*"),              // every component listed in Release
	debrepo.WithArchitecture("amd64"),
	debrepo.WithChecksumVerification(true),  // verify indexes against Release
	debrepo.WithByHashFirst(true),           // prefer the by-hash addresses
)
repo, err := client.Open(ctx, "https://deb.debian.org/debian")
```

## Command-line tools

### rpmrepo

```bash
go run ./cmd/rpmrepo packages https://download.docker.com/linux/centos/7/x86_64/stable docker-ce
go run ./cmd/rpmrepo packages -arch x86_64 -latest -verify <repo-url> docker-ce
go run ./cmd/rpmrepo packages <repo-url> <repo-url> nginx
go run ./cmd/rpmrepo packages -json <repo-url> docker-ce | jq '.[0].format.requires'
go run ./cmd/rpmrepo repomd <repo-url>
```

Every argument before the package name is a repository URL; several are read together and their
results are merged (see [Querying several repositories](#querying-several-repositories)).

Sample output against a real repository:

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

Every argument before the package name is a repository URL; several are read together and their
results are merged (see [Querying several repositories](#querying-several-repositories-1)).

Sample output against a real repository:

```text
共 94 个软件包（仓库 mirrors.aliyun.com/docker-ce/linux/debian，suite bookworm）
包名        版本                            架构   组件    大小      指纹          下载地址
docker-ce   5:29.8.0-1~debian.12~bookworm  amd64  stable  23.2 MB  b78d5ee83ed1…  https://mirrors.aliyun.com/docker-ce/linux/debian/dists/bookworm/pool/stable/amd64/docker-ce_29.8.0-1~debian.12~bookworm_amd64.deb
```

> Note: both CLIs print their output in Chinese.

## Testing

```bash
go test ./...

# On a restricted network or from mainland China, point the end-to-end tests at a mirror
PKGREPO_DOCKER_BASE=https://mirrors.aliyun.com/docker-ce go test ./...

# Fully download the real rpm / deb and verify its fingerprint (skipped by default)
PKGREPO_FULL_DOWNLOAD=1 go test ./rpmrepo/ -run TestRealReposPackageChecksum -v
PKGREPO_FULL_DOWNLOAD=1 go test ./debrepo/ -run TestRealReposPackageChecksum -v

# Optional: check the Debian upstream repository (binary-all and Sources indexes)
PKGREPO_DEBIAN_BASE=https://deb.debian.org/debian go test ./debrepo/ -run TestUpstreamDebianRepository -v
```

The end-to-end tests use **no hand-crafted sample data**: they parse real repositories
(Docker's CentOS, Debian and Ubuntu repository trees). Each repository downloads its metadata
once per test run, and the remaining cases reuse those same real bytes. When the network is
unavailable the online cases are skipped with the reason printed; unit tests (the official rpm
`rpmvercmp` cases, the dpkg version-comparison cases, deb822 parsing, decompression, queries,
URL resolution, index discovery and `by-hash` fallback, and so on) need no network.

Coverage: version comparison (every case from `rpmvercmp.at`, plus `dpkg
--compare-versions` semantics), all supported compression formats, real gzip/xz/zstd metadata,
streaming parsing, download URL reachability, fingerprints and sizes, dependency parsing
(including alternatives, architecture qualifiers and build profiles), metadata checksums
(including tampered scenarios), and error branches such as a missing repository, a missing
primary/index, and unsupported formats.

### Verified against real repositories

The tables below are snapshots from a run on 2026-09-12 (repository contents change over time).

RPM (Docker's CentOS repositories):

| Repository URL | primary compression | docker-ce package count | Newest version |
| --- | --- | --- | --- |
| `https://download.docker.com/linux/centos/7/x86_64/stable/` | gzip | 99 | 3:26.1.4-1.el7 |
| `https://download.docker.com/linux/centos/8/x86_64/stable/` | gzip | 58 | 3:26.1.3-1.el8 |
| `https://download.docker.com/linux/centos/9/x86_64/stable/` | zstd | 103 | 3:29.8.0-1.el9 |
| `https://download.docker.com/linux/centos/10/x86_64/stable/` | zstd | 53 | 3:29.8.0-1.el10 |

deb (Docker's Debian/Ubuntu repositories, `stable` component, amd64):

| Repository URL | index compression | docker-ce versions | Newest version |
| --- | --- | --- | --- |
| `https://download.docker.com/linux/debian` (bookworm) | gzip | 94 | 5:29.8.0-1~debian.12~bookworm |
| `https://download.docker.com/linux/debian` (bullseye) | gzip | 113 | 5:29.8.0-1~debian.11~bullseye |
| `https://download.docker.com/linux/ubuntu` (jammy) | gzip | 106 | 5:29.8.0-1~ubuntu.22.04~jammy |
| `https://download.docker.com/linux/ubuntu` (noble) | gzip | 71 | 5:29.8.0-1~ubuntu.24.04~noble |

Verification covers metadata checksum validation, picking the newest version by Epoch and
Debian version ordering, download URL construction, sizes and fingerprints, and parsing of
real dependencies — including capability names such as `(iptables-nft or iptables)` and
`libc.so.6(GLIBC_2.34)(64bit)` for rpm, and alternatives, architecture qualifiers and build
profiles such as `debconf (>= 0.5) | debconf-2.0` and `python3:any (>= 3.11)` for deb.

## Roadmap

- filelists metadata (file lists inside packages) and updateinfo (security advisories)
- GPG signature verification (`repomd.xml.asc`, `InRelease`/`Release.gpg`)
- Contents/Translation indexes for deb repositories
- metadata caching, mirror lists (mirrorlist, `Signed-By` sources.list entries) and multi-mirror fallback
