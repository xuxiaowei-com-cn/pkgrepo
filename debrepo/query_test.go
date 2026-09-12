package debrepo

import (
	"strings"
	"testing"
)

func testPackages() []Package {
	makePackage := func(name, version, arch, component string) Package {
		pkg := Package{
			Name:         name,
			Version:      MustParseVersion(version),
			Architecture: arch,
			Component:    component,
			Filename:     "pool/main/" + name + "/" + name + "_" + version + "_" + arch + ".deb",
		}
		pkg.Size.File = int64(len(version) * 1000)
		return pkg
	}
	return []Package{
		makePackage("nginx", "1.22.1-9", "amd64", "main"),
		makePackage("nginx", "1.24.0-1", "amd64", "main"),
		makePackage("nginx", "1.24.0-1", "arm64", "main"),
		makePackage("nginx", "1.20.0-1", "amd64", "main"),
		makePackage("nginx-doc", "1.24.0-1", "all", "main"),
		makePackage("curl", "7.88.1-10", "amd64", "main"),
		makePackage("docker-ce", "5:27.0.3-1", "amd64", "stable"),
	}
}

func TestQueryMatch(t *testing.T) {
	pkgs := testPackages()
	cases := []struct {
		query Query
		want  []string
	}{
		{Query{Name: "nginx"}, []string{"nginx", "nginx", "nginx", "nginx"}},
		{Query{Name: "ng*"}, []string{"nginx", "nginx", "nginx", "nginx", "nginx-doc"}},
		{Query{Name: "nginx", Arch: "arm64"}, []string{"nginx"}},
		{Query{Name: "nginx", Arch: "any"}, []string{"nginx", "nginx", "nginx", "nginx"}},
		{Query{Name: "nginx-doc", Arch: "all"}, []string{"nginx-doc"}},
		{Query{Name: "docker-*", Component: "stable"}, []string{"docker-ce"}},
		{Query{Name: "docker-*", Component: "main"}, nil},
		{Query{Name: "NGINX", IgnoreCase: true, Arch: "ARM64"}, []string{"nginx"}},
		{Query{Name: "nginx", Version: "1.22.1-9"}, []string{"nginx"}},
		{Query{Name: "nginx", Version: "1.22.1"}, nil},
	}
	for _, tc := range cases {
		var matched []Package
		for i := range pkgs {
			if tc.query.Match(&pkgs[i]) {
				matched = append(matched, pkgs[i])
			}
		}
		if len(matched) != len(tc.want) {
			t.Errorf("Query.Match(%+v) 匹配 %d 个包，期望 %d", tc.query, len(matched), len(tc.want))
		}
	}
}

func TestQueryProvidesAndEssential(t *testing.T) {
	nginx := Package{
		Name:    "nginx",
		Version: MustParseVersion("1.24.0-1"),
		Provides: Dependencies{
			{Alternatives: []Constraint{{Name: "httpd"}, {Name: "httpd-cgi"}}},
		},
	}
	essential := Package{Name: "bash", Version: MustParseVersion("5.2.15-2"), Essential: true}
	if !(Query{Provides: "httpd"}).Match(&nginx) {
		t.Error("Provide 查找应当匹配 httpd")
	}
	if (Query{Provides: "httpd-server"}).Match(&nginx) {
		t.Error("Provide 查找不应匹配 httpd-server")
	}
	if !(Query{Provides: "*"}).Match(&nginx) {
		t.Error("通配符 Provide 查找应当匹配")
	}
	yes, no := true, false
	if !(Query{Essential: &yes}).Match(&essential) || (Query{Essential: &yes}).Match(&nginx) {
		t.Error("Essential=true 过滤错误")
	}
	if !(Query{Essential: &no}).Match(&nginx) || (Query{Essential: &no}).Match(&essential) {
		t.Error("Essential=false 过滤错误")
	}
}

func TestQuerySortAndLatest(t *testing.T) {
	pkgs := testPackages()

	query := Query{Name: "nginx"}
	query.sort(pkgs)
	var order []string
	for _, pkg := range pkgs {
		if pkg.Name == "nginx" {
			order = append(order, pkg.Version.String()+"/"+pkg.Architecture)
		}
	}
	want := "1.24.0-1/amd64,1.24.0-1/arm64,1.22.1-9/amd64,1.20.0-1/amd64"
	if got := strings.Join(order, ","); got != want {
		t.Errorf("默认排序 = %q，期望 %q", got, want)
	}

	latest := retainLatest(testPackages())
	var latestNames []string
	for _, pkg := range latest {
		latestNames = append(latestNames, pkg.Name+"_"+pkg.Architecture)
	}
	wantLatest := "nginx_amd64,nginx_arm64,nginx-doc_all,curl_amd64,docker-ce_amd64"
	if got := strings.Join(latestNames, ","); got != wantLatest {
		t.Errorf("Latest 结果 = %q，期望 %q", got, wantLatest)
	}

	ascending := testPackages()
	Query{Sort: SortVersionAsc}.sort(ascending)
	if ascending[0].Name != "curl" {
		t.Errorf("版本升序第一个包 = %q", ascending[0].Name)
	}

	bySize := testPackages()
	Query{Sort: SortSizeDesc}.sort(bySize)
	if bySize[0].Size.File < bySize[len(bySize)-1].Size.File {
		t.Error("SortSizeDesc 排序错误")
	}

	original := testPackages()
	Query{Sort: SortNone}.sort(original)
	if original[0].Version.String() != "1.22.1-9" {
		t.Errorf("SortNone 不应改变顺序，实际 %q", original[0].Version.String())
	}
}

func TestQueryFilter(t *testing.T) {
	pkg := Package{Name: "nginx", Version: MustParseVersion("1.24.0-1")}
	query := Query{Name: "nginx", Filter: func(p *Package) bool { return p.Name != "nginx" }}
	if query.Match(&pkg) {
		t.Error("Filter 返回 false 时不应匹配")
	}
	if (Query{}).Match(nil) {
		t.Error("nil 包不应匹配")
	}
}

func TestSourceQuery(t *testing.T) {
	sources := []Source{
		{Name: "nginx", Version: MustParseVersion("1.22.1-9"), Component: "main", Section: "httpd"},
		{Name: "nginx", Version: MustParseVersion("1.24.0-1"), Component: "main", Section: "httpd"},
		{Name: "curl", Version: MustParseVersion("7.88.1-10"), Component: "main", Section: "web"},
	}
	query := SourceQuery{Name: "nginx", Latest: true}
	var matched []Source
	for i := range sources {
		if query.Match(&sources[i]) {
			matched = append(matched, sources[i])
		}
	}
	matched = retainLatestSources(matched)
	if len(matched) != 1 || matched[0].Version.String() != "1.24.0-1" {
		t.Errorf("Latest 源码包 = %+v", matched)
	}
	if !(SourceQuery{Name: "ng*", Section: "http*"}).Match(&sources[0]) {
		t.Error("源码包通配符匹配失败")
	}
	if (SourceQuery{Section: "web"}).Match(&sources[0]) {
		t.Error("源码包 Section 过滤失败")
	}
	if (SourceQuery{}).Match(nil) {
		t.Error("nil 源码包不应匹配")
	}
}
