package rpmrepo

import "testing"

func testPackages() []RpmPackage {
	build := func(name, arch, epoch, version, release string, buildTime int64) RpmPackage {
		return RpmPackage{
			Type:    "rpm",
			Name:    name,
			Arch:    arch,
			Version: EVR{Epoch: epoch, Version: version, Release: release},
			Time:    Time{Build: UnixTime(buildTime)},
			Location: Location{
				Href: "Packages/" + name + "-" + version + "-" + release + "." + arch + ".rpm",
			},
			Format: Format{
				Provides: []Dependency{{Name: name, Flags: "EQ", Version: version}},
				Requires: []Dependency{{Name: "libc.so.6()(64bit)"}},
			},
		}
	}
	return []RpmPackage{
		build("docker-ce", "x86_64", "3", "24.0.7", "1.el7", 1700000000),
		build("docker-ce", "x86_64", "3", "24.0.6", "1.el7", 1690000000),
		build("docker-ce", "aarch64", "3", "24.0.7", "1.el7", 1700000500),
		build("docker-ce", "nosrc", "3", "24.0.7", "1.el7", 1700000600),
		build("Docker-CLI", "noarch", "0", "24.0.7", "1.el7", 1700000700),
	}
}

func TestQueryMatchName(t *testing.T) {
	pkgs := testPackages()
	cases := []struct {
		query Query
		want  int
	}{
		{Query{}, 5},
		{Query{Name: "docker-ce"}, 4},
		{Query{Name: "docker-*"}, 4},
		{Query{Name: "docker-ce*"}, 4},
		{Query{Name: "Docker-CLI"}, 1},
		{Query{Name: "docker-cli"}, 0},
		{Query{Name: "docker-cli", IgnoreCase: true}, 1},
		{Query{Name: "*-ce"}, 4},
	}
	for _, tc := range cases {
		if got := countMatches(pkgs, tc.query); got != tc.want {
			t.Errorf("Query%+v matched %d packages, want %d", tc.query, got, tc.want)
		}
	}
}

func TestQueryMatchArch(t *testing.T) {
	pkgs := testPackages()
	cases := []struct {
		query Query
		want  int
	}{
		{Query{Arch: "x86_64"}, 2},
		{Query{Arch: "src"}, 1}, // src also matches the nosrc architecture
		{Query{Arch: "aarch64"}, 1},
		{Query{Arch: "*64"}, 3},
		{Query{Name: "docker-ce", Arch: "x86_64"}, 2},
	}
	for _, tc := range cases {
		if got := countMatches(pkgs, tc.query); got != tc.want {
			t.Errorf("Query%+v matched %d packages, want %d", tc.query, got, tc.want)
		}
	}
}

func TestQueryMatchVersionAndProvides(t *testing.T) {
	pkgs := testPackages()
	cases := []struct {
		query Query
		want  int
	}{
		{Query{Name: "docker-ce", Version: "24.0.7"}, 3},
		{Query{Name: "docker-ce", Version: "24.0.7", Arch: "x86_64"}, 1},
		{Query{Name: "docker-ce", Epoch: "3", Release: "1.el7"}, 4},
		{Query{Name: "docker-ce", Epoch: "1"}, 0},
		{Query{Provides: "docker-ce"}, 4},
		{Query{Provides: "libc.so.6()(64bit)"}, 0},
		{Query{Filter: func(p *RpmPackage) bool { return p.Arch == "noarch" }}, 1},
	}
	for _, tc := range cases {
		if got := countMatches(pkgs, tc.query); got != tc.want {
			t.Errorf("Query%+v matched %d packages, want %d", tc.query, got, tc.want)
		}
	}
}

func TestQuerySort(t *testing.T) {
	pkgs := testPackages()

	nevrAs := func(pkgs []RpmPackage) []string {
		out := make([]string, 0, len(pkgs))
		for i := range pkgs {
			out = append(out, pkgs[i].NEVRA())
		}
		return out
	}
	assertOrder := func(t *testing.T, name string, got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: length is %d, want %d", name, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: item %d is %s, want %s (full result %v)", name, i, got[i], want[i], got)
			}
		}
	}

	t.Run("default sort", func(t *testing.T) {
		sorted := append([]RpmPackage(nil), pkgs...)
		Query{}.sort(sorted)
		assertOrder(t, "SortDefault", nevrAs(sorted), []string{
			"Docker-CLI-24.0.7-1.el7.noarch",
			"docker-ce-3:24.0.7-1.el7.aarch64",
			"docker-ce-3:24.0.7-1.el7.nosrc",
			"docker-ce-3:24.0.7-1.el7.x86_64",
			"docker-ce-3:24.0.6-1.el7.x86_64",
		})
	})

	t.Run("version ascending", func(t *testing.T) {
		sorted := append([]RpmPackage(nil), pkgs...)
		Query{Sort: SortVersionAsc}.sort(sorted)
		assertOrder(t, "SortVersionAsc", nevrAs(sorted), []string{
			"Docker-CLI-24.0.7-1.el7.noarch",
			"docker-ce-3:24.0.6-1.el7.x86_64",
			"docker-ce-3:24.0.7-1.el7.aarch64",
			"docker-ce-3:24.0.7-1.el7.nosrc",
			"docker-ce-3:24.0.7-1.el7.x86_64",
		})
	})

	t.Run("build time descending", func(t *testing.T) {
		sorted := append([]RpmPackage(nil), pkgs...)
		Query{Sort: SortBuildTimeDesc}.sort(sorted)
		assertOrder(t, "SortBuildTimeDesc", nevrAs(sorted), []string{
			"Docker-CLI-24.0.7-1.el7.noarch",
			"docker-ce-3:24.0.7-1.el7.nosrc",
			"docker-ce-3:24.0.7-1.el7.aarch64",
			"docker-ce-3:24.0.7-1.el7.x86_64",
			"docker-ce-3:24.0.6-1.el7.x86_64",
		})
	})

	t.Run("keep original order", func(t *testing.T) {
		sorted := append([]RpmPackage(nil), pkgs...)
		Query{Sort: SortNone}.sort(sorted)
		assertOrder(t, "SortNone", nevrAs(sorted), nevrAs(pkgs))
	})
}

func TestRetainLatest(t *testing.T) {
	latest := retainLatest(testPackages())
	if len(latest) != 4 {
		t.Fatalf("kept %d newest packages, want 4 (three docker-ce architectures plus Docker-CLI)", len(latest))
	}
	for _, pkg := range latest {
		if pkg.Name == "docker-ce" && pkg.Version.Version != "24.0.7" {
			t.Errorf("docker-ce kept an old version %s", pkg.NEVRA())
		}
	}
}

func TestFindPackagesLimit(t *testing.T) {
	pkgs := testPackages()
	query := Query{Name: "docker-ce", Limit: 2}
	matched := make([]RpmPackage, 0, len(pkgs))
	for i := range pkgs {
		if query.Match(&pkgs[i]) {
			matched = append(matched, pkgs[i])
		}
	}
	query.sort(matched)
	if len(matched) > query.Limit {
		matched = matched[:query.Limit]
	}
	if len(matched) != 2 || matched[0].Version.Version != "24.0.7" {
		t.Errorf("unexpected Limit result: %v", matched)
	}
}

func countMatches(pkgs []RpmPackage, query Query) int {
	count := 0
	for i := range pkgs {
		if query.Match(&pkgs[i]) {
			count++
		}
	}
	return count
}
