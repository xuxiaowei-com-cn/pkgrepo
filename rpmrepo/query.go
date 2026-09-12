package rpmrepo

import (
	"path"
	"sort"
	"strings"
)

// SortOrder 是结果排序方式。
type SortOrder int

const (
	// SortDefault 先按名称升序，再按版本降序（新版本在前），最后按架构升序。
	SortDefault SortOrder = iota
	// SortNone 保持元数据中的原始顺序。
	SortNone
	// SortVersionAsc 先按名称升序，再按版本升序（旧版本在前）。
	SortVersionAsc
	// SortVersionDesc 先按名称升序，再按版本降序，最后按架构升序。
	SortVersionDesc
	// SortBuildTimeDesc 按包构建时间降序（最新的包在前）。
	SortBuildTimeDesc
	// SortFilenameAsc 按包文件名升序。
	SortFilenameAsc
)

// Query 描述一次软件包查找。
//
// 零值 Query 匹配仓库中的所有软件包。各字段之间是"与"的关系。
type Query struct {
	// Name 是软件包名称，支持 shell 通配符（*、?、[abc]），例如 "nginx*"。
	Name string
	// Arch 是架构，例如 x86_64、noarch、src，同样支持通配符。
	// 传 "src" 时也会匹配 "nosrc"。
	Arch string
	// Provides 用于按能力（Provides）查找，例如 "webserver"、"docker-ce"。
	Provides string
	// Epoch、Version、Release 用于精确锁定版本，留空表示不限制。
	Epoch   string
	Version string
	Release string
	// Latest 为 true 时，每个"名称 + 架构"只保留版本最新的一个包。
	Latest bool
	// IgnoreCase 为 true 时，名称、架构、能力、版本比较均忽略大小写。
	IgnoreCase bool
	// Limit 大于 0 时最多返回 Limit 个包（在排序之后截断）。
	Limit int
	// Sort 是排序方式，零值为 SortDefault。
	Sort SortOrder
	// Filter 是自定义过滤函数，返回 false 的包会被排除。
	Filter func(*Package) bool
}

// Match 判断软件包是否满足查询条件。
func (q Query) Match(p *Package) bool {
	if p == nil {
		return false
	}
	if !q.matchName(p.Name) || !q.matchArch(p.Arch) {
		return false
	}
	if q.Provides != "" && !q.matchDependency(p.Format.Provides, q.Provides) {
		return false
	}
	if !q.matchField(p.Version.Epoch, q.Epoch) ||
		!q.matchField(p.Version.Version, q.Version) ||
		!q.matchField(p.Version.Release, q.Release) {
		return false
	}
	if q.Filter != nil && !q.Filter(p) {
		return false
	}
	return true
}

func (q Query) matchName(name string) bool {
	if q.Name == "" {
		return true
	}
	want, got := q.Name, name
	if q.IgnoreCase {
		want, got = strings.ToLower(want), strings.ToLower(got)
	}
	if hasGlobMeta(want) {
		ok, err := path.Match(want, got)
		return err == nil && ok
	}
	return want == got
}

func (q Query) matchArch(arch string) bool {
	if q.Arch == "" {
		return true
	}
	want, got := q.Arch, arch
	if q.IgnoreCase {
		want, got = strings.ToLower(want), strings.ToLower(got)
	}
	if want == got {
		return true
	}
	if hasGlobMeta(want) {
		ok, err := path.Match(want, got)
		return err == nil && ok
	}
	// "src" 同时匹配源码包使用的 "nosrc" 架构。
	return want == "src" && got == "nosrc"
}

func (q Query) matchDependency(deps []Dependency, name string) bool {
	want := name
	if q.IgnoreCase {
		want = strings.ToLower(want)
	}
	for _, dep := range deps {
		got := dep.Name
		if q.IgnoreCase {
			got = strings.ToLower(got)
		}
		if hasGlobMeta(want) {
			if ok, err := path.Match(want, got); err == nil && ok {
				return true
			}
			continue
		}
		if got == want {
			return true
		}
	}
	return false
}

// matchField 比较单个版本字段，want 为空表示不限制。
func (q Query) matchField(got, want string) bool {
	if want == "" {
		return true
	}
	if q.IgnoreCase {
		return strings.EqualFold(got, want)
	}
	return got == want
}

func hasGlobMeta(s string) bool { return strings.ContainsAny(s, "*?[") }

// sort 按 q.Sort 指定的方式对结果排序。
func (q Query) sort(pkgs []Package) {
	switch q.Sort {
	case SortNone:
		return
	case SortBuildTimeDesc:
		sort.SliceStable(pkgs, func(i, j int) bool {
			return pkgs[i].Time.Build > pkgs[j].Time.Build
		})
	case SortVersionAsc:
		sort.SliceStable(pkgs, func(i, j int) bool { return comparePackages(pkgs[i], pkgs[j], false) < 0 })
	case SortFilenameAsc:
		sort.SliceStable(pkgs, func(i, j int) bool {
			return pkgs[i].Filename() < pkgs[j].Filename()
		})
	default:
		// SortDefault 与 SortVersionDesc：名称升序、版本降序、架构升序。
		sort.SliceStable(pkgs, func(i, j int) bool { return comparePackages(pkgs[i], pkgs[j], true) < 0 })
	}
}

// comparePackages 比较两个包：名称升序、架构升序，版本按 newestFirst 决定升降序。
func comparePackages(a, b Package, newestFirst bool) int {
	if c := strings.Compare(a.Name, b.Name); c != 0 {
		return c
	}
	if c := a.Version.Compare(b.Version); c != 0 {
		if newestFirst {
			return -c
		}
		return c
	}
	return strings.Compare(a.Arch, b.Arch)
}

// retainLatest 对每个"名称 + 架构"只保留版本最新的包，保持原有顺序。
func retainLatest(pkgs []Package) []Package {
	best := make(map[string]int, len(pkgs))
	for i := range pkgs {
		key := pkgs[i].Name + "\x00" + pkgs[i].Arch
		index, ok := best[key]
		if !ok || pkgs[i].Version.Compare(pkgs[index].Version) > 0 {
			best[key] = i
		}
	}
	latest := make([]Package, 0, len(best))
	for i := range pkgs {
		key := pkgs[i].Name + "\x00" + pkgs[i].Arch
		if best[key] == i {
			latest = append(latest, pkgs[i])
		}
	}
	return latest
}
