package debrepo

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Version is a Debian version number: [epoch:]upstream[-revision].
//
//	1:1.22.1-9  ->  Epoch=1, Upstream="1.22.1", Revision="9"
//	2.36        ->  Epoch=0, Upstream="2.36",      Revision=""
type Version struct {
	// Epoch is the epoch, which is 0 when not given explicitly.
	Epoch int `json:"epoch"`
	// Upstream is the upstream version number.
	Upstream string `json:"upstream"`
	// Revision is the Debian revision (the part after the last '-'), which may be empty.
	Revision string `json:"revision"`
}

// ParseVersion parses a Debian version number. The epoch must be numeric and the upstream version must
// not be empty.
func ParseVersion(raw string) (Version, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Version{}, fmt.Errorf("%w: empty version number", ErrInvalidVersion)
	}
	v := Version{}
	rest := raw
	if colon := strings.IndexByte(rest, ':'); colon >= 0 {
		epoch := rest[:colon]
		value, err := strconv.Atoi(strings.TrimSpace(epoch))
		if err != nil || value < 0 {
			return Version{}, fmt.Errorf("%w: invalid epoch %q (from %q)", ErrInvalidVersion, epoch, raw)
		}
		v.Epoch = value
		rest = rest[colon+1:]
	}
	if dash := strings.LastIndexByte(rest, '-'); dash >= 0 {
		v.Upstream = rest[:dash]
		v.Revision = rest[dash+1:]
	} else {
		v.Upstream = rest
	}
	if v.Upstream == "" {
		return Version{}, fmt.Errorf("%w: missing upstream version (from %q)", ErrInvalidVersion, raw)
	}
	return v, nil
}

// MustParseVersion is like ParseVersion but panics when parsing fails, which makes it convenient in
// tests and for constants.
func MustParseVersion(raw string) Version {
	v, err := ParseVersion(raw)
	if err != nil {
		panic(err)
	}
	return v
}

// IsZero reports whether the version is empty.
func (v Version) IsZero() bool { return v.Upstream == "" && v.Revision == "" && v.Epoch == 0 }

// String returns "epoch:upstream-revision", omitting the epoch when it is 0 and the revision when it
// is empty.
func (v Version) String() string {
	var sb strings.Builder
	if v.Epoch != 0 {
		sb.WriteString(strconv.Itoa(v.Epoch))
		sb.WriteByte(':')
	}
	sb.WriteString(v.Upstream)
	if v.Revision != "" {
		sb.WriteByte('-')
		sb.WriteString(v.Revision)
	}
	return sb.String()
}

// Compare compares versions by the Debian rules: epoch first, then the upstream version, and finally
// the Debian revision. A result less than 0 means v is older, 0 means they are equal, and a result
// greater than 0 means v is newer.
func (v Version) Compare(o Version) int {
	switch {
	case v.Epoch < o.Epoch:
		return -1
	case v.Epoch > o.Epoch:
		return 1
	}
	if c := compareVersionPart(v.Upstream, o.Upstream); c != 0 {
		return c
	}
	return compareVersionPart(v.Revision, o.Revision)
}

// MarshalJSON renders the version as a Debian-style version string, and an empty version as null.
func (v Version) MarshalJSON() ([]byte, error) {
	if v.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(v.String())
}

// UnmarshalJSON parses a Debian-style version string.
func (v *Version) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := ParseVersion(raw)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

// MarshalText implements encoding.TextMarshaler.
func (v Version) MarshalText() ([]byte, error) { return []byte(v.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (v *Version) UnmarshalText(text []byte) error {
	parsed, err := ParseVersion(string(text))
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

// CompareVersions compares two version strings using the Debian version comparison rules
// (dpkg --compare-versions): a result less than 0 means a is older than b, 0 means they are equal, and
// a result greater than 0 means a is newer than b.
//
// The key behaviors that keep it consistent with dpkg:
//
//   - The epoch is compared first (defaulting to 0), so "1:1.0" > "2.0".
//   - A version is split into alternating non-numeric and numeric segments; numeric segments are
//     compared by value and non-numeric segments character by character, with letters sorting before
//     every non-letter character.
//   - "~" sorts before everything, including the empty string, so "1.0~rc1" < "1.0".
//   - An empty revision is smaller than any revision, so "1.0" < "1.0-1".
func CompareVersions(a, b string) int {
	va, errA := ParseVersion(a)
	vb, errB := ParseVersion(b)
	if errA != nil || errB != nil {
		// Fall back to a literal comparison when a version is invalid, so that a parse failure does
		// not make sorting impossible.
		return strings.Compare(a, b)
	}
	return va.Compare(vb)
}

// compareVersionPart implements verrevcmp from dpkg.
func compareVersionPart(a, b string) int {
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		// Non-numeric segments: compare character by character until a digit is reached.
		for (i < len(a) && !isDigit(a[i])) || (j < len(b) && !isDigit(b[j])) {
			ac, bc := 0, 0
			if i < len(a) {
				ac = versionOrder(a[i])
			}
			if j < len(b) {
				bc = versionOrder(b[j])
			}
			if ac != bc {
				return sign(ac - bc)
			}
			i++
			j++
		}

		// Numeric segments: skip leading zeros first, then compare by value (length plus the
		// per-digit difference).
		for i < len(a) && a[i] == '0' {
			i++
		}
		for j < len(b) && b[j] == '0' {
			j++
		}
		firstDiff := 0
		for i < len(a) && j < len(b) && isDigit(a[i]) && isDigit(b[j]) {
			if firstDiff == 0 {
				firstDiff = int(a[i]) - int(b[j])
			}
			i++
			j++
		}
		if i < len(a) && isDigit(a[i]) {
			return 1
		}
		if j < len(b) && isDigit(b[j]) {
			return -1
		}
		if firstDiff != 0 {
			return sign(firstDiff)
		}
	}
	return 0
}

// versionOrder returns the weight of a character during comparison, matching order() from dpkg.
func versionOrder(c byte) int {
	switch {
	case isDigit(c):
		return 0
	case isAlpha(c):
		return int(c)
	case c == '~':
		return -1
	default:
		return int(c) + 256
	}
}

func sign(v int) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	default:
		return 0
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isAlnum(c byte) bool { return isDigit(c) || isAlpha(c) }
