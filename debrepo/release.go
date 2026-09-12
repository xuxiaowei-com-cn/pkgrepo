package debrepo

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Time is a timestamp from deb822, for example the Release file's
// "Date: Sat, 11 Jul 2026 09:02:23 UTC".
type Time struct {
	// Raw is the original string.
	Raw   string
	value time.Time
}

// timeLayouts holds the several time formats seen in Release files.
var timeLayouts = []string{
	time.RFC1123,
	time.RFC1123Z,
	time.RFC822,
	time.RFC822Z,
	time.RFC3339,
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 2006 15:04:05 -0700",
}

// ParseTime parses the timestamp in a Release file; an empty string returns the zero value.
func ParseTime(raw string) (Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Time{}, nil
	}
	for _, layout := range timeLayouts {
		if value, err := time.Parse(layout, raw); err == nil {
			return Time{Raw: raw, value: value}, nil
		}
	}
	return Time{}, fmt.Errorf("debrepo: invalid timestamp %q", raw)
}

// Time returns the time.Time, or the zero time when it is empty.
func (t Time) Time() time.Time { return t.value }

// IsZero reports whether the timestamp is empty.
func (t Time) IsZero() bool { return t.value.IsZero() }

// IsSet reports whether the timestamp has been parsed.
func (t Time) IsSet() bool { return !t.value.IsZero() }

// After reports whether the timestamp is later than other (false when it is empty).
func (t Time) After(other time.Time) bool { return !t.value.IsZero() && t.value.After(other) }

// String returns the time in RFC3339 format (UTC), or an empty string when it is empty.
func (t Time) String() string {
	if t.value.IsZero() {
		return ""
	}
	return t.value.UTC().Format(time.RFC3339)
}

// Format formats the time with layout, or returns an empty string when it is empty.
func (t Time) Format(layout string) string {
	if t.value.IsZero() {
		return ""
	}
	return t.value.Format(layout)
}

// MarshalJSON renders the time as an RFC3339 string, and a zero value as null.
func (t Time) MarshalJSON() ([]byte, error) {
	if t.value.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.String())
}

// UnmarshalJSON parses an RFC3339 string or a Release-style timestamp.
func (t *Time) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := ParseTime(raw)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

// FileEntry is one line of a Release checksum section, describing the size and digest of an index
// file; the Sources index uses the same structure to describe the files inside a source package.
type FileEntry struct {
	// Path is the path relative to the repository root (Release) or to the source package directory
	// (Sources).
	Path string `json:"path"`
	// Size is the file size in bytes.
	Size int64 `json:"size"`
	// Checksum is the digest of the file.
	Checksum Checksum `json:"checksum"`
}

// Release is the parse result of dists/<suite>/Release (or InRelease): the distribution information
// of the repository plus the checksums of every index file.
type Release struct {
	// Origin and Label describe the origin of the repository.
	Origin string `json:"origin,omitempty"`
	Label  string `json:"label,omitempty"`
	// Suite is the suite name (stable, testing) and Codename is the codename (bookworm, trixie).
	Suite    string `json:"suite,omitempty"`
	Codename string `json:"codename,omitempty"`
	// Version is the distribution version number, for example "13.6".
	Version string `json:"version,omitempty"`
	// Description is the release description.
	Description string `json:"description,omitempty"`
	// Date is the creation time and ValidUntil is the expiry time (which may be empty).
	Date       Time `json:"date,omitempty"`
	ValidUntil Time `json:"valid_until,omitempty"`
	// AcquireByHash indicates that the repository supports fetching indexes by hash (which keeps
	// repository updates atomic).
	AcquireByHash bool `json:"acquire_by_hash,omitempty"`
	// NotAutomatic and ButAutomaticUpgrades are the apt behavior flags of a third-party repository.
	NotAutomatic         bool `json:"not_automatic,omitempty"`
	ButAutomaticUpgrades bool `json:"but_automatic_upgrades,omitempty"`
	// Architectures and Components are the architectures and components the repository provides.
	Architectures []string `json:"architectures,omitempty"`
	Components    []string `json:"components,omitempty"`

	stanza  *Stanza
	entries []FileEntry
	paths   []string
	byPath  map[string]FileEntry
	byAlgo  map[string]map[string]FileEntry
}

// checksumSections holds the names of the checksum sections in a Release file.
var checksumSections = map[string]string{
	"md5sum": "md5",
	"sha1":   "sha1",
	"sha256": "sha256",
	"sha512": "sha512",
}

// ParseRelease parses the content of Release or InRelease (the PGP signature of InRelease is stripped
// automatically).
func ParseRelease(r io.Reader) (*Release, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("debrepo: reading Release failed: %w", err)
	}
	cleared := ClearPGPArmor(data)
	var first *Stanza
	err = ParseStanzasFunc(strings.NewReader(string(cleared)), func(stanza *Stanza) error {
		if first == nil {
			copied := *stanza
			first = &copied
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if first == nil || first.Len() == 0 {
		return nil, fmt.Errorf("%w: the Release file is empty", ErrNotRepository)
	}
	return parseReleaseStanza(first)
}

func parseReleaseStanza(stanza *Stanza) (*Release, error) {
	release := &Release{
		stanza: stanza,
		byPath: make(map[string]FileEntry, 16),
		byAlgo: make(map[string]map[string]FileEntry, 4),
	}
	release.Origin = stanza.Get("Origin")
	release.Label = stanza.Get("Label")
	release.Suite = stanza.Get("Suite")
	release.Codename = stanza.Get("Codename")
	release.Version = stanza.Get("Version")
	release.Description = stanza.Get("Description")
	release.AcquireByHash = parseYesNo(stanza.Get("Acquire-By-Hash"))
	release.NotAutomatic = parseYesNo(stanza.Get("NotAutomatic")) || parseYesNo(stanza.Get("Not-Automatic"))
	release.ButAutomaticUpgrades = parseYesNo(stanza.Get("ButAutomaticUpgrades")) ||
		parseYesNo(stanza.Get("But-Automatic-Upgrades"))
	release.Architectures = strings.Fields(stanza.Get("Architectures"))
	release.Components = strings.Fields(stanza.Get("Components"))

	var err error
	if release.Date, err = ParseTime(stanza.Get("Date")); err != nil {
		return nil, err
	}
	if release.ValidUntil, err = ParseTime(stanza.Get("Valid-Until")); err != nil {
		return nil, err
	}

	for _, name := range stanza.Names() {
		algo, ok := checksumSections[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			continue
		}
		entries, err := ParseFileEntries(stanza.Get(name), algo)
		if err != nil {
			return nil, err
		}
		if release.byAlgo[algo] == nil {
			release.byAlgo[algo] = make(map[string]FileEntry, len(entries))
		}
		for _, entry := range entries {
			release.entries = append(release.entries, entry)
			release.byAlgo[algo][entry.Path] = entry
			existing, ok := release.byPath[entry.Path]
			if !ok {
				release.paths = append(release.paths, entry.Path)
			}
			if !ok || checksumRank(entry.Checksum.Type) > checksumRank(existing.Checksum.Type) {
				release.byPath[entry.Path] = entry
			}
		}
	}
	return release, nil
}

// ParseFileEntries parses a checksum section or the Files/Checksums-* fields of Sources: every line
// is "digest size file name".
func ParseFileEntries(value, algo string) ([]FileEntry, error) {
	var entries []FileEntry
	for _, line := range strings.Split(value, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 3 {
			return nil, fmt.Errorf("%w: %q in the %s section should be \"digest size path\"",
				ErrInvalidControl, strings.TrimSpace(line), algo)
		}
		size, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid size %q in the %s section", ErrInvalidControl, fields[1], algo)
		}
		entries = append(entries, FileEntry{
			Path:     fields[2],
			Size:     size,
			Checksum: Checksum{Type: algo, Value: fields[0]},
		})
	}
	return entries, nil
}

// Field returns the raw field value from the Release file; the field name is case-insensitive.
func (r *Release) Field(name string) string {
	if r == nil || r.stanza == nil {
		return ""
	}
	return r.stanza.Get(name)
}

// FieldNames returns the field names that appear in the Release file (in their original order).
func (r *Release) FieldNames() []string {
	if r == nil || r.stanza == nil {
		return nil
	}
	return r.stanza.Names()
}

// FieldsMap returns every field in the Release file (keyed by lower-case field name).
func (r *Release) FieldsMap() map[string]string {
	if r == nil || r.stanza == nil {
		return nil
	}
	fields := make(map[string]string, r.stanza.Len())
	for _, name := range r.stanza.Names() {
		fields[strings.ToLower(name)] = r.stanza.Get(name)
	}
	return fields
}

// MarshalJSON renders the parsed fields together with the raw fields from the Release file.
func (r *Release) MarshalJSON() ([]byte, error) {
	if r == nil {
		return []byte("null"), nil
	}
	type alias Release
	return json.Marshal(struct {
		*alias
		Fields map[string]string `json:"fields,omitempty"`
	}{(*alias)(r), r.FieldsMap()})
}

// Files returns every file entry recorded in the Release file (in order of appearance, including
// each checksum algorithm).
func (r *Release) Files() []FileEntry {
	if r == nil {
		return nil
	}
	entries := make([]FileEntry, len(r.entries))
	copy(entries, r.entries)
	return entries
}

// FilesByStrength returns the file entries deduplicated by path: only the entry with the strongest
// algorithm (sha512 > sha256 > sha1 > md5) is kept for each path, in the order the files first
// appear. This is usually the method to use when displaying the repository's index list, since it
// keeps the same path from appearing four times.
func (r *Release) FilesByStrength() []FileEntry {
	if r == nil {
		return nil
	}
	entries := make([]FileEntry, 0, len(r.paths))
	for _, path := range r.paths {
		if entry, ok := r.byPath[path]; ok {
			entries = append(entries, entry)
		}
	}
	return entries
}

// Lookup returns the file entry with the strongest algorithm (sha512 > sha256 > sha1 > md5) for a
// path.
func (r *Release) Lookup(path string) (FileEntry, bool) {
	if r == nil {
		return FileEntry{}, false
	}
	entry, ok := r.byPath[path]
	return entry, ok
}

// LookupAlgorithm returns the file entry for a path and algorithm.
func (r *Release) LookupAlgorithm(path, algo string) (FileEntry, bool) {
	if r == nil {
		return FileEntry{}, false
	}
	algo = strings.ToLower(strings.TrimSpace(algo))
	if normalized, ok := checksumSections[algo]; ok {
		algo = normalized
	}
	files, ok := r.byAlgo[algo]
	if !ok {
		return FileEntry{}, false
	}
	entry, ok := files[path]
	return entry, ok
}

// Has reports whether the Release file records the given path (that is, whether the index really
// exists).
func (r *Release) Has(path string) bool {
	_, ok := r.Lookup(path)
	return ok
}

// Expired reports whether the repository is past Valid-Until (always false when Valid-Until is not
// set).
func (r *Release) Expired(now time.Time) bool {
	return r != nil && r.ValidUntil.IsSet() && now.After(r.ValidUntil.Time())
}

// ClearPGPArmor strips the cleartext signature wrapper from InRelease and returns the body protected
// by the signature; when the input is not a PGP cleartext signature it is returned unchanged.
//
// The InRelease format (RFC 4880 cleartext signature framework):
//
//	-----BEGIN PGP SIGNED MESSAGE-----
//	Hash: SHA256
//
//	Origin: Debian
//	…
//	-----BEGIN PGP SIGNATURE-----
//	…
//	-----END PGP SIGNATURE-----
func ClearPGPArmor(data []byte) []byte {
	text := string(data)
	trimmed := strings.TrimLeft(text, "\r\n \t")
	const begin = "-----BEGIN PGP SIGNED MESSAGE-----"
	if !strings.HasPrefix(trimmed, begin) {
		return data
	}
	body := trimmed[len(begin):]
	body = strings.TrimLeft(body, "\r\n")
	// Skip headers such as "Hash: SHA256" up to the first blank line.
	if idx := strings.Index(body, "\r\n\r\n"); idx >= 0 {
		body = body[idx+4:]
	} else if idx := strings.Index(body, "\n\n"); idx >= 0 {
		body = body[idx+2:]
	}
	// Cut off the signature part.
	if idx := strings.Index(body, "-----BEGIN PGP SIGNATURE-----"); idx >= 0 {
		body = body[:idx]
	}
	// Remove the dash escape (the "- " prefix).
	var sb strings.Builder
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(line, "- ") {
			line = line[2:]
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	return []byte(sb.String())
}

// parseYesNo parses the Debian boolean spellings: yes/no, true/false, and 1/0.
func parseYesNo(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "yes", "true", "1", "y":
		return true
	default:
		return false
	}
}
