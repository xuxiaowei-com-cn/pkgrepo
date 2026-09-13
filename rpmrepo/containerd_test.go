package rpmrepo

import (
	"context"
	"strings"
	"testing"
	"time"
)

// containerdPackage is the software name used by the tests.
//
// The official Docker repositories provide containerd.io for el7/el8/el9/el10, which makes it a good
// probe for "repository address + software name -> package list" across CentOS versions.
const containerdPackage = "containerd.io"

// TestContainerd queries containerd.io on the official Docker repositories for CentOS 7/8/9/10: it
// verifies both the main "repository address + software name -> package list" path and the query
// style that reuses a single copy of the metadata.
//
// In China or behind a restricted network, an environment variable can point at a mirror serving the
// same repository data:
//
//	PKGREPO_DOCKER_BASE=https://mirrors.aliyun.com/docker-ce go test ./rpmrepo -run TestContainerd -v
func TestContainerd(t *testing.T) {
	ctx := context.Background()

	for _, repo := range realRepos() {
		t.Run(repo.name, func(t *testing.T) {
			// 1. List every package by name (the name supports the * ? [abc] wildcards).
			pkgs := containerdPackages(t, ctx, repo)
			if len(pkgs) == 0 {
				t.Fatalf("%s was not found in the %s repository", containerdPackage, repo.name)
			}
			for i := range pkgs {
				pkg := &pkgs[i]
				t.Logf("%s\t%d\t%s", pkg.NEVRA(), pkg.Size.Package, pkg.DownloadURL)
				if pkg.Name != containerdPackage {
					t.Fatalf("the query result contains another package: %s", pkg.NEVRA())
				}
			}

			// 2. Open the repository and reuse the same metadata: take only the newest version.
			repository := containerdRepository(t, ctx, repo)
			latest := findLatestContainerd(t, ctx, repository)
			t.Logf("newest version: %s %s", latest.NEVRA(), latest.Checksum)
			assertContainerdPackage(t, repo, latest)

			// 3. Reuse the same metadata for a finer query: restrict the architecture and take
			// the newest by descending version.
			x86 := findContainerd(t, ctx, repository, Query{
				Name: containerdPackage,
				Arch: "x86_64",
			})
			t.Logf("newest x86_64 package: %s", x86[0].DownloadURL)
			if x86[0].NEVRA() != latest.NEVRA() {
				t.Errorf("the newest package from the architecture query is %s, which differs from %s from the Latest query", x86[0].NEVRA(), latest.NEVRA())
			}
			for i := range x86 {
				if x86[i].Version.Compare(latest.Version) > 0 {
					t.Errorf("wrong sort result: %s is newer than %s", x86[i].NEVRA(), latest.NEVRA())
				}
			}

			// 4. Without a version condition, the newest version should still appear in the
			// list.
			if !containsPackage(pkgs, latest.NEVRA()) {
				t.Errorf("the list query does not contain %s", latest.NEVRA())
			}
		})
	}
}

// containerdPackages lists every containerd.io package in the repository by name.
func containerdPackages(t *testing.T, ctx context.Context, repo realRepo) []RpmPackage {
	t.Helper()
	requireNetwork(t, repo.url)
	pkgs, err := callWithRetry(t, repo.url, func() ([]RpmPackage, error) {
		return ListPackages(ctx, repo.url, containerdPackage, WithHTTPClient(realRepoHTTPClient(2*time.Minute)))
	})
	if err != nil {
		skipIfNetworkUnavailable(t, repo.url, err)
		t.Fatalf("querying %s in %s failed: %v", containerdPackage, repo.name, err)
	}
	return pkgs
}

// containerdRepository opens the repository so that later queries reuse the same metadata.
func containerdRepository(t *testing.T, ctx context.Context, repo realRepo) *Repository {
	t.Helper()
	requireNetwork(t, repo.url)
	repository, err := callWithRetry(t, repo.url, func() (*Repository, error) {
		return Open(ctx, repo.url, WithHTTPClient(realRepoHTTPClient(2*time.Minute)))
	})
	if err != nil {
		skipIfNetworkUnavailable(t, repo.url, err)
		t.Fatalf("opening the %s repository failed: %v", repo.name, err)
	}
	return repository
}

// findContainerd queries an already opened repository and fails immediately when there is no result.
func findContainerd(t *testing.T, ctx context.Context, repository *Repository, q Query) []RpmPackage {
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
func findLatestContainerd(t *testing.T, ctx context.Context, repository *Repository) *RpmPackage {
	t.Helper()
	pkg, err := repository.FindPackage(ctx, Query{
		Name:   containerdPackage,
		Arch:   "x86_64",
		Latest: true,
	})
	if err != nil {
		t.Fatalf("querying the newest version in %s failed: %v", repository.URL, err)
	}
	return pkg
}

// assertContainerdPackage checks that the package information returned by a real repository is
// self-consistent.
func assertContainerdPackage(t *testing.T, repo realRepo, pkg *RpmPackage) {
	t.Helper()
	distro := strings.TrimPrefix(repo.name, "centos")

	if pkg.Name != containerdPackage || pkg.Arch != "x86_64" {
		t.Fatalf("the query result contains another package: %s", pkg.NEVRA())
	}
	if pkg.Checksum.Type != "sha256" || pkg.Checksum.Value == "" {
		t.Fatalf("unexpected checksum for package %s: %+v", pkg.NEVRA(), pkg.Checksum)
	}
	if pkg.Size.Package <= 0 || pkg.Size.Installed <= 0 {
		t.Fatalf("unexpected size for package %s: %+v", pkg.NEVRA(), pkg.Size)
	}
	if !strings.Contains(pkg.DownloadURL, "/centos/"+distro+"/") {
		t.Errorf("the download address of package %s does not belong to CentOS %s: %q", pkg.NEVRA(), distro, pkg.DownloadURL)
	}
	if !strings.HasSuffix(pkg.DownloadURL, pkg.Filename()) {
		t.Errorf("unexpected download address of package %s: %q", pkg.NEVRA(), pkg.DownloadURL)
	}
	if want := pkg.Name + "-" + pkg.Version.String() + "." + pkg.Arch; pkg.NEVRA() != want {
		t.Errorf("NEVRA is %q, want %q", pkg.NEVRA(), want)
	}
	if len(pkg.Format.Requires) == 0 {
		t.Errorf("no dependency was parsed for package %s", pkg.NEVRA())
	}
	// The version should carry the distribution tag (el7, el8, el9, el10).
	if !strings.HasSuffix(pkg.Version.Release, repo.distTag) {
		t.Errorf("the release of package %s does not contain %s", pkg.NEVRA(), repo.distTag)
	}
	if pkg.Time.Build.IsZero() || pkg.Time.File.IsZero() {
		t.Errorf("package %s is missing its build/add time", pkg.NEVRA())
	}
}

// containsPackage reports whether the list contains a package with the given NEVRA.
func containsPackage(pkgs []RpmPackage, nevra string) bool {
	for i := range pkgs {
		if pkgs[i].NEVRA() == nevra {
			return true
		}
	}
	return false
}
