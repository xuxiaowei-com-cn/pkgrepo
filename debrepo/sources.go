package debrepo

import (
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
)

// Source is the metadata of a single source package, corresponding to one paragraph in the Sources
// index.
type Source struct {
	// Name is the source package name (the Package field).
	Name string `json:"name"`
	// Binary is the list of binary package names produced by this source package.
	Binary []string `json:"binary,omitempty"`
	// Version is the version number.
	Version Version `json:"version"`
	// Architecture is the architecture supported by the source package (any, all, or a list of
	// specific architectures).
	Architecture string `json:"architecture,omitempty"`
	// Maintainer and Uploaders are the maintainer and the uploaders.
	Maintainer string   `json:"maintainer,omitempty"`
	Uploaders  []string `json:"uploaders,omitempty"`
	// Homepage, Section, and Priority are the same as for a binary package.
	Homepage string `json:"homepage,omitempty"`
	Section  string `json:"section,omitempty"`
	Priority string `json:"priority,omitempty"`
	// StandardsVersion is the Debian policy version followed while packaging.
	StandardsVersion string `json:"standards_version,omitempty"`
	// Format is the source package format, for example "3.0 (quilt)".
	Format string `json:"format,omitempty"`
	// Directory is the directory holding the source package files (relative to the repository root),
	// for example pool/main/n/nginx.
	Directory string `json:"directory,omitempty"`
	// Files are the files of the source package (MD5) and Checksums maps each algorithm to its file
	// list.
	Files     []FileEntry            `json:"files,omitempty"`
	Checksums map[string][]FileEntry `json:"checksums,omitempty"`

	// Build dependency relations.
	BuildDepends        Dependencies `json:"build_depends,omitempty"`
	BuildDependsIndep   Dependencies `json:"build_depends_indep,omitempty"`
	BuildDependsArch    Dependencies `json:"build_depends_arch,omitempty"`
	BuildConflicts      Dependencies `json:"build_conflicts,omitempty"`
	BuildConflictsIndep Dependencies `json:"build_conflicts_indep,omitempty"`
	BuildConflictsArch  Dependencies `json:"build_conflicts_arch,omitempty"`

	// Version control addresses.
	VcsGit     string `json:"vcs_git,omitempty"`
	VcsBrowser string `json:"vcs_browser,omitempty"`
	VcsSvn     string `json:"vcs_svn,omitempty"`
	VcsHg      string `json:"vcs_hg,omitempty"`
	VcsBzr     string `json:"vcs_bzr,omitempty"`
	// Testsuite is the autopkgtest-related declaration.
	Testsuite string `json:"testsuite,omitempty"`

	// Suite and Component are the distribution and component the source package belongs to.
	Suite     string `json:"suite,omitempty"`
	Component string `json:"component,omitempty"`
	// RepoURL is the root address of the repository the package belongs to and RepoID is the
	// repository identifier.
	RepoURL string `json:"repo_url,omitempty"`
	RepoID  string `json:"repo_id,omitempty"`

	// Fields holds every field of the index (keyed by lower-case field name).
	Fields map[string]string `json:"fields,omitempty"`
}

// sourceDependencyFields lists the Source fields that must be parsed as dependency relations.
var sourceDependencyFields = []struct {
	field string
	set   func(*Source, Dependencies)
}{
	{"Build-Depends", func(s *Source, d Dependencies) { s.BuildDepends = d }},
	{"Build-Depends-Indep", func(s *Source, d Dependencies) { s.BuildDependsIndep = d }},
	{"Build-Depends-Arch", func(s *Source, d Dependencies) { s.BuildDependsArch = d }},
	{"Build-Conflicts", func(s *Source, d Dependencies) { s.BuildConflicts = d }},
	{"Build-Conflicts-Indep", func(s *Source, d Dependencies) { s.BuildConflictsIndep = d }},
	{"Build-Conflicts-Arch", func(s *Source, d Dependencies) { s.BuildConflictsArch = d }},
}

// sourceChecksumFields lists the file checksum fields of the Sources index.
var sourceChecksumFields = []struct {
	field string
	algo  string
}{
	{"Files", "md5"},
	{"Checksums-Sha1", "sha1"},
	{"Checksums-Sha256", "sha256"},
	{"Checksums-Sha512", "sha512"},
}

// ParseSources streams over the Sources index, calling fn once per parsed source package.
func ParseSources(r io.Reader, fn func(*Source) error) error {
	if fn == nil {
		return nil
	}
	return ParseStanzasFunc(r, func(stanza *Stanza) error {
		source, err := ParseSourceStanza(stanza)
		if err != nil {
			return err
		}
		return fn(source)
	})
}

// ParseSourceStanza parses one paragraph of the Sources index into a Source.
func ParseSourceStanza(stanza *Stanza) (*Source, error) {
	source := &Source{Checksums: make(map[string][]FileEntry, 4)}
	source.Name = stanza.Get("Package")
	if source.Name == "" {
		return nil, fmt.Errorf("%w: missing the Package field", ErrInvalidPackage)
	}
	source.Binary = splitList(stanza.Get("Binary"))
	source.Architecture = stanza.Get("Architecture")
	source.Maintainer = stanza.Get("Maintainer")
	source.Uploaders = splitCommaList(stanza.Get("Uploaders"))
	source.Homepage = stanza.Get("Homepage")
	source.Section = stanza.Get("Section")
	source.Priority = stanza.Get("Priority")
	source.StandardsVersion = stanza.Get("Standards-Version")
	source.Format = stanza.Get("Format")
	source.Directory = stanza.Get("Directory")
	source.VcsGit = stanza.Get("Vcs-Git")
	source.VcsBrowser = stanza.Get("Vcs-Browser")
	source.VcsSvn = stanza.Get("Vcs-Svn")
	source.VcsHg = stanza.Get("Vcs-Hg")
	source.VcsBzr = stanza.Get("Vcs-Bzr")
	source.Testsuite = stanza.Get("Testsuite")

	version := stanza.Get("Version")
	if version != "" {
		parsed, err := ParseVersion(version)
		if err != nil {
			return nil, fmt.Errorf("%w: %s %s", ErrInvalidPackage, source.Name, err)
		}
		source.Version = parsed
	}

	for _, item := range sourceChecksumFields {
		value := stanza.Get(item.field)
		if value == "" {
			continue
		}
		entries, err := ParseFileEntries(value, item.algo)
		if err != nil {
			return nil, fmt.Errorf("%w: %s field of %s: %w", ErrInvalidPackage, item.field, source.Name, err)
		}
		source.Checksums[item.algo] = entries
		if item.algo == "md5" {
			source.Files = entries
		}
	}

	for _, item := range sourceDependencyFields {
		value := stanza.Get(item.field)
		if value == "" {
			continue
		}
		deps, err := ParseDependencies(value)
		if err != nil {
			return nil, fmt.Errorf("%w: %s field of %s: %w", ErrInvalidPackage, item.field, source.Name, err)
		}
		item.set(source, deps)
	}

	source.Fields = make(map[string]string, stanza.Len())
	for _, name := range stanza.Names() {
		source.Fields[strings.ToLower(name)] = stanza.Get(name)
	}
	return source, nil
}

// splitList splits a list on whitespace and commas (the Binary field may be separated by spaces or
// commas).
func splitList(value string) []string {
	value = strings.ReplaceAll(value, "\n", " ")
	var items []string
	for _, field := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	}) {
		if field != "" {
			items = append(items, field)
		}
	}
	return items
}

// splitCommaList splits a multi-line list on commas (fields such as Uploaders).
func splitCommaList(value string) []string {
	value = strings.ReplaceAll(value, "\n", " ")
	var items []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

// ID returns a source package identifier of the form "nginx_1.22.1-9".
func (s *Source) ID() string {
	if s.Version.IsZero() {
		return s.Name
	}
	return s.Name + "_" + s.Version.String()
}

// String returns the source package identifier (the same as ID).
func (s *Source) String() string { return s.ID() }

// Field returns the raw field value from the index; the field name is case-insensitive.
func (s *Source) Field(name string) string { return s.Fields[strings.ToLower(name)] }

// HasField reports whether the given field exists in the index.
func (s *Source) HasField(name string) bool {
	_, ok := s.Fields[strings.ToLower(name)]
	return ok
}

// ChecksumOf returns the checksum of a file in the source package using the strongest algorithm.
func (s *Source) ChecksumOf(name string) (Checksum, bool) {
	for _, algo := range []string{"sha512", "sha256", "sha1", "md5"} {
		for _, entry := range s.Checksums[algo] {
			if entry.Path == name {
				return entry.Checksum, true
			}
		}
	}
	return Checksum{}, false
}

// DSC returns the .dsc file entry of the source package.
func (s *Source) DSC() (FileEntry, bool) {
	for _, entry := range s.Files {
		if strings.HasSuffix(entry.Path, ".dsc") {
			return entry, true
		}
	}
	return FileEntry{}, false
}

// FileURL returns the download address of a file in the source package (for example .dsc or
// .orig.tar.gz). The repository must have populated RepoURL and Directory.
func (s *Source) FileURL(name string) (string, error) {
	if s.RepoURL == "" {
		return "", fmt.Errorf("debrepo: source package %s has no repository address", s.ID())
	}
	base, err := url.Parse(s.RepoURL)
	if err != nil {
		return "", fmt.Errorf("debrepo: invalid repository address %q: %w", s.RepoURL, err)
	}
	if s.Directory == "" {
		return "", fmt.Errorf("debrepo: source package %s has no Directory field", s.ID())
	}
	reference := &url.URL{Path: path.Join(s.Directory, name)}
	return base.ResolveReference(reference).String(), nil
}
