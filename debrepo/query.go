package debrepo

import (
	"path"
	"sort"
	"strings"
)

// SortOrder is the way results are sorted.
type SortOrder int

const (
	// SortDefault sorts by name ascending, then by version descending (newest first), and finally by
	// architecture ascending.
	SortDefault SortOrder = iota
	// SortNone keeps the original order from the index.
	SortNone
	// SortVersionAsc sorts by name ascending, then by version ascending (oldest first).
	SortVersionAsc
	// SortVersionDesc sorts by name ascending, then by version descending, and finally by
	// architecture ascending.
	SortVersionDesc
	// SortFilenameAsc sorts by package file name ascending.
	SortFilenameAsc
	// SortSizeDesc sorts by package file size descending.
	SortSizeDesc
)

// Query describes a single binary package lookup.
//
// The zero Query matches every package in the repository. The fields are combined with AND.
type Query struct {
	// Name is the package name; it supports shell wildcards (*, ?, [abc]), for example "nginx*".
	Name string
	// Arch is the architecture, for example amd64, arm64, or all; "any" or an empty value means no
	// architecture restriction.
	Arch string
	// Component restricts the component (main, contrib, universe, ...); it also supports wildcards.
	Component string
	// Provides looks up packages by virtual package (Provides), for example "mail-transport-agent".
	Provides string
	// Section and Priority restrict the software category and priority; they support wildcards.
	Section  string
	Priority string
	// Version matches the version number exactly (a Debian version string such as "1:1.22.1-9").
	Version string
	// Essential filters by the Essential flag when it is non-nil.
	Essential *bool
	// When Latest is true, only the newest package is kept for every "name + architecture +
	// component".
	Latest bool
	// When IgnoreCase is true, name, architecture, component, and version comparisons all ignore
	// case.
	IgnoreCase bool
	// When Limit is greater than 0, at most Limit packages are returned (truncated after sorting).
	Limit int
	// Sort is the sort order; the zero value is SortDefault.
	Sort SortOrder
	// Filter is a custom filter function; packages for which it returns false are excluded.
	Filter func(*Package) bool
}

// Match reports whether the package satisfies the query conditions.
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

// sort orders the results according to q.Sort.
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
		// SortDefault and SortVersionDesc: name ascending, version descending, architecture
		// ascending.
		sort.SliceStable(pkgs, func(i, j int) bool { return comparePackages(pkgs[i], pkgs[j], true) < 0 })
	}
}

// comparePackages compares two packages: name ascending, architecture ascending, and version
// ascending or descending depending on newestFirst.
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

// retainLatest keeps only the newest package for every "name + architecture + component", preserving
// the original order.
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

// SourceQuery describes a single source package (Sources index) lookup.
type SourceQuery struct {
	// Name is the source package name; it supports shell wildcards.
	Name string
	// Component restricts the component; it supports wildcards.
	Component string
	// Section and Priority restrict the software category and priority.
	Section  string
	Priority string
	// Version matches the version number exactly.
	Version string
	// When Latest is true, only the newest source package is kept for every "name + component".
	Latest bool
	// When IgnoreCase is true, comparisons ignore case.
	IgnoreCase bool
	// When Limit is greater than 0, at most Limit source packages are returned (truncated after
	// sorting).
	Limit int
	// Sort is the sort order; the zero value is SortDefault.
	Sort SortOrder
	// Filter is a custom filter function; source packages for which it returns false are excluded.
	Filter func(*Source) bool
}

// Match reports whether the source package satisfies the query conditions.
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

// sort orders the results according to q.Sort.
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

// retainLatestSources keeps only the newest source package for every "name + component".
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
