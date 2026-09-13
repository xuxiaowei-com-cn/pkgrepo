package debrepo

import (
	"context"
	"strings"
	"testing"
	"time"
)

// containerdPackage is the software name used by the tests.
//
// Docker's deb repositories provide containerd.io for several Debian/Ubuntu suites, which makes it a
// good probe for how the main "repository address + software name -> package list" path behaves on
// deb repositories.
const containerdPackage = "containerd.io"

// TestContainerd queries containerd.io on Docker's Debian/Ubuntu deb repositories: it verifies both
// the main "repository address + software name -> package list" path and the query style that reuses
// a single copy of the metadata.
//
// In China or behind a restricted network, an environment variable can point at a mirror serving the
// same repository data:
//
//	PKGREPO_DOCKER_BASE=https://mirrors.aliyun.com/docker-ce go test ./debrepo -run TestContainerd -v
func TestContainerd(t *testing.T) {
	ctx := context.Background()

	for _, repo := range realRepos() {
		repo := repo
		t.Run(repo.name, func(t *testing.T) {
			// 1. List every package by name (the name supports the * ? [abc] wildcards).
			pkgs := containerdPackages(t, ctx, repo)
			if len(pkgs) == 0 {
				t.Fatalf("%s was not found in the %s repository", containerdPackage, repo.name)
			}
			for i := range pkgs {
				pkg := &pkgs[i]
				t.Logf("%s\t%d\t%s", pkg.ID(), pkg.Size.File, pkg.DownloadURL)
				if pkg.Name != containerdPackage {
					t.Fatalf("the query result contains another package: %s", pkg.ID())
				}
			}

			// 2. Open the repository and reuse the same metadata: take only the newest version.
			repository := openRealRepo(t, repo)
			latest := findLatestContainerd(t, ctx, repository)
			checksum, _ := latest.Checksum()
			t.Logf("newest version: %s %s", latest.ID(), checksum)
			assertContainerdPackage(t, repo, latest)

			// 3. Reuse the same metadata for a finer query: restrict the architecture and take
			// the newest by descending version.
			amd64 := findContainerd(t, ctx, repository, Query{
				Name: containerdPackage,
				Arch: []string{repo.arch},
			})
			t.Logf("newest %s package: %s", repo.arch, amd64[0].DownloadURL)
			if amd64[0].ID() != latest.ID() {
				t.Errorf("the newest package from the architecture query is %s, which differs from %s from the Latest query", amd64[0].ID(), latest.ID())
			}
			for i := range amd64 {
				if amd64[i].Version.Compare(latest.Version) > 0 {
					t.Errorf("wrong sort result: %s is newer than %s", amd64[i].ID(), latest.ID())
				}
			}

			// 4. Without a version condition, the newest version should still appear in the
			// list.
			if !containsPackage(pkgs, latest.ID()) {
				t.Errorf("the list query does not contain %s", latest.ID())
			}
		})
	}
}

// containerdPackages lists every containerd.io package in the repository by name.
func containerdPackages(t *testing.T, ctx context.Context, repo realRepo) []Package {
	t.Helper()
	requireNetwork(t, repo.url)
	pkgs, err := callWithRetry(t, repo.url, func() ([]Package, error) {
		return ListPackages(ctx, repo.url, containerdPackage,
			WithFetcher(newCachingFetcher(&HTTPFetcher{Client: realRepoClient(2 * time.Minute)})),
			WithSuite(repo.suite),
			WithComponent(repo.component),
			WithArchitecture(repo.arch),
		)
	})
	if err != nil {
		if isOfflineError(err) {
			networkUnavailable.Store(true)
			t.Skipf("skipping: the network is unavailable, cannot access %s (%v)", repo.url, err)
		}
		t.Fatalf("querying %s in %s failed: %v", containerdPackage, repo.name, err)
	}
	return pkgs
}

// findContainerd queries an already opened repository and fails immediately when there is no result.
func findContainerd(t *testing.T, ctx context.Context, repository *Repository, q Query) []Package {
	t.Helper()
	pkgs, err := repository.FindPackages(ctx, q)
	if err != nil {
		t.Fatalf("query %+v failed: %v", q, err)
	}
	if len(pkgs) == 0 {
		t.Fatalf("query %+v returned no result", q)
	}
	return pkgs
}

// findLatestContainerd returns the newest containerd.io package in the repository.
func findLatestContainerd(t *testing.T, ctx context.Context, repository *Repository) *Package {
	t.Helper()
	pkg, err := repository.FindPackage(ctx, Query{
		Name:   containerdPackage,
		Arch:   []string{repository.Architectures[0]},
		Latest: true,
	})
	if err != nil {
		t.Fatalf("querying the newest version in %s failed: %v", repository.URL, err)
	}
	return pkg
}

// assertContainerdPackage checks that the deb package information returned by a real repository is
// self-consistent.
func assertContainerdPackage(t *testing.T, repo realRepo, pkg *Package) {
	t.Helper()
	if pkg.Name != containerdPackage || pkg.Architecture != repo.arch {
		t.Fatalf("the query result contains another package: %s", pkg.ID())
	}
	if pkg.Checksums.SHA256 == "" {
		t.Fatalf("package %s is missing its sha256 checksum: %+v", pkg.ID(), pkg.Checksums)
	}
	if pkg.Size.File <= 0 || pkg.Size.Installed <= 0 {
		t.Fatalf("unexpected size for package %s: %+v", pkg.ID(), pkg.Size)
	}
	// A deb download address is the repository root plus Filename (Docker's Filename carries a
	// dists/<suite> prefix).
	prefix := repo.url + "/dists/" + repo.suite + "/pool/"
	if !strings.HasPrefix(pkg.DownloadURL, prefix) {
		t.Errorf("the download address of package %s does not belong to %s %s: %q", pkg.ID(), repo.distro, repo.suite, pkg.DownloadURL)
	}
	if !strings.HasSuffix(pkg.DownloadURL, pkg.BaseFilename()) {
		t.Errorf("unexpected download address of package %s: %q", pkg.ID(), pkg.DownloadURL)
	}
	if want := pkg.Name + "_" + pkg.Version.String() + "_" + pkg.Architecture; pkg.ID() != want {
		t.Errorf("identifier is %q, want %q", pkg.ID(), want)
	}
	if pkg.Suite != repo.suite || pkg.Component != repo.component {
		t.Errorf("the context of package %s is %s/%s, want %s/%s",
			pkg.ID(), pkg.Suite, pkg.Component, repo.suite, repo.component)
	}
	if len(pkg.Depends) == 0 {
		t.Errorf("no dependency was parsed for package %s", pkg.ID())
	}
	if !pkg.DependsOn("libc6") {
		t.Errorf("the dependencies of package %s do not contain libc6: %v", pkg.ID(), pkg.Depends)
	}
	if pkg.Section == "" || pkg.Priority == "" {
		t.Errorf("package %s is missing Section/Priority: %+v", pkg.ID(), pkg)
	}
	if pkg.Description.Synopsis == "" {
		t.Errorf("package %s is missing its description", pkg.ID())
	}
}

// containsPackage reports whether the list contains a package with the given identifier.
func containsPackage(pkgs []Package, id string) bool {
	for i := range pkgs {
		if pkgs[i].ID() == id {
			return true
		}
	}
	return false
}
