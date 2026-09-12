package debrepo

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
	// SortNone 保持索引中的原始顺序。
	SortNone
	// SortVersionAsc 先按名称升序，再按版本升序（旧版本在前）。
	SortVersionAsc
	// SortVersionDesc 先按名称升序，再按版本降序，最后按架构升序。
	SortVersionDesc
	// SortFilenameAsc 按包文件名升序。
	SortFilenameAsc
	// SortSizeDesc 按包文件大小降序。
	SortSizeDesc
)

// Query 描述一次二进制软件包查找。
//
// 零值 Query 匹配仓库中的所有软件包。各字段之间是"与"的关系。
type Query struct {
	// Name 是软件包名称，支持 shell 通配符（*、?、[abc]），例如 "nginx*"。
	Name string
	// Arch 是架构，例如 amd64、arm64、all；"any" 或空值表示不限制架构。
	Arch string
	// Component 限定组件（main、contrib、universe…），同样支持通配符。
	Component string
	// Provides 用于按虚拟包（Provides）查找，例如 "mail-transport-agent"。
	Provides string
	// Section、Priority 限定软件分类与优先级，支持通配符。
	Section  string
	Priority string
	// Version 精确匹配版本号（Debian 版本字符串，如 "1:1.22.1-9"）。
	Version string
	// Essential 非空时按 Essential 标记过滤。
	Essential *bool
	// Latest 为 true 时，每个"名称 + 架构 + 组件"只保留版本最新的一个包。
	Latest bool
	// IgnoreCase 为 true 时，名称、架构、组件、版本比较均忽略大小写。
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
	if !q.matchField(p.Name, q.Name) ||
		!q.matchArch(p.Architecture) ||
		!q.matchField(p.Component, q.Component) ||
		!q.matchField(p.Section, q.Section) ||
		!q.matchField(p.Priority, q.Priority) {
		return false
	}
	if q.Version != "" && !q.matchVersion(p.Version.String(), q.Version) {
		return false
	}
	if q.Provides != "" && !q.matchProvides(p, q.Provides) {
		return false
	}
	if q.Essential != nil && p.Essential != *q.Essential {
		return false
	}
	if q.Filter != nil && !q.Filter(p) {
		return false
	}
	return true
}

func (q Query) matchField(got, want string) bool {
	if want == "" {
		return true
	}
	if q.IgnoreCase {
		got, want = strings.ToLower(got), strings.ToLower(want)
	}
	if hasGlobMeta(want) {
		ok, err := path.Match(want, got)
		return err == nil && ok
	}
	return got == want
}

func (q Query) matchArch(arch string) bool {
	if q.Arch == "" || q.Arch == "any" {
		return true
	}
	return q.matchField(arch, q.Arch)
}

func (q Query) matchVersion(got, want string) bool {
	if q.IgnoreCase {
		return strings.EqualFold(got, want)
	}
	return got == want
}

func (q Query) matchProvides(p *Package, name string) bool {
	for _, dep := range p.Provides {
		for _, alt := range dep.Alternatives {
			if q.matchField(alt.Name, name) {
				return true
			}
		}
	}
	return false
}

func hasGlobMeta(s string) bool { return strings.ContainsAny(s, "*?[") }

// sort 按 q.Sort 指定的方式对结果排序。
func (q Query) sort(pkgs []Package) {
	switch q.Sort {
	case SortNone:
		return
	case SortFilenameAsc:
		sort.SliceStable(pkgs, func(i, j int) bool { return pkgs[i].Filename < pkgs[j].Filename })
	case SortSizeDesc:
		sort.SliceStable(pkgs, func(i, j int) bool { return pkgs[i].Size.File > pkgs[j].Size.File })
	case SortVersionAsc:
		sort.SliceStable(pkgs, func(i, j int) bool { return comparePackages(pkgs[i], pkgs[j], false) < 0 })
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
	if c := strings.Compare(a.Architecture, b.Architecture); c != 0 {
		return c
	}
	return strings.Compare(a.Component, b.Component)
}

// retainLatest 对每个"名称 + 架构 + 组件"只保留版本最新的包，保持原有顺序。
func retainLatest(pkgs []Package) []Package {
	best := make(map[string]int, len(pkgs))
	for i := range pkgs {
		key := pkgs[i].Name + "\x00" + pkgs[i].Architecture + "\x00" + pkgs[i].Component
		index, ok := best[key]
		if !ok || pkgs[i].Version.Compare(pkgs[index].Version) > 0 {
			best[key] = i
		}
	}
	latest := make([]Package, 0, len(best))
	for i := range pkgs {
		key := pkgs[i].Name + "\x00" + pkgs[i].Architecture + "\x00" + pkgs[i].Component
		if best[key] == i {
			latest = append(latest, pkgs[i])
		}
	}
	return latest
}

// SourceQuery 描述一次源码包（Sources 索引）查找。
type SourceQuery struct {
	// Name 是源码包名称，支持 shell 通配符。
	Name string
	// Component 限定组件，支持通配符。
	Component string
	// Section、Priority 限定软件分类与优先级。
	Section  string
	Priority string
	// Version 精确匹配版本号。
	Version string
	// Latest 为 true 时，每个"名称 + 组件"只保留版本最新的一个源码包。
	Latest bool
	// IgnoreCase 为 true 时比较忽略大小写。
	IgnoreCase bool
	// Limit 大于 0 时最多返回 Limit 个源码包（在排序之后截断）。
	Limit int
	// Sort 是排序方式，零值为 SortDefault。
	Sort SortOrder
	// Filter 是自定义过滤函数，返回 false 的源码包会被排除。
	Filter func(*Source) bool
}

// Match 判断源码包是否满足查询条件。
func (q SourceQuery) Match(s *Source) bool {
	if s == nil {
		return false
	}
	if !q.matchField(s.Name, q.Name) ||
		!q.matchField(s.Component, q.Component) ||
		!q.matchField(s.Section, q.Section) ||
		!q.matchField(s.Priority, q.Priority) {
		return false
	}
	if q.Version != "" && !q.matchVersion(s.Version.String(), q.Version) {
		return false
	}
	if q.Filter != nil && !q.Filter(s) {
		return false
	}
	return true
}

func (q SourceQuery) matchField(got, want string) bool {
	if want == "" {
		return true
	}
	if q.IgnoreCase {
		got, want = strings.ToLower(got), strings.ToLower(want)
	}
	if hasGlobMeta(want) {
		ok, err := path.Match(want, got)
		return err == nil && ok
	}
	return got == want
}

func (q SourceQuery) matchVersion(got, want string) bool {
	if q.IgnoreCase {
		return strings.EqualFold(got, want)
	}
	return got == want
}

// sort 按 q.Sort 指定的方式对结果排序。
func (q SourceQuery) sort(sources []Source) {
	switch q.Sort {
	case SortNone:
		return
	case SortFilenameAsc:
		sort.SliceStable(sources, func(i, j int) bool { return sources[i].ID() < sources[j].ID() })
	case SortVersionAsc:
		sort.SliceStable(sources, func(i, j int) bool { return compareSources(sources[i], sources[j], false) < 0 })
	default:
		sort.SliceStable(sources, func(i, j int) bool { return compareSources(sources[i], sources[j], true) < 0 })
	}
}

func compareSources(a, b Source, newestFirst bool) int {
	if c := strings.Compare(a.Name, b.Name); c != 0 {
		return c
	}
	if c := a.Version.Compare(b.Version); c != 0 {
		if newestFirst {
			return -c
		}
		return c
	}
	return strings.Compare(a.Component, b.Component)
}

// retainLatestSources 对每个"名称 + 组件"只保留版本最新的源码包。
func retainLatestSources(sources []Source) []Source {
	best := make(map[string]int, len(sources))
	for i := range sources {
		key := sources[i].Name + "\x00" + sources[i].Component
		index, ok := best[key]
		if !ok || sources[i].Version.Compare(sources[index].Version) > 0 {
			best[key] = i
		}
	}
	latest := make([]Source, 0, len(best))
	for i := range sources {
		key := sources[i].Name + "\x00" + sources[i].Component
		if best[key] == i {
			latest = append(latest, sources[i])
		}
	}
	return latest
}
