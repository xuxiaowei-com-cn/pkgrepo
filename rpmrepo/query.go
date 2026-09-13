package rpmrepo

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
	// SortNone keeps the original order from the metadata.
	SortNone
	// SortVersionAsc sorts by name ascending, then by version ascending (oldest first).
	SortVersionAsc
	// SortVersionDesc sorts by name ascending, then by version descending, and finally by
	// architecture ascending.
	SortVersionDesc
	// SortBuildTimeDesc sorts by package build time descending (newest package first).
	SortBuildTimeDesc
	// SortFilenameAsc sorts by package file name ascending.
	SortFilenameAsc
)

// Query describes a single package lookup.
//
// The zero Query matches every package in the repository. The fields are combined with AND.
type Query struct {
	// Name is the package name; it supports shell wildcards (*, ?, [abc]), for example "nginx*".
	Name string
	// Arch is the architecture, for example x86_64, noarch, or src; it also supports wildcards.
	// Passing "src" also matches "nosrc".
	Arch string
	// Provides looks up packages by capability (Provides), for example "webserver" or "docker-ce".
	Provides string
	// Epoch, Version, and Release pin an exact version; leaving them empty means no restriction.
	Epoch   string
	Version string
	Release string
	// When Latest is true, only the newest package is kept for every "name + architecture".
	Latest bool
	// When IgnoreCase is true, name, architecture, capability, and version comparisons all ignore
	// case.
	IgnoreCase bool
	// When Limit is greater than 0, at most Limit packages are returned (truncated after sorting).
	Limit int
	// Sort is the sort order; the zero value is SortDefault.
	Sort SortOrder
	// Filter is a custom filter function; packages for which it returns false are excluded.
	Filter func(*RpmPackage) bool
}

// Match reports whether the package satisfies the query conditions.
func (q Query) Match(p *RpmPackage) bool {
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
	// "src" also matches the "nosrc" architecture used by source packages.
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

// matchField compares a single version field; an empty want means no restriction.
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

// sort orders the results according to q.Sort.
func (q Query) sort(pkgs []RpmPackage) {
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
		// SortDefault and SortVersionDesc: name ascending, version descending, architecture
		// ascending.
		sort.SliceStable(pkgs, func(i, j int) bool { return comparePackages(pkgs[i], pkgs[j], true) < 0 })
	}
}

// comparePackages compares two packages: name ascending, architecture ascending, and version
// ascending or descending depending on newestFirst.
func comparePackages(a, b RpmPackage, newestFirst bool) int {
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

// retainLatest keeps only the newest package for every "name + architecture", preserving the
// original order.
func retainLatest(pkgs []RpmPackage) []RpmPackage {
	best := make(map[string]int, len(pkgs))
	for i := range pkgs {
		key := pkgs[i].Name + "\x00" + pkgs[i].Arch
		index, ok := best[key]
		if !ok || pkgs[i].Version.Compare(pkgs[index].Version) > 0 {
			best[key] = i
		}
	}
	latest := make([]RpmPackage, 0, len(best))
	for i := range pkgs {
		key := pkgs[i].Name + "\x00" + pkgs[i].Arch
		if best[key] == i {
			latest = append(latest, pkgs[i])
		}
	}
	return latest
}
