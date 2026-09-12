package rpmrepo

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// UnixTime is a Unix timestamp (in seconds) from RPM metadata.
type UnixTime int64

// Time converts the timestamp into a UTC time.Time; a zero value means the metadata did not provide
// that time.
func (t UnixTime) Time() time.Time { return time.Unix(int64(t), 0).UTC() }

// IsZero reports whether the timestamp is empty.
func (t UnixTime) IsZero() bool { return t == 0 }

// String returns the time in RFC3339 format, or an empty string when the timestamp is empty.
func (t UnixTime) String() string {
	if t == 0 {
		return ""
	}
	return t.Time().Format(time.RFC3339)
}

// MarshalJSON renders the timestamp as an RFC3339 string, and a zero value as null.
func (t UnixTime) MarshalJSON() ([]byte, error) {
	if t == 0 {
		return []byte("null"), nil
	}
	return json.Marshal(t.String())
}

// UnmarshalXMLAttr parses an attribute form timestamp, for example <time file="1615500000"/>.
func (t *UnixTime) UnmarshalXMLAttr(attr xml.Attr) error { return t.parse(attr.Value) }

// UnmarshalXML parses an element form timestamp, for example <timestamp>1615500000</timestamp>.
func (t *UnixTime) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var raw string
	if err := d.DecodeElement(&raw, &start); err != nil {
		return err
	}
	return t.parse(raw)
}

func (t *UnixTime) parse(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "-" {
		*t = 0
		return nil
	}
	sec, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("rpmrepo: invalid timestamp %q: %w", raw, err)
	}
	*t = UnixTime(sec)
	return nil
}

// XMLBool accepts the various boolean spellings used in RPM metadata: YES/NO, true/false, and 1/0.
type XMLBool bool

// UnmarshalXMLAttr parses a boolean value in attribute form.
func (b *XMLBool) UnmarshalXMLAttr(attr xml.Attr) error {
	switch strings.ToLower(strings.TrimSpace(attr.Value)) {
	case "", "no", "n", "false", "0":
		*b = false
	case "yes", "y", "true", "1":
		*b = true
	default:
		return fmt.Errorf("rpmrepo: invalid boolean value %q", attr.Value)
	}
	return nil
}

// Checksum is the checksum (fingerprint) of a package, for example a sha256 digest.
type Checksum struct {
	// Type is the algorithm name, commonly sha256, sha1, sha512, or md5.
	Type string `xml:"type,attr" json:"type"`
	// Value is the hexadecimal digest value.
	Value string `xml:",chardata" json:"value"`
	// PkgID reports whether this digest can serve as the package's unique identifier
	// (pkgid="YES" in primary.xml).
	PkgID XMLBool `xml:"pkgid,attr" json:"pkg_id,omitempty"`
}

// String returns "type:value", or an empty string when the checksum value is empty.
func (c Checksum) String() string {
	if c.Value == "" {
		return ""
	}
	return c.Type + ":" + c.Value
}

// Location is the position of the package file inside the repository.
type Location struct {
	// Href is the path relative to the repository root, for example
	// Packages/n/nginx-1.24.0-1.el9.x86_64.rpm.
	Href string `xml:"href,attr" json:"href"`
	// Base is the optional xml:base attribute, which overrides the base address used for relative
	// paths.
	Base string `xml:"base,attr" json:"base,omitempty"`
}

// Size holds the various sizes of a package file (in bytes).
type Size struct {
	// Package is the size of the rpm file.
	Package int64 `xml:"package,attr" json:"package"`
	// Installed is the disk space used after installation.
	Installed int64 `xml:"installed,attr" json:"installed"`
	// Archive is the total size of the archive files inside the package.
	Archive int64 `xml:"archive,attr" json:"archive"`
}

// Time holds the time a package was added to the repository and the time it was built.
type Time struct {
	// File is the time the package was added to the repository.
	File UnixTime `xml:"file,attr" json:"file"`
	// Build is the time the package was built.
	Build UnixTime `xml:"build,attr" json:"build"`
}

// HeaderRange is the byte range of the rpm header inside the file; it can be used to download only
// the header for incremental analysis.
type HeaderRange struct {
	Start int64 `xml:"start,attr" json:"start"`
	End   int64 `xml:"end,attr" json:"end"`
}

// Dependency is a single dependency relation (Provides/Requires/Conflicts/Obsoletes, and so on).
type Dependency struct {
	// Name is the capability name, for example nginx or libc.so.6()(64bit).
	Name string `xml:"name,attr" json:"name"`
	// Flags is the version constraint, one of EQ, LT, LE, GT, or GE; empty means no version
	// restriction.
	Flags string `xml:"flags,attr" json:"flags,omitempty"`
	// Epoch, Version, and Release are the versions involved in the constraint; they only appear when
	// a version constraint is present.
	Epoch   string `xml:"epoch,attr" json:"epoch,omitempty"`
	Version string `xml:"ver,attr" json:"version,omitempty"`
	Release string `xml:"rel,attr" json:"release,omitempty"`
	// Pre indicates that the dependency must be satisfied before installation.
	Pre XMLBool `xml:"pre,attr" json:"pre,omitempty"`
}

// EVR returns the version constraint of the dependency.
func (d Dependency) EVR() EVR {
	return EVR{Epoch: d.Epoch, Version: d.Version, Release: d.Release}
}

// IsVersioned reports whether the dependency carries a version constraint.
func (d Dependency) IsVersioned() bool { return d.Flags != "" || d.Version != "" }

// String returns a human-readable dependency description, for example "nginx >= 1.24.0-1.el9".
func (d Dependency) String() string {
	if !d.IsVersioned() {
		return d.Name
	}
	return d.Name + " " + d.Operator() + " " + d.EVR().String()
}

// Operator converts the rpm flags into a symbol such as ">=", or returns an empty string when there
// is no version constraint.
func (d Dependency) Operator() string {
	switch strings.ToUpper(d.Flags) {
	case "EQ":
		return "="
	case "LT":
		return "<"
	case "LE":
		return "<="
	case "GT":
		return ">"
	case "GE":
		return ">="
	default:
		return ""
	}
}

// Format is the extended metadata from the rpm header.
type Format struct {
	License     string      `xml:"license" json:"license,omitempty"`
	Vendor      string      `xml:"vendor" json:"vendor,omitempty"`
	Group       string      `xml:"group" json:"group,omitempty"`
	BuildHost   string      `xml:"buildhost" json:"build_host,omitempty"`
	SourceRPM   string      `xml:"sourcerpm" json:"source_rpm,omitempty"`
	HeaderRange HeaderRange `xml:"header-range" json:"header_range"`

	Provides    []Dependency `xml:"provides>entry" json:"provides,omitempty"`
	Requires    []Dependency `xml:"requires>entry" json:"requires,omitempty"`
	Conflicts   []Dependency `xml:"conflicts>entry" json:"conflicts,omitempty"`
	Obsoletes   []Dependency `xml:"obsoletes>entry" json:"obsoletes,omitempty"`
	Recommends  []Dependency `xml:"recommends>entry" json:"recommends,omitempty"`
	Suggests    []Dependency `xml:"suggests>entry" json:"suggests,omitempty"`
	Supplements []Dependency `xml:"supplements>entry" json:"supplements,omitempty"`
	Enhances    []Dependency `xml:"enhances>entry" json:"enhances,omitempty"`
}

// Package is the metadata of a single package, corresponding to one <package> element in
// primary.xml.
type Package struct {
	// Type is usually rpm.
	Type string `xml:"type,attr" json:"type"`
	// Name is the package name.
	Name string `xml:"name" json:"name"`
	// Arch is the architecture, for example x86_64, aarch64, noarch, or src.
	Arch string `xml:"arch" json:"arch"`
	// Version is the version information (epoch/ver/rel).
	Version EVR `xml:"version" json:"version"`
	// Checksum is the checksum (fingerprint) of the package file.
	Checksum Checksum `xml:"checksum" json:"checksum"`
	// Summary is the package summary.
	Summary string `xml:"summary" json:"summary,omitempty"`
	// Description is the package description.
	Description string `xml:"description" json:"description,omitempty"`
	// Packager is the packager.
	Packager string `xml:"packager" json:"packager,omitempty"`
	// URL is the upstream project address.
	URL string `xml:"url" json:"url,omitempty"`
	// Time is the time the package was added and built.
	Time Time `xml:"time" json:"time"`
	// Size is the size of the package file, the installed size, and the archive size.
	Size Size `xml:"size" json:"size"`
	// Location is the relative path of the package file inside the repository.
	Location Location `xml:"location" json:"location"`
	// Format is the extended metadata from the rpm header, including all dependencies.
	Format Format `xml:"format" json:"format"`

	// DownloadURL is the resolved absolute download address; it is populated when the repository
	// loads the package.
	DownloadURL string `xml:"-" json:"download_url,omitempty"`
	// RepoURL is the root address of the repository the package belongs to.
	RepoURL string `xml:"-" json:"repo_url,omitempty"`
	// RepoID is the identifier of the repository the package belongs to.
	RepoID string `xml:"-" json:"repo_id,omitempty"`
}

// NEVRA returns the unique identifier of the package: name-epoch:version-release.arch.
func (p *Package) NEVRA() string {
	return p.Name + "-" + p.Version.String() + "." + p.Arch
}

// Filename returns the name of the package file, for example nginx-1.24.0-1.el9.x86_64.rpm.
func (p *Package) Filename() string {
	base := p.Location.Href
	if idx := strings.LastIndexByte(base, '/'); idx >= 0 {
		base = base[idx+1:]
	}
	if unescaped, err := url.PathUnescape(base); err == nil {
		return unescaped
	}
	return base
}

// IsSource reports whether the package is a source package.
func (p *Package) IsSource() bool { return p.Arch == "src" || p.Arch == "nosrc" }

// Provides reports whether the package provides the given capability.
func (p *Package) Provides(name string) bool { return hasDependency(p.Format.Provides, name) }

// Requires reports whether the package requires the given capability.
func (p *Package) Requires(name string) bool { return hasDependency(p.Format.Requires, name) }

func hasDependency(deps []Dependency, name string) bool {
	for _, dep := range deps {
		if dep.Name == name {
			return true
		}
	}
	return false
}

// String returns the NEVRA of the package.
func (p *Package) String() string { return p.NEVRA() }
