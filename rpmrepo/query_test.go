package rpmrepo

import "testing"

func testPackages() []Package {
	build := func(name, arch, epoch, version, release string, buildTime int64) Package {
		return Package{
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
	return []Package{
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
			t.Errorf("Query%+v 匹配到 %d 个包，期望 %d 个", tc.query, got, tc.want)
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
		{Query{Arch: "src"}, 1}, // src 同时匹配 nosrc 架构
		{Query{Arch: "aarch64"}, 1},
		{Query{Arch: "*64"}, 3},
		{Query{Name: "docker-ce", Arch: "x86_64"}, 2},
	}
	for _, tc := range cases {
		if got := countMatches(pkgs, tc.query); got != tc.want {
			t.Errorf("Query%+v 匹配到 %d 个包，期望 %d 个", tc.query, got, tc.want)
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
		{Query{Filter: func(p *Package) bool { return p.Arch == "noarch" }}, 1},
	}
	for _, tc := range cases {
		if got := countMatches(pkgs, tc.query); got != tc.want {
			t.Errorf("Query%+v 匹配到 %d 个包，期望 %d 个", tc.query, got, tc.want)
		}
	}
}

func TestQuerySort(t *testing.T) {
	pkgs := testPackages()

	nevrAs := func(pkgs []Package) []string {
		out := make([]string, 0, len(pkgs))
		for i := range pkgs {
			out = append(out, pkgs[i].NEVRA())
		}
		return out
	}
	assertOrder := func(t *testing.T, name string, got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s: 长度为 %d，期望 %d", name, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s: 第 %d 项为 %s，期望 %s（完整结果 %v）", name, i, got[i], want[i], got)
			}
		}
	}

	t.Run("默认排序", func(t *testing.T) {
		sorted := append([]Package(nil), pkgs...)
		Query{}.sort(sorted)
		assertOrder(t, "SortDefault", nevrAs(sorted), []string{
			"Docker-CLI-24.0.7-1.el7.noarch",
			"docker-ce-3:24.0.7-1.el7.aarch64",
			"docker-ce-3:24.0.7-1.el7.nosrc",
			"docker-ce-3:24.0.7-1.el7.x86_64",
			"docker-ce-3:24.0.6-1.el7.x86_64",
		})
	})

	t.Run("版本升序", func(t *testing.T) {
		sorted := append([]Package(nil), pkgs...)
		Query{Sort: SortVersionAsc}.sort(sorted)
		assertOrder(t, "SortVersionAsc", nevrAs(sorted), []string{
			"Docker-CLI-24.0.7-1.el7.noarch",
			"docker-ce-3:24.0.6-1.el7.x86_64",
			"docker-ce-3:24.0.7-1.el7.aarch64",
			"docker-ce-3:24.0.7-1.el7.nosrc",
			"docker-ce-3:24.0.7-1.el7.x86_64",
		})
	})

	t.Run("构建时间降序", func(t *testing.T) {
		sorted := append([]Package(nil), pkgs...)
		Query{Sort: SortBuildTimeDesc}.sort(sorted)
		assertOrder(t, "SortBuildTimeDesc", nevrAs(sorted), []string{
			"Docker-CLI-24.0.7-1.el7.noarch",
			"docker-ce-3:24.0.7-1.el7.nosrc",
			"docker-ce-3:24.0.7-1.el7.aarch64",
			"docker-ce-3:24.0.7-1.el7.x86_64",
			"docker-ce-3:24.0.6-1.el7.x86_64",
		})
	})

	t.Run("保持原顺序", func(t *testing.T) {
		sorted := append([]Package(nil), pkgs...)
		Query{Sort: SortNone}.sort(sorted)
		assertOrder(t, "SortNone", nevrAs(sorted), nevrAs(pkgs))
	})
}

func TestRetainLatest(t *testing.T) {
	latest := retainLatest(testPackages())
	if len(latest) != 4 {
		t.Fatalf("保留 %d 个最新包，期望 4 个（docker-ce 的三个架构加 Docker-CLI）", len(latest))
	}
	for _, pkg := range latest {
		if pkg.Name == "docker-ce" && pkg.Version.Version != "24.0.7" {
			t.Errorf("docker-ce 保留了旧版本 %s", pkg.NEVRA())
		}
	}
}

func TestFindPackagesLimit(t *testing.T) {
	pkgs := testPackages()
	query := Query{Name: "docker-ce", Limit: 2}
	matched := make([]Package, 0, len(pkgs))
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
		t.Errorf("Limit 结果不符合预期: %v", matched)
	}
}

func countMatches(pkgs []Package, query Query) int {
	count := 0
	for i := range pkgs {
		if query.Match(&pkgs[i]) {
			count++
		}
	}
	return count
}
