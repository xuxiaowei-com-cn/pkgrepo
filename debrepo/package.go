package debrepo

import (
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Size holds the size information of a package.
type Size struct {
	// File is the size of the .deb file in bytes, corresponding to the Size field in the index.
	File int64 `json:"file"`
	// Installed is the disk space used after installation (KiB), corresponding to the
	// Installed-Size field in the index.
	Installed int64 `json:"installed"`
}

// Description is the description of a package: Synopsis is the first-line summary and Long is the
// full description.
type Description struct {
	Synopsis string `json:"synopsis,omitempty"`
	Long     string `json:"long,omitempty"`
}

// String returns the full description (summary plus long description).
func (d Description) String() string {
	if d.Long == "" {
		return d.Synopsis
	}
	if d.Synopsis == "" {
		return d.Long
	}
	return d.Synopsis + "\n" + d.Long
}

// IsZero reports whether the description is empty.
func (d Description) IsZero() bool { return d.Synopsis == "" && d.Long == "" }

// DebPackage is the metadata of a single binary package, corresponding to one paragraph in the
// Packages index.
//
// The field names match those of the Debian control file; the struct also carries context such as
// DownloadURL, RepoURL, RepoID, Suite, and Component, which the repository fills in automatically.
type DebPackage struct {
	// Name is the package name.
	Name string `json:"name"`
	// Source is the source package name and SourceVersion is the version in parentheses inside the
	// Source field (usually empty).
	Source        string `json:"source,omitempty"`
	SourceVersion string `json:"source_version,omitempty"`
	// Version is the version number (epoch:upstream-revision).
	Version Version `json:"version"`
	// Architecture is the architecture, for example amd64, arm64, or all.
	Architecture string `json:"architecture"`
	// MultiArch is the Multi-Arch flag: same, foreign, allowed, or no.
	MultiArch string `json:"multi_arch,omitempty"`
	// Essential indicates that the package is required by the system.
	Essential bool `json:"essential,omitempty"`
	// Priority is the priority (required, important, standard, optional, extra).
	Priority string `json:"priority,omitempty"`
	// Section is the software category (such as utils or net).
	Section string `json:"section,omitempty"`
	// Description is the summary and the full description.
	Description Description `json:"description,omitempty"`
	// Maintainer is the maintainer and OriginalMaintainer is the original maintainer (Ubuntu's
	// XSBC-Original-Maintainer).
	Maintainer         string `json:"maintainer,omitempty"`
	OriginalMaintainer string `json:"original_maintainer,omitempty"`
	// Homepage is the upstream project address.
	Homepage string `json:"homepage,omitempty"`

	// Size is the package file size and the installed size.
	Size Size `json:"size"`
	// Filename is the path of the package file relative to the repository root, for example
	// pool/main/n/nginx/nginx_1.22.1-9_amd64.deb.
	Filename string `json:"filename"`
	// DownloadURL is the resolved absolute download address; it is populated when the repository
	// loads the package.
	DownloadURL string `json:"download_url,omitempty"`
	// Checksums are the checksums of the package file (MD5/SHA1/SHA256/SHA512).
	Checksums Checksums `json:"checksums"`

	// Dependency relations, one for one with the Debian fields.
	Depends    Dependencies `json:"depends,omitempty"`
	PreDepends Dependencies `json:"pre_depends,omitempty"`
	Recommends Dependencies `json:"recommends,omitempty"`
	Suggests   Dependencies `json:"suggests,omitempty"`
	Breaks     Dependencies `json:"breaks,omitempty"`
	Conflicts  Dependencies `json:"conflicts,omitempty"`
	Provides   Dependencies `json:"provides,omitempty"`
	Replaces   Dependencies `json:"replaces,omitempty"`
	Enhances   Dependencies `json:"enhances,omitempty"`
	// BuiltUsing lists the source packages used to build this package (the Built-Using field).
	BuiltUsing Dependencies `json:"built_using,omitempty"`

	// Suite and Component are the distribution and component the package belongs to.
	Suite     string `json:"suite,omitempty"`
	Component string `json:"component,omitempty"`
	// RepoURL is the root address of the repository the package belongs to and RepoID is the
	// repository identifier.
	RepoURL string `json:"repo_url,omitempty"`
	RepoID  string `json:"repo_id,omitempty"`

	// Fields holds every field of the index (keyed by lower-case field name), which makes fields that
	// have no struct counterpart — Tag, Task, Bugs, Package-Type, and so on — easy to read.
	Fields map[string]string `json:"fields,omitempty"`
}

// packageDependencyFields lists the DebPackage fields that must be parsed as dependency relations.
var packageDependencyFields = []struct {
	field string
	set   func(*DebPackage, Dependencies)
}{
	{"Depends", func(p *DebPackage, d Dependencies) { p.Depends = d }},
	{"Pre-Depends", func(p *DebPackage, d Dependencies) { p.PreDepends = d }},
	{"Recommends", func(p *DebPackage, d Dependencies) { p.Recommends = d }},
	{"Suggests", func(p *DebPackage, d Dependencies) { p.Suggests = d }},
	{"Breaks", func(p *DebPackage, d Dependencies) { p.Breaks = d }},
	{"Conflicts", func(p *DebPackage, d Dependencies) { p.Conflicts = d }},
	{"Provides", func(p *DebPackage, d Dependencies) { p.Provides = d }},
	{"Replaces", func(p *DebPackage, d Dependencies) { p.Replaces = d }},
	{"Enhances", func(p *DebPackage, d Dependencies) { p.Enhances = d }},
	{"Built-Using", func(p *DebPackage, d Dependencies) { p.BuiltUsing = d }},
}

// ParsePackages streams over the Packages index, calling fn once per parsed package.
//
// An error returned by fn stops parsing and is returned unchanged, which makes it easy to exit the
// iteration early. Because parsing is streaming, memory usage stays constant even for repositories
// with hundreds of thousands of packages.
func ParsePackages(r io.Reader, fn func(*DebPackage) error) error {
	if fn == nil {
		return nil
	}
	return ParseStanzasFunc(r, func(stanza *Stanza) error {
		pkg, err := ParsePackageStanza(stanza)
		if err != nil {
			return err
		}
		return fn(pkg)
	})
}

// ParsePackageStanza parses one paragraph of the Packages index into a DebPackage.
func ParsePackageStanza(stanza *Stanza) (*DebPackage, error) {
	pkg := &DebPackage{}
	pkg.Name = stanza.Get("Package")
	if pkg.Name == "" {
		return nil, fmt.Errorf("%w: missing the Package field", ErrInvalidPackage)
	}
	pkg.Source, pkg.SourceVersion = parseSourceField(stanza.Get("Source"))
	if pkg.Source == "" {
		pkg.Source = pkg.Name
	}
	pkg.Architecture = stanza.Get("Architecture")
	pkg.MultiArch = stanza.Get("Multi-Arch")
	pkg.Essential = parseYesNo(stanza.Get("Essential"))
	pkg.Priority = stanza.Get("Priority")
	pkg.Section = stanza.Get("Section")
	pkg.Description = parseDescription(stanza.Get("Description"))
	pkg.Maintainer = stanza.Get("Maintainer")
	pkg.OriginalMaintainer = firstNonEmpty(stanza.Get("Original-Maintainer"), stanza.Get("XSBC-Original-Maintainer"))
	pkg.Homepage = stanza.Get("Homepage")
	pkg.Filename = stanza.Get("Filename")
	pkg.Checksums = Checksums{
		MD5:    stanza.Get("MD5sum"),
		SHA1:   stanza.Get("SHA1"),
		SHA256: stanza.Get("SHA256"),
		SHA512: stanza.Get("SHA512"),
	}

	version := stanza.Get("Version")
	if version != "" {
		parsed, err := ParseVersion(version)
		if err != nil {
			return nil, fmt.Errorf("%w: %s %s", ErrInvalidPackage, pkg.Name, err)
		}
		pkg.Version = parsed
	}
	var err error
	if pkg.Size.File, err = parseOptionalInt(stanza.Get("Size")); err != nil {
		return nil, fmt.Errorf("%w: invalid Size field of %s: %w", ErrInvalidPackage, pkg.Name, err)
	}
	if pkg.Size.Installed, err = parseOptionalInt(stanza.Get("Installed-Size")); err != nil {
		return nil, fmt.Errorf("%w: invalid Installed-Size field of %s: %w", ErrInvalidPackage, pkg.Name, err)
	}

	for _, item := range packageDependencyFields {
		value := stanza.Get(item.field)
		if value == "" {
			continue
		}
		deps, err := ParseDependencies(value)
		if err != nil {
			return nil, fmt.Errorf("%w: %s field of %s: %w", ErrInvalidPackage, item.field, pkg.Name, err)
		}
		item.set(pkg, deps)
	}

	pkg.Fields = make(map[string]string, stanza.Len())
	for _, name := range stanza.Names() {
		pkg.Fields[strings.ToLower(name)] = stanza.Get(name)
	}
	return pkg, nil
}

// parseSourceField parses the "Source" field: it may be just a name or may carry "(version)".
func parseSourceField(raw string) (name, version string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if open := strings.IndexByte(raw, '('); open >= 0 {
		if closeIdx := strings.IndexByte(raw[open:], ')'); closeIdx >= 0 {
			version = strings.TrimSpace(raw[open+1 : open+closeIdx])
			name = strings.TrimSpace(raw[:open])
			return name, version
		}
	}
	return raw, ""
}

// parseDescription splits a Debian multi-line description: the first line is the summary and the
// remaining lines are the long description; a line containing only "." means a blank line.
func parseDescription(raw string) Description {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Description{}
	}
	lines := strings.Split(raw, "\n")
	desc := Description{Synopsis: strings.TrimSpace(lines[0])}
	long := make([]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "." {
			long = append(long, "")
			continue
		}
		long = append(long, line)
	}
	// Trim leading and trailing blank lines from the long description.
	for len(long) > 0 && strings.TrimSpace(long[0]) == "" {
		long = long[1:]
	}
	for len(long) > 0 && strings.TrimSpace(long[len(long)-1]) == "" {
		long = long[:len(long)-1]
	}
	desc.Long = strings.Join(long, "\n")
	return desc
}

// parseOptionalInt parses an optional integer field; an empty string returns 0.
func parseOptionalInt(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// SourceName returns the source package name (which is the package name when the Source field is
// absent).
func (p *DebPackage) SourceName() string {
	if p.Source != "" {
		return p.Source
	}
	return p.Name
}

// IsArchitectureIndependent reports whether the package is architecture-independent
// (Architecture: all).
func (p *DebPackage) IsArchitectureIndependent() bool { return p.Architecture == "all" }

// Checksum returns the strongest available checksum (sha512 > sha256 > sha1 > md5).
func (p *DebPackage) Checksum() (Checksum, bool) { return p.Checksums.Strongest() }

// BaseFilename returns the package file name (the last segment of Filename).
func (p *DebPackage) BaseFilename() string {
	if idx := strings.LastIndexByte(p.Filename, '/'); idx >= 0 {
		return p.Filename[idx+1:]
	}
	return p.Filename
}

// ID returns an identifier of the form "nginx_1.22.1-9_amd64".
func (p *DebPackage) ID() string {
	parts := []string{p.Name}
	if !p.Version.IsZero() {
		parts = append(parts, p.Version.String())
	}
	if p.Architecture != "" {
		parts = append(parts, p.Architecture)
	}
	return strings.Join(parts, "_")
}

// String returns the package identifier (the same as ID).
func (p *DebPackage) String() string { return p.ID() }

// Field returns the raw field value from the index; the field name is case-insensitive.
func (p *DebPackage) Field(name string) string { return p.Fields[strings.ToLower(name)] }

// HasField reports whether the given field exists in the index.
func (p *DebPackage) HasField(name string) bool {
	_, ok := p.Fields[strings.ToLower(name)]
	return ok
}

// ProvidesPackage reports whether the package provides the given virtual package (Provides field).
func (p *DebPackage) ProvidesPackage(name string) bool { return p.Provides.Has(name) }

// DependsOn reports whether the package directly depends on the given package (Depends field,
// including alternatives).
func (p *DebPackage) DependsOn(name string) bool { return p.Depends.Has(name) }
